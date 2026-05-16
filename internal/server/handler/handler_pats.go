package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/userstore"
)

type patBody struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at,omitempty"`
}

type createPATRequest struct {
	Label string `json:"label"`
}

type createPATResponse struct {
	PAT   patBody `json:"pat"`
	Token string  `json:"token"`
}

// CreatePAT handles POST /v1/pats.
func (h *Handler) CreatePAT(c *gin.Context) {
	uid := h.userID(c)
	if h.users == nil || auth.IsLegacy(uid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "user session required"})
		return
	}
	var req createPATRequest
	_ = c.ShouldBindJSON(&req)

	pat, raw, err := h.users.MintPAT(uid, req.Label)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, createPATResponse{
		PAT:   toPATBody(*pat),
		Token: raw,
	})
}

// ListPATs handles GET /v1/pats.
func (h *Handler) ListPATs(c *gin.Context) {
	uid := h.userID(c)
	if h.users == nil || auth.IsLegacy(uid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "user session required"})
		return
	}
	pats, err := h.users.ListPATs(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]patBody, 0, len(pats))
	for _, p := range pats {
		out = append(out, toPATBody(p))
	}
	c.JSON(http.StatusOK, gin.H{"pats": out})
}

// DeletePAT handles DELETE /v1/pats/:id.
func (h *Handler) DeletePAT(c *gin.Context) {
	uid := h.userID(c)
	if h.users == nil || auth.IsLegacy(uid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "user session required"})
		return
	}
	id := c.Param("id")
	if err := h.users.DeletePAT(uid, id); err != nil {
		if errors.Is(err, userstore.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "pat not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func toPATBody(p userstore.PAT) patBody {
	out := patBody{
		ID:        p.ID,
		Label:     p.Label,
		CreatedAt: p.CreatedAt.Unix(),
	}
	if p.LastUsedAt != nil {
		ts := p.LastUsedAt.Unix()
		out.LastUsedAt = &ts
	}
	return out
}
