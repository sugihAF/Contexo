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
)

// HubHandler exposes the git-backed CtxHub endpoints.
type HubHandler struct {
	store *gitstore.Store
}

func NewHub(store *gitstore.Store) *HubHandler {
	return &HubHandler{store: store}
}

// CreateRepo handles POST /v1/repos/:id.
func (h *HubHandler) CreateRepo(c *gin.Context) {
	repoID := c.Param("id")
	if err := h.store.Init(repoID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"repo_id": repoID})
}

// ReadPage handles GET /v1/repos/:id/pages/*path. Returns content as
// text/markdown with X-Page-SHA header carrying the last-touch sha.
func (h *HubHandler) ReadPage(c *gin.Context) {
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

// PushRequest is the body of POST /v1/repos/:id/sync/push.
type PushRequest struct {
	AuthorName  string     `json:"author_name"`
	AuthorEmail string     `json:"author_email"`
	Message     string     `json:"message"`
	Files       []PushFile `json:"files"`
}

// PushFile is one file in a PushRequest.
type PushFile struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	ParentSHA string `json:"parent_sha,omitempty"`
}

// PushResponse is what the server returns for a push.
type PushResponse struct {
	NewHead   string               `json:"new_head"`
	Pushed    int                  `json:"pushed"`
	Conflicts []*gitstore.Conflict `json:"conflicts,omitempty"`
}

// Push handles POST /v1/repos/:id/sync/push.
// Auto-creates the repo if missing.
func (h *HubHandler) Push(c *gin.Context) {
	repoID := c.Param("id")
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
		return
	}
	var req PushRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad json: " + err.Error()})
		return
	}
	if req.AuthorName == "" {
		req.AuthorName = "unknown"
	}
	if req.AuthorEmail == "" {
		req.AuthorEmail = "unknown@ctxhub.local"
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

	var conflicts []*gitstore.Conflict
	pushed := 0
	var newHead string

	for _, file := range req.Files {
		sha, conflict, err := h.store.Write(repoID, file.Path, []byte(file.Content),
			req.AuthorName, req.AuthorEmail, req.Message, file.ParentSHA)
		if err != nil {
			if errors.Is(err, gitstore.ErrConflict) {
				conflicts = append(conflicts, conflict)
				continue
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		pushed++
		newHead = sha
	}

	if newHead == "" {
		// All files were conflicts; report HEAD as current
		newHead, _ = h.store.HeadSHA(repoID)
	}

	status := http.StatusOK
	if len(conflicts) > 0 {
		status = http.StatusConflict
	}
	c.JSON(status, PushResponse{
		NewHead:   newHead,
		Pushed:    pushed,
		Conflicts: conflicts,
	})
}

// PullResponse is what the server returns for a pull.
type PullResponse struct {
	NewHead string     `json:"new_head"`
	Files   []PullFile `json:"files"`
}

// PullFile is one file in a PullResponse.
type PullFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA     string `json:"sha"`
}

// Pull handles GET /v1/repos/:id/sync/pull?since=<sha>.
func (h *HubHandler) Pull(c *gin.Context) {
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

	files := make([]PullFile, 0, len(paths))
	for _, p := range paths {
		content, sha, err := h.store.Read(repoID, p)
		if err != nil {
			continue
		}
		files = append(files, PullFile{Path: p, Content: string(content), SHA: sha})
	}

	c.JSON(http.StatusOK, PullResponse{NewHead: head, Files: files})
}

// Timeline handles GET /v1/repos/:id/timeline?limit=N.
func (h *HubHandler) Timeline(c *gin.Context) {
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
func (h *HubHandler) History(c *gin.Context) {
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
