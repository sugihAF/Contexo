// Package server wires the Contexo HTTP routes.
package server

import (
	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server/handler"
)

// NewRouter wires the git-backed Contexo endpoints. CORS runs first (handles
// preflight OPTIONS without auth); /v1/auth/google is unauthenticated (it
// establishes the session). All other /v1/* requires a Bearer token that the
// resolver can map to a user_id (session JWT, PAT, or legacy API key).
func NewRouter(h *handler.Handler, resolver *auth.Resolver) *gin.Engine {
	r := gin.Default()
	r.Use(CORS())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Unauthenticated: establishes identity.
	r.POST("/v1/auth/google", h.GoogleAuth)

	// Authenticated routes.
	v1 := r.Group("/v1")
	v1.Use(auth.GinMiddleware(resolver.Validator()))

	v1.GET("/me", h.Me)

	v1.POST("/pats", h.CreatePAT)
	v1.GET("/pats", h.ListPATs)
	v1.DELETE("/pats/:id", h.DeletePAT)

	v1.GET("/repos", h.ListRepos)
	v1.POST("/repos", h.CreateRepo)
	v1.POST("/repos/join", h.JoinRepo)

	// Legacy: POST /v1/repos/:id (kept for back-compat with the old CLI).
	v1.POST("/repos/:id", h.CreateRepoLegacy)

	v1.GET("/repos/:id/pages", h.ListPages)
	v1.GET("/repos/:id/pages/*path", h.ReadPage)
	v1.POST("/repos/:id/sync/push", h.Push)
	v1.GET("/repos/:id/sync/pull", h.Pull)
	v1.POST("/repos/:id/sync/distill", h.Distill)
	v1.GET("/repos/:id/timeline", h.Timeline)
	v1.GET("/repos/:id/history/*path", h.History)
	v1.GET("/repos/:id/diff/*path", h.Diff)

	v1.POST("/repos/:id/invite-keys", h.MintInviteKey)
	v1.GET("/repos/:id/invite-keys", h.ListInviteKeys)
	v1.DELETE("/repos/:id/invite-keys/:keyId", h.DeleteInviteKey)

	v1.GET("/repos/:id/members", h.ListMembers)
	v1.DELETE("/repos/:id/members/:userId", h.RemoveMember)

	return r
}
