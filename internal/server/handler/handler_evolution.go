package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/diff"
	"github.com/sugihAF/contexo/internal/server/gitstore"
)

// EvolutionEntry pairs one commit with its diff against the prior version of
// the same path. For the very first commit (no prior version) the diff
// compares against an empty document, so every section surfaces as added.
type EvolutionEntry struct {
	Commit gitstore.CommitMeta `json:"commit"`
	Diff   diff.SectionDiff    `json:"diff"`
}

// Evolution handles GET /v1/repos/:id/evolution/*path?limit=N.
//
// Returns up to N most recent commits touching `*path`, each paired with the
// section-aware diff against its immediate prior commit for the same path.
// Designed for agents and humans who want the full trajectory of a page in a
// single round-trip instead of one history call + N diff calls.
//
// The answer is computed first and written only after memberAfterRead (see
// Diff).
func (h *Handler) Evolution(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireMember(c, repoID) {
		return
	}
	path := strings.TrimPrefix(c.Param("path"), "/")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing path"})
		return
	}
	limit := 20
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}
	status, body := h.evolution(repoID, path, limit, c.Query("blame") == "true")
	if !h.memberAfterRead(c, repoID) {
		return
	}
	c.JSON(status, body)
}

// evolution computes Evolution's answer, its status and JSON body, without
// writing it.
func (h *Handler) evolution(repoID, path string, limit int, blame bool) (int, any) {
	commits, err := h.store.LogPath(repoID, path, limit)
	if err != nil {
		if errors.Is(err, gitstore.ErrRepoNotFound) {
			return http.StatusNotFound, gin.H{"error": "repo not found"}
		}
		return http.StatusInternalServerError, gin.H{"error": err.Error()}
	}
	if len(commits) == 0 {
		return http.StatusNotFound, gin.H{"error": "path not present in repo"}
	}

	entries := make([]EvolutionEntry, 0, len(commits))
	for _, commit := range commits {
		toBytes, err := h.store.ReadAtSha(repoID, path, commit.SHA)
		if err != nil {
			// A path may legitimately be absent at the very tip of a rename
			// chain we couldn't follow; skip the entry rather than failing
			// the whole request.
			if errors.Is(err, gitstore.ErrPathNotAtSHA) {
				continue
			}
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		parentSHA, err := h.store.ResolveParentSHAForPath(repoID, path, commit.SHA)
		if err != nil && !errors.Is(err, gitstore.ErrUnknownSHA) {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		var fromBytes []byte
		if parentSHA != "" {
			fromBytes, err = h.store.ReadAtSha(repoID, path, parentSHA)
			if err != nil && !errors.Is(err, gitstore.ErrPathNotAtSHA) {
				return http.StatusInternalServerError, gin.H{"error": err.Error()}
			}
		}
		d := diff.PageSections(fromBytes, toBytes, parentSHA, commit.SHA)
		entries = append(entries, EvolutionEntry{Commit: commit, Diff: d})
	}

	if blame {
		for i := range entries {
			_ = annotateBlame(h, repoID, path, &entries[i].Diff)
		}
	}

	return http.StatusOK, gin.H{"path": path, "entries": entries}
}
