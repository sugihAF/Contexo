package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/userstore"
)

type memberBody struct {
	UserID  string `json:"user_id"`
	Email   string `json:"email"`
	Role    string `json:"role"`
	AddedAt int64  `json:"added_at"`
}

// ListMembers handles GET /v1/repos/:id/members. Any member of the repo may
// list its members.
func (h *Handler) ListMembers(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireMember(c, repoID) {
		return
	}
	if h.users == nil {
		c.JSON(http.StatusOK, gin.H{"members": []memberBody{}})
		return
	}
	members, err := h.users.ListRepoMembers(repoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]memberBody, 0, len(members))
	for _, m := range members {
		out = append(out, memberBody{
			UserID:  m.UserID,
			Email:   m.Email,
			Role:    m.Role,
			AddedAt: m.AddedAt.Unix(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"members": out})
}

// RemoveMember handles DELETE /v1/repos/:id/members/:userId. Only an owner may
// remove a member, and the last owner cannot be removed.
func (h *Handler) RemoveMember(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireOwner(c, repoID) {
		return
	}
	if h.users == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "user auth not configured"})
		return
	}
	userID := c.Param("userId")
	if err := h.users.RemoveMember(repoID, userID); err != nil {
		switch {
		case errors.Is(err, userstore.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "not a member of this repo"})
		case errors.Is(err, userstore.ErrLastOwner):
			c.JSON(http.StatusConflict, gin.H{"error": "cannot remove the last owner"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.Status(http.StatusNoContent)
}
