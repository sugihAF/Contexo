package server

import (
	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server/gitstore"
	"github.com/sugihAF/contexo/internal/server/handler"
)

// NewHubRouter wires the git-backed CtxHub endpoints behind a bearer-key middleware.
func NewHubRouter(store *gitstore.Store, keyValidator auth.KeyValidator) *gin.Engine {
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	v1 := r.Group("/v1")
	v1.Use(auth.GinMiddleware(keyValidator))

	h := handler.NewHub(store)

	v1.POST("/repos/:id", h.CreateRepo)
	v1.GET("/repos/:id/pages/*path", h.ReadPage)
	v1.POST("/repos/:id/sync/push", h.Push)
	v1.GET("/repos/:id/sync/pull", h.Pull)
	v1.GET("/repos/:id/timeline", h.Timeline)
	v1.GET("/repos/:id/history/*path", h.History)

	return r
}
