package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/userstore"
)

type inviteKeyBody struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

type mintInviteRequest struct {
	Label string `json:"label"`
}

type mintInviteResponse struct {
	Key   inviteKeyBody `json:"key"`
	Token string        `json:"token"`
}

// MintInviteKey handles POST /v1/repos/:id/invite-keys.
func (h *Handler) MintInviteKey(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireOwner(c, repoID) {
		return
	}
	uid := h.userID(c)
	if auth.IsLegacy(uid) {
		// Legacy auth has no user_id we can record; reject for safety.
		c.JSON(http.StatusForbidden, gin.H{"error": "user session required to mint invite keys"})
		return
	}
	var req mintInviteRequest
	_ = c.ShouldBindJSON(&req)
	k, raw, err := h.users.MintInviteKey(repoID, uid, req.Label)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, mintInviteResponse{
		Key:   toInviteKeyBody(*k),
		Token: raw,
	})
}

// ListInviteKeys handles GET /v1/repos/:id/invite-keys.
func (h *Handler) ListInviteKeys(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireOwner(c, repoID) {
		return
	}
	keys, err := h.users.ListInviteKeys(repoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]inviteKeyBody, 0, len(keys))
	for _, k := range keys {
		out = append(out, toInviteKeyBody(k))
	}
	c.JSON(http.StatusOK, gin.H{"keys": out})
}

// DeleteInviteKey handles DELETE /v1/repos/:id/invite-keys/:keyId.
func (h *Handler) DeleteInviteKey(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireOwner(c, repoID) {
		return
	}
	keyID := c.Param("keyId")
	if err := h.users.DeleteInviteKey(repoID, keyID); err != nil {
		if errors.Is(err, userstore.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "invite key not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

type joinRequest struct {
	Key string `json:"key"`
}

type joinResponse struct {
	RepoID string `json:"repo_id"`
	Role   string `json:"role"`
}

// JoinRepo handles POST /v1/repos/join.
func (h *Handler) JoinRepo(c *gin.Context) {
	uid := h.userID(c)
	if h.users == nil || auth.IsLegacy(uid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "user session required"})
		return
	}
	var req joinRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key required"})
		return
	}
	repoID, err := h.users.ResolveInviteKey(req.Key)
	if err != nil {
		switch {
		case errors.Is(err, userstore.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "invite key not recognized"})
		case errors.Is(err, userstore.ErrExpired):
			c.JSON(http.StatusGone, gin.H{"error": "invite key expired"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	if err := h.users.AddMember(repoID, uid, userstore.RoleMember); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	role, _ := h.users.GetRole(repoID, uid)
	c.JSON(http.StatusOK, joinResponse{RepoID: repoID, Role: role})
}

func toInviteKeyBody(k userstore.InviteKey) inviteKeyBody {
	return inviteKeyBody{
		ID:        k.ID,
		Label:     k.Label,
		CreatedAt: k.CreatedAt.Unix(),
		ExpiresAt: k.ExpiresAt.Unix(),
	}
}
