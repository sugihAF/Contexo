package handler

import (
	"errors"
	"net/http"
	gosync "sync"

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
//
// The owner check and the insert of the key run under createRepoMu, as one
// step with respect to repo creation. An owner row read without it may be a
// stale row of an operator-deleted repo that a re-create of the id is about
// to purge, and the key inserted after the purge would be a live key of the
// new repo (in WAL mode the read sees the row while the purge is open, so a
// stale owner who keeps minting wins on every re-create). The body is read
// before the lock: a slow client must never hold it.
func (h *Handler) MintInviteKey(c *gin.Context) {
	repoID := c.Param("id")
	var req mintInviteRequest
	_ = c.ShouldBindJSON(&req)
	createRepoMu.Lock()
	defer createRepoMu.Unlock()
	if !h.requireOwner(c, repoID) {
		return
	}
	uid := h.userID(c)
	if auth.IsLegacy(uid) {
		// Legacy auth has no user_id we can record; reject for safety.
		c.JSON(http.StatusForbidden, gin.H{"error": "user session required to mint invite keys"})
		return
	}
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

// ListInviteKeys handles GET /v1/repos/:id/invite-keys. The owner check and
// the listing run under createRepoMu, so a stale owner of an operator-deleted
// repo never lists the keys of a re-created one (see MintInviteKey). The
// list is written after the lock is released: a long list sent to a client
// that reads slowly must never hold it.
func (h *Handler) ListInviteKeys(c *gin.Context) {
	repoID := c.Param("id")
	createRepoMu.Lock()
	unlock := gosync.OnceFunc(createRepoMu.Unlock)
	defer unlock()
	if !h.requireOwner(c, repoID) {
		return
	}
	keys, err := h.users.ListInviteKeys(repoID)
	unlock()
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

// DeleteInviteKey handles DELETE /v1/repos/:id/invite-keys/:keyId. The owner
// check and the delete run under createRepoMu, so a stale owner of an
// operator-deleted repo never deletes a key of a re-created one (see
// MintInviteKey).
func (h *Handler) DeleteInviteKey(c *gin.Context) {
	repoID := c.Param("id")
	createRepoMu.Lock()
	defer createRepoMu.Unlock()
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
//
// Everything from resolving the key to the member row (the membership, the
// member count, the owners, the member quota) runs under createRepoMu, as
// one step with respect to repo creation. A key resolved without it may be a
// stale key of an operator-deleted repo that a re-create of the id is about
// to purge, and the member row inserted after the purge would let its holder
// into the new repo (in WAL mode the read sees the key while the purge is
// open, so a client that keeps joining wins on every re-create). The body is
// read before the lock: a slow client must never hold it.
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
	createRepoMu.Lock()
	defer createRepoMu.Unlock()
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
	// Re-joining is idempotent (AddMember is INSERT OR IGNORE) and must not be
	// blocked by the member cap. Only a genuinely new member counts against it.
	alreadyMember, err := h.users.IsMember(repoID, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !alreadyMember {
		count, err := h.users.CountRepoMembers(repoID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		owners, err := h.users.RepoOwners(repoID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := h.quota.AllowMemberAdd(repoID, owners, count); err != nil {
			if h.writeQuotaError(c, err) {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
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
