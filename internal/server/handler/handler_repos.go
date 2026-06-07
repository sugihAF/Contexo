package handler

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/userstore"
)

var repoIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

type createRepoRequest struct {
	ID string `json:"id"`
}

type createRepoResponse struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

// CreateRepo handles POST /v1/repos. The caller becomes the owner.
func (h *Handler) CreateRepo(c *gin.Context) {
	uid := h.userID(c)
	if h.users == nil || auth.IsLegacy(uid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "user session required"})
		return
	}
	var req createRepoRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
		return
	}
	if !repoIDPattern.MatchString(req.ID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id must match [A-Za-z0-9][A-Za-z0-9_-]{0,63}"})
		return
	}
	exists := h.store.Exists(req.ID)
	if exists {
		// If repo already exists on disk but has no owner, claim it.
		hasOwner, err := h.users.RepoHasOwner(req.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if hasOwner {
			c.JSON(http.StatusConflict, gin.H{"error": "repo already exists"})
			return
		}
	}

	// The caller is about to become a new owner (of a fresh repo or an orphan
	// claim). Consult the quota before creating anything, so a rejection leaves
	// no orphan repo on disk. On the OSS build the policy is Unlimited.
	if !h.enforceRepoQuota(c, uid) {
		return
	}

	if !exists {
		if err := h.store.Init(req.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := h.users.AddMember(req.ID, uid, userstore.RoleOwner); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, createRepoResponse{ID: req.ID, Role: userstore.RoleOwner})
}
