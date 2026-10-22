package handler

import (
	"errors"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
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
//
// A signed-in caller must also be in the list itself, the same snapshot it
// answers with. requireMember checks in an earlier statement, without
// createRepoMu: a former member of an operator-deleted repo can pass it on a
// stale row a moment before a re-create of the id purges it, and the list,
// read after the new owner row, would show the new owner. A list without the
// caller's row is answered as requireMember answers a non-member.
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
	if uid := h.userID(c); !auth.IsLegacy(uid) && !slices.ContainsFunc(members, func(m userstore.Membership) bool {
		return m.UserID == uid
	}) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not a member of this repo"})
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
// remove a member, and the last owner cannot be removed. The owner check and
// the removal run under createRepoMu, as the invite-key routes do, so a stale
// owner of an operator-deleted repo never removes a member of a re-created
// one.
func (h *Handler) RemoveMember(c *gin.Context) {
	repoID := c.Param("id")
	createRepoMu.Lock()
	defer createRepoMu.Unlock()
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
