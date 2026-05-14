package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/schema"
)

// CreateCommit handles POST /v1/repos/:repoId/commits.
func (h *Handler) CreateCommit(c *gin.Context) {
	repoID := c.Param("repoId")

	var commit schema.ContextCommit
	if err := c.ShouldBindJSON(&commit); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.svc.CreateCommit(c.Request.Context(), repoID, &commit); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, commit)
}

// GetCommit handles GET /v1/repos/:repoId/commits/:id.
func (h *Handler) GetCommit(c *gin.Context) {
	repoID := c.Param("repoId")
	commitID := c.Param("id")

	commit, err := h.svc.GetCommit(c.Request.Context(), repoID, commitID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if commit == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "commit not found"})
		return
	}

	c.JSON(http.StatusOK, commit)
}

// ListCommits handles GET /v1/repos/:repoId/commits.
func (h *Handler) ListCommits(c *gin.Context) {
	repoID := c.Param("repoId")

	commits, err := h.svc.ListCommits(c.Request.Context(), repoID, 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, commits)
}

// ListCommitsByFeature handles GET /v1/repos/:repoId/features/:feature/commits.
func (h *Handler) ListCommitsByFeature(c *gin.Context) {
	repoID := c.Param("repoId")
	feature := c.Param("feature")

	commits, err := h.svc.ListCommitsByFeature(c.Request.Context(), repoID, feature)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, commits)
}
