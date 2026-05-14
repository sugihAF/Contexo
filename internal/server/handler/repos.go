package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/server/service"
)

// Handler holds the HTTP handler dependencies.
type Handler struct {
	svc *service.Service
}

// New creates a Handler.
func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// CreateOrg handles POST /v1/orgs.
func (h *Handler) CreateOrg(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	org, err := h.svc.CreateOrg(c.Request.Context(), req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, org)
}

// ListOrgs handles GET /v1/orgs.
func (h *Handler) ListOrgs(c *gin.Context) {
	orgs, err := h.svc.ListOrgs(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, orgs)
}

// CreateRepo handles POST /v1/repos.
func (h *Handler) CreateRepo(c *gin.Context) {
	var req struct {
		OrgID string `json:"org_id" binding:"required"`
		Name  string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	repo, err := h.svc.CreateRepo(c.Request.Context(), req.OrgID, req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, repo)
}

// ListRepos handles GET /v1/repos.
func (h *Handler) ListRepos(c *gin.Context) {
	repos, err := h.svc.ListRepos(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, repos)
}
