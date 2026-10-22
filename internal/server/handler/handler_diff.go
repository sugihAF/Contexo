package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/diff"
	"github.com/sugihAF/contexo/internal/server/gitstore"
)

// Diff handles GET /v1/repos/:id/diff/*path?from=&to=.
//
// Defaults when query params are omitted:
//   - to absent   → HEAD-for-this-path (most recent commit touching the path)
//   - from absent → parent commit of `to` for this path
//   - both absent → most recent change for the path (parent..head)
//
// Returns 400 if a sha doesn't resolve or if `from` defaults are impossible
// (e.g. the page only has one commit, so there is no parent). Returns 404 if
// the path doesn't exist at either side.
//
// The answer is computed first and written only after memberAfterRead, so
// nothing a read found (content, or which paths and commits exist) reaches a
// former member whose row a re-create purged during the read.
func (h *Handler) Diff(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireMember(c, repoID) {
		return
	}
	path := strings.TrimPrefix(c.Param("path"), "/")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing path"})
		return
	}
	status, body := h.diff(repoID, path, strings.TrimSpace(c.Query("from")), strings.TrimSpace(c.Query("to")), c.Query("blame") == "true")
	if !h.memberAfterRead(c, repoID) {
		return
	}
	c.JSON(status, body)
}

// diff computes Diff's answer, its status and JSON body, without writing it.
func (h *Handler) diff(repoID, path, fromSHA, toSHA string, blame bool) (int, any) {
	if toSHA == "" {
		head, err := h.store.HeadSHAForPath(repoID, path)
		if err != nil {
			if errors.Is(err, gitstore.ErrRepoNotFound) {
				return http.StatusNotFound, gin.H{"error": "repo not found"}
			}
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		if head == "" {
			return http.StatusNotFound, gin.H{"error": "path not present in repo"}
		}
		toSHA = head
	}

	if fromSHA == "" {
		parent, err := h.store.ResolveParentSHAForPath(repoID, path, toSHA)
		if err != nil {
			if errors.Is(err, gitstore.ErrUnknownSHA) {
				return http.StatusBadRequest, gin.H{"error": "unknown sha: " + toSHA}
			}
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		if parent == "" {
			return http.StatusBadRequest, gin.H{"error": "no parent commit for this path; pass ?from explicitly"}
		}
		fromSHA = parent
	}

	// Tolerate page-absent-at-one-side: if a sha exists but the path is
	// missing in that commit's tree, treat that side as an empty document so
	// the differ emits clean adds/removes. Only error if BOTH sides lack the
	// path (the file genuinely never existed in either revision).
	fromBytes, fromPathOK, err := readAtShaTolerant(h, repoID, path, fromSHA)
	if err != nil {
		return readAtShaError(err, fromSHA)
	}
	toBytes, toPathOK, err := readAtShaTolerant(h, repoID, path, toSHA)
	if err != nil {
		return readAtShaError(err, toSHA)
	}
	if !fromPathOK && !toPathOK {
		return http.StatusNotFound, gin.H{"error": "path not present at either sha"}
	}

	d := diff.PageSections(fromBytes, toBytes, fromSHA, toSHA)

	if blame {
		// Blame is best-effort; silently skip on failure rather than fail the
		// whole diff response. The base SectionDiff is still useful.
		_ = annotateBlame(h, repoID, path, &d)
	}

	return http.StatusOK, d
}

// annotateBlame walks the page's commit history oldest→newest and records the
// first commit each ## heading appeared in. Each SectionChange whose heading
// matches a recorded commit gets its IntroducedBy field populated.
//
// Walks up to a hard cap (200) for performance bound on pathological histories.
// Failures to read individual revisions are skipped, not propagated.
func annotateBlame(h *Handler, repoID, path string, d *diff.SectionDiff) error {
	commits, err := h.store.LogPath(repoID, path, 200)
	if err != nil {
		return err
	}
	headingFirst := map[string]*diff.Commit{}
	// commits are newest-first; walk reversed so older commits set the seen flag.
	for i := len(commits) - 1; i >= 0; i-- {
		commit := commits[i]
		bytes, err := h.store.ReadAtSha(repoID, path, commit.SHA)
		if err != nil {
			continue
		}
		for _, heading := range diff.ParseHeadings(bytes) {
			if _, seen := headingFirst[heading]; seen {
				continue
			}
			headingFirst[heading] = &diff.Commit{
				SHA: commit.SHA, Author: commit.Author, Email: commit.Email,
				Time: commit.Time, Message: commit.Message,
			}
		}
	}
	for i := range d.Sections {
		// Renamed sections were introduced under their *old* heading; prefer
		// the old heading's blame if known, otherwise fall back to the new.
		key := d.Sections[i].Heading
		if d.Sections[i].Status == diff.StatusRenamed && d.Sections[i].OldHeading != "" {
			if c, ok := headingFirst[d.Sections[i].OldHeading]; ok {
				d.Sections[i].IntroducedBy = c
				continue
			}
		}
		if c, ok := headingFirst[key]; ok {
			d.Sections[i].IntroducedBy = c
		}
	}
	return nil
}

// readAtShaTolerant returns (bytes, pathExisted, err). On ErrPathNotAtSHA it
// returns (nil, false, nil) so the caller can supply an empty document to the
// differ and get clean adds/removes. Other errors propagate.
func readAtShaTolerant(h *Handler, repoID, path, sha string) ([]byte, bool, error) {
	b, err := h.store.ReadAtSha(repoID, path, sha)
	if err == nil {
		return b, true, nil
	}
	if errors.Is(err, gitstore.ErrPathNotAtSHA) {
		return nil, false, nil
	}
	return nil, false, err
}

// readAtShaError is the status and JSON body Diff answers for a ReadAtSha
// error.
func readAtShaError(err error, sha string) (int, any) {
	switch {
	case errors.Is(err, gitstore.ErrUnknownSHA):
		return http.StatusBadRequest, gin.H{"error": "unknown sha: " + sha}
	case errors.Is(err, gitstore.ErrRepoNotFound):
		return http.StatusNotFound, gin.H{"error": "repo not found"}
	default:
		return http.StatusInternalServerError, gin.H{"error": err.Error()}
	}
}
