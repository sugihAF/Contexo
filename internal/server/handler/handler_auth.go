package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/userstore"
)

type googleAuthRequest struct {
	IDToken string `json:"id_token"`
}

type googleAuthResponse struct {
	AccessToken string             `json:"access_token"`
	User        googleAuthUserBody `json:"user"`
}

type googleAuthUserBody struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// GoogleAuth handles POST /v1/auth/google. Verifies the Google ID token,
// upserts the user, and returns a session JWT. On the very first sign-in
// (when the users table is empty), the user inherits ownership of every
// existing un-owned repo on the server.
func (h *Handler) GoogleAuth(c *gin.Context) {
	if h.google == nil || h.signer == nil || h.users == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "google auth not configured"})
		return
	}
	var req googleAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.IDToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id_token required"})
		return
	}
	claims, err := h.google.Verify(req.IDToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	// Detect "first user ever" before insert.
	priorCount, err := h.users.CountUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	user, isNew, err := h.users.UpsertGoogleUser(claims.Email, claims.Name, claims.Subject)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if isNew && priorCount == 0 {
		// Bootstrap: this user becomes owner of every existing git repo
		// that has no owner row yet.
		repos, _ := h.store.ListRepos()
		for _, repoID := range repos {
			hasOwner, _ := h.users.RepoHasOwner(repoID)
			if !hasOwner {
				_ = h.users.AddMember(repoID, user.ID, userstore.RoleOwner)
			}
		}
	}

	token, _, err := h.signer.Mint(user.ID, user.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, googleAuthResponse{
		AccessToken: token,
		User: googleAuthUserBody{
			ID:    user.ID,
			Email: user.Email,
			Name:  user.Name,
		},
	})
}

// Me handles GET /v1/me. Returns the current user (rejecting legacy auth).
func (h *Handler) Me(c *gin.Context) {
	uid := h.userID(c)
	if h.users == nil || uid == "" || uid == "legacy:admin" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "session required"})
		return
	}
	user, err := h.users.GetUserByID(uid)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unknown user"})
		return
	}
	c.JSON(http.StatusOK, googleAuthUserBody{
		ID:    user.ID,
		Email: user.Email,
		Name:  user.Name,
	})
}
