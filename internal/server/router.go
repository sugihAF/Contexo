package server

import (
	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server/handler"
	"github.com/sugihAF/contexo/internal/server/service"
)

// NewRouter creates the Gin router with all routes.
func NewRouter(svc *service.Service, keyValidator auth.KeyValidator) *gin.Engine {
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	v1 := r.Group("/v1")
	v1.Use(auth.GinMiddleware(keyValidator))

	h := handler.New(svc)

	// Orgs
	v1.POST("/orgs", h.CreateOrg)
	v1.GET("/orgs", h.ListOrgs)

	// Repos
	v1.POST("/repos", h.CreateRepo)
	v1.GET("/repos", h.ListRepos)

	// Commits
	v1.POST("/repos/:repoId/commits", h.CreateCommit)
	v1.GET("/repos/:repoId/commits", h.ListCommits)
	v1.GET("/repos/:repoId/commits/:id", h.GetCommit)
	v1.GET("/repos/:repoId/features/:feature/commits", h.ListCommitsByFeature)

	// Sessions
	v1.POST("/repos/:repoId/sessions", h.CreateSession)
	v1.GET("/repos/:repoId/sessions/:id", h.GetSession)
	v1.PUT("/repos/:repoId/sessions/:id/chunks/:chunkId", h.UploadSessionChunk)
	v1.GET("/repos/:repoId/sessions/:id/slice", h.GetSessionSlice)

	// Features
	v1.GET("/repos/:repoId/features", h.ListFeatures)
	v1.GET("/repos/:repoId/features/:feature/overview", h.GetFeatureOverview)
	v1.PUT("/repos/:repoId/features/:feature/overview", h.PutFeatureOverview)

	// Context levels
	v1.GET("/repos/:repoId/context", h.GetContextLevel)

	// Git links
	v1.POST("/repos/:repoId/git-links", h.CreateGitLink)
	v1.GET("/repos/:repoId/git/:gitSha/related", h.GetRelatedByGitSHA)

	// Symbol blame
	v1.GET("/repos/:repoId/symbols/:symbolKey/blame", h.GetSymbolBlame)

	return r
}
