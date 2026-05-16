// Package handler holds the HTTP handlers for the Contexo git-backed server.
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

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server/gitstore"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/userstore"
)

// Handler holds dependencies for every HTTP route.
type Handler struct {
	store  *gitstore.Store
	users  *userstore.Store
	signer *auth.SessionSigner
	google auth.Verifier
}

// New constructs a Handler. users/signer/google may be nil during tests of the
// gitstore-only paths.
func New(store *gitstore.Store, users *userstore.Store, signer *auth.SessionSigner, google auth.Verifier) *Handler {
	return &Handler{store: store, users: users, signer: signer, google: google}
}

// userID returns the authenticated user_id placed in the gin context by the
// auth middleware, or "" if missing.
func (h *Handler) userID(c *gin.Context) string {
	v, _ := c.Get("user_id")
	id, _ := v.(string)
	return id
}

// requireMember returns true (and writes the response) if the request is not
// authorized to act on repoID. Legacy auth always passes.
func (h *Handler) requireMember(c *gin.Context, repoID string) bool {
	uid := h.userID(c)
	if auth.IsLegacy(uid) {
		return true
	}
	if h.users == nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "user auth not configured"})
		return false
	}
	ok, err := h.users.IsMember(repoID, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	if !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not a member of this repo"})
		return false
	}
	return true
}

// requireOwner is like requireMember but also checks role=owner.
func (h *Handler) requireOwner(c *gin.Context, repoID string) bool {
	uid := h.userID(c)
	if auth.IsLegacy(uid) {
		return true
	}
	if h.users == nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "user auth not configured"})
		return false
	}
	role, err := h.users.GetRole(repoID, uid)
	if err != nil {
		if errors.Is(err, userstore.ErrNotFound) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not a member of this repo"})
			return false
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	if role != userstore.RoleOwner {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "owner role required"})
		return false
	}
	return true
}

// ListRepos handles GET /v1/repos. Returns every repo for legacy auth, or
// just the user's memberships for real users.
func (h *Handler) ListRepos(c *gin.Context) {
	uid := h.userID(c)
	all, err := h.store.ListReposWithMeta()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if auth.IsLegacy(uid) || h.users == nil {
		c.JSON(http.StatusOK, gin.H{"repos": all})
		return
	}
	memberships, err := h.users.ListUserRepos(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	allowed := make(map[string]string, len(memberships))
	for _, m := range memberships {
		allowed[m.RepoID] = m.Role
	}
	filtered := make([]repoSummaryWithRole, 0, len(allowed))
	for _, r := range all {
		if role, ok := allowed[r.ID]; ok {
			filtered = append(filtered, repoSummaryWithRole{RepoSummary: r, Role: role})
		}
	}
	c.JSON(http.StatusOK, gin.H{"repos": filtered})
}

type repoSummaryWithRole struct {
	gitstore.RepoSummary
	Role string `json:"role"`
}

// ListPages handles GET /v1/repos/:id/pages.
func (h *Handler) ListPages(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireMember(c, repoID) {
		return
	}
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

// CreateRepoLegacy handles the deprecated POST /v1/repos/:id used by the
// existing CLI's repo-init flow. Kept for back-compat with legacy auth; real
// users should use POST /v1/repos.
func (h *Handler) CreateRepoLegacy(c *gin.Context) {
	repoID := c.Param("id")
	uid := h.userID(c)
	if err := h.store.Init(repoID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !auth.IsLegacy(uid) && h.users != nil {
		_ = h.users.AddMember(repoID, uid, userstore.RoleOwner)
	}
	c.JSON(http.StatusCreated, gin.H{"repo_id": repoID})
}

// ReadPage handles GET /v1/repos/:id/pages/*path.
func (h *Handler) ReadPage(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireMember(c, repoID) {
		return
	}
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

// Push handles POST /v1/repos/:id/sync/push. For real users, the first push
// to a new repo auto-creates and assigns ownership.
func (h *Handler) Push(c *gin.Context) {
	repoID := c.Param("id")
	uid := h.userID(c)

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

	repoExists := h.store.Exists(repoID)
	if !repoExists {
		if err := h.store.Init(repoID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !auth.IsLegacy(uid) && h.users != nil {
			_ = h.users.AddMember(repoID, uid, userstore.RoleOwner)
		}
	} else {
		if !h.requireMember(c, repoID) {
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
	if !h.requireMember(c, repoID) {
		return
	}
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
	if !h.requireMember(c, repoID) {
		return
	}
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
	if !h.requireMember(c, repoID) {
		return
	}
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
