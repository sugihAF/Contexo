package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/server/gitstore"
	"github.com/sugihAF/contexo/internal/sync"
)

// Handler exposes the git-backed Contexo endpoints.
type Handler struct {
	store *gitstore.Store
}

func New(store *gitstore.Store) *Handler {
	return &Handler{store: store}
}

// ListRepos handles GET /v1/repos.
func (h *Handler) ListRepos(c *gin.Context) {
	summaries, err := h.store.ListReposWithMeta()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"repos": summaries})
}

// ListPages handles GET /v1/repos/:id/pages.
func (h *Handler) ListPages(c *gin.Context) {
	repoID := c.Param("id")
	pages, err := h.store.ListPages(repoID)
	if err != nil {
		if errors.Is(err, gitstore.ErrRepoNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "repo not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"pages": pages})
}

// CreateRepo handles POST /v1/repos/:id.
func (h *Handler) CreateRepo(c *gin.Context) {
	repoID := c.Param("id")
	if err := h.store.Init(repoID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"repo_id": repoID})
}

// ReadPage handles GET /v1/repos/:id/pages/*path. Returns content as
// text/markdown with X-Page-SHA header carrying the last-touch sha.
func (h *Handler) ReadPage(c *gin.Context) {
	repoID := c.Param("id")
	path := strings.TrimPrefix(c.Param("path"), "/")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing page path"})
		return
	}

	content, sha, err := h.store.Read(repoID, path)
	if err != nil {
		if errors.Is(err, gitstore.ErrRepoNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "repo not found"})
			return
		}
		if errors.Is(err, os.ErrNotExist) {
			c.JSON(http.StatusNotFound, gin.H{"error": "page not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("X-Page-SHA", sha)
	c.Data(http.StatusOK, "text/markdown", content)
}

// Push handles POST /v1/repos/:id/sync/push.
func (h *Handler) Push(c *gin.Context) {
	repoID := c.Param("id")
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
		return
	}
	var req sync.PushRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad json: " + err.Error()})
		return
	}
	if req.AuthorName == "" {
		req.AuthorName = "unknown"
	}
	if req.AuthorEmail == "" {
		req.AuthorEmail = "unknown@contexo.local"
	}
	if req.Message == "" {
		req.Message = "ctx push"
	}

	if !h.store.Exists(repoID) {
		if err := h.store.Init(repoID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	var conflicts []sync.Conflict
	var pushed []sync.PushedFile
	var newHead string

	for _, file := range req.Files {
		sha, conflict, err := h.store.Write(repoID, file.Path, []byte(file.Content),
			req.AuthorName, req.AuthorEmail, req.Message, file.ParentSHA)
		if err != nil {
			if errors.Is(err, gitstore.ErrConflict) {
				conflicts = append(conflicts, sync.Conflict{
					Path:              conflict.Path,
					CurrentSHA:        conflict.CurrentSHA,
					CurrentContent:    conflict.CurrentContent,
					ExpectedParentSHA: conflict.ExpectedParentSHA,
				})
				continue
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		pushed = append(pushed, sync.PushedFile{Path: file.Path, SHA: sha})
		newHead = sha
	}

	if newHead == "" {
		newHead, _ = h.store.HeadSHA(repoID)
	}

	status := http.StatusOK
	if len(conflicts) > 0 {
		status = http.StatusConflict
	}
	c.JSON(status, sync.PushResponse{
		NewHead:   newHead,
		Pushed:    pushed,
		Conflicts: conflicts,
	})
}

// Pull handles GET /v1/repos/:id/sync/pull?since=<sha>.
func (h *Handler) Pull(c *gin.Context) {
	repoID := c.Param("id")
	since := c.Query("since")

	paths, head, err := h.store.ChangedSince(repoID, since)
	if err != nil {
		if errors.Is(err, gitstore.ErrRepoNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "repo not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	files := make([]sync.PullFile, 0, len(paths))
	for _, p := range paths {
		content, sha, err := h.store.Read(repoID, p)
		if err != nil {
			continue
		}
		files = append(files, sync.PullFile{Path: p, Content: string(content), SHA: sha})
	}

	c.JSON(http.StatusOK, sync.PullResponse{NewHead: head, Files: files})
}

// Timeline handles GET /v1/repos/:id/timeline?limit=N.
func (h *Handler) Timeline(c *gin.Context) {
	repoID := c.Param("id")
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}
	commits, err := h.store.Log(repoID, limit)
	if err != nil {
		if errors.Is(err, gitstore.ErrRepoNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "repo not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"commits": commits})
}

// History handles GET /v1/repos/:id/history/*path?limit=N.
func (h *Handler) History(c *gin.Context) {
	repoID := c.Param("id")
	path := strings.TrimPrefix(c.Param("path"), "/")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing path"})
		return
	}
	limit := 50
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
	c.JSON(http.StatusOK, gin.H{"commits": commits})
}
