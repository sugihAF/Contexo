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

	commits, err := h.store.LogPath(repoID, path, limit)
	if err != nil {
		if errors.Is(err, gitstore.ErrRepoNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "repo not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(commits) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "path not present in repo"})
		return
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		parentSHA, err := h.store.ResolveParentSHAForPath(repoID, path, commit.SHA)
		if err != nil && !errors.Is(err, gitstore.ErrUnknownSHA) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		var fromBytes []byte
		if parentSHA != "" {
			fromBytes, err = h.store.ReadAtSha(repoID, path, parentSHA)
			if err != nil && !errors.Is(err, gitstore.ErrPathNotAtSHA) {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		d := diff.PageSections(fromBytes, toBytes, parentSHA, commit.SHA)
		entries = append(entries, EvolutionEntry{Commit: commit, Diff: d})
	}

	c.JSON(http.StatusOK, gin.H{"path": path, "entries": entries})
}
