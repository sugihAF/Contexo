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

	fromSHA := strings.TrimSpace(c.Query("from"))
	toSHA := strings.TrimSpace(c.Query("to"))

	if toSHA == "" {
		head, err := h.store.HeadSHAForPath(repoID, path)
		if err != nil {
			if errors.Is(err, gitstore.ErrRepoNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "repo not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if head == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "path not present in repo"})
			return
		}
		toSHA = head
	}

	if fromSHA == "" {
		parent, err := h.store.ResolveParentSHAForPath(repoID, path, toSHA)
		if err != nil {
			if errors.Is(err, gitstore.ErrUnknownSHA) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "unknown sha: " + toSHA})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if parent == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no parent commit for this path; pass ?from explicitly"})
			return
		}
		fromSHA = parent
	}

	fromBytes, err := h.store.ReadAtSha(repoID, path, fromSHA)
	if err != nil {
		switch {
		case errors.Is(err, gitstore.ErrUnknownSHA):
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown sha: " + fromSHA})
		case errors.Is(err, gitstore.ErrPathNotAtSHA):
			c.JSON(http.StatusNotFound, gin.H{"error": "path not present at sha " + fromSHA})
		case errors.Is(err, gitstore.ErrRepoNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "repo not found"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	toBytes, err := h.store.ReadAtSha(repoID, path, toSHA)
	if err != nil {
		switch {
		case errors.Is(err, gitstore.ErrUnknownSHA):
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown sha: " + toSHA})
		case errors.Is(err, gitstore.ErrPathNotAtSHA):
			c.JSON(http.StatusNotFound, gin.H{"error": "path not present at sha " + toSHA})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	d := diff.PageSections(fromBytes, toBytes, fromSHA, toSHA)
	c.JSON(http.StatusOK, d)
}
