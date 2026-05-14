package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CreateGitLink handles POST /v1/repos/:repoId/git-links.
func (h *Handler) CreateGitLink(c *gin.Context) {
	repoID := c.Param("repoId")

	var req struct {
		GitSHA   string `json:"git_sha" binding:"required"`
		CommitID string `json:"commit_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.svc.LinkGit(c.Request.Context(), repoID, req.GitSHA, req.CommitID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"git_sha":   req.GitSHA,
		"commit_id": req.CommitID,
		"repo_id":   repoID,
	})
}

// GetRelatedByGitSHA handles GET /v1/repos/:repoId/git/:gitSha/related.
func (h *Handler) GetRelatedByGitSHA(c *gin.Context) {
	repoID := c.Param("repoId")
	gitSHA := c.Param("gitSha")

	commitIDs, err := h.svc.GetByGitSHA(c.Request.Context(), repoID, gitSHA)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"git_sha":    gitSHA,
		"commit_ids": commitIDs,
		"repo_id":    repoID,
	})
}
