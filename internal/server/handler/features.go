package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/schema"
)

// ListFeatures handles GET /v1/repos/:repoId/features.
func (h *Handler) ListFeatures(c *gin.Context) {
	repoID := c.Param("repoId")

	features, err := h.svc.ListFeatures(c.Request.Context(), repoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, features)
}

// GetFeatureOverview handles GET /v1/repos/:repoId/features/:feature/overview.
func (h *Handler) GetFeatureOverview(c *gin.Context) {
	repoID := c.Param("repoId")
	feature := c.Param("feature")

	overview, err := h.svc.GetFeatureOverview(c.Request.Context(), repoID, feature)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if overview == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "feature not found"})
		return
	}

	c.JSON(http.StatusOK, overview)
}

// PutFeatureOverview handles PUT /v1/repos/:repoId/features/:feature/overview.
func (h *Handler) PutFeatureOverview(c *gin.Context) {
	repoID := c.Param("repoId")
	feature := c.Param("feature")

	var overview schema.FeatureOverview
	if err := c.ShouldBindJSON(&overview); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	overview.RepoID = repoID
	overview.Feature = feature

	if err := h.svc.PutFeatureOverview(c.Request.Context(), &overview); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, overview)
}

// GetContextLevel handles GET /v1/repos/:repoId/context.
// Query params: level=feature|log|metadata, feature=<name>, limit=<n>
func (h *Handler) GetContextLevel(c *gin.Context) {
	repoID := c.Param("repoId")
	level := c.DefaultQuery("level", "feature")
	feature := c.Query("feature")

	switch level {
	case "feature":
		if feature == "" {
			// List all features
			features, err := h.svc.ListFeatures(c.Request.Context(), repoID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"level": "feature", "features": features})
			return
		}
		// Specific feature overview
		overview, err := h.svc.GetFeatureOverview(c.Request.Context(), repoID, feature)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"level": "feature", "overview": overview})

	case "log":
		commits, err := h.svc.ListCommitsByFeature(c.Request.Context(), repoID, feature)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"level": "log", "feature": feature, "commits": commits})

	case "metadata":
		c.JSON(http.StatusOK, gin.H{"level": "metadata", "repo_id": repoID})

	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid level, use: feature, log, metadata"})
	}
}
