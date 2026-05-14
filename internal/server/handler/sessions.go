package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SessionMeta represents server-side session metadata.
type SessionMeta struct {
	ID         string `json:"id"`
	RepoID     string `json:"repo_id"`
	Source     string `json:"source"`
	StartedAt  string `json:"started_at"`
	EndedAt    string `json:"ended_at,omitempty"`
	EventCount int    `json:"event_count"`
	Feature    string `json:"feature,omitempty"`
	S3Key      string `json:"s3_key,omitempty"`
}

// CreateSession handles POST /v1/repos/:repoId/sessions.
func (h *Handler) CreateSession(c *gin.Context) {
	var meta SessionMeta
	if err := c.ShouldBindJSON(&meta); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	meta.RepoID = c.Param("repoId")

	// In production, store to PostgreSQL
	c.JSON(http.StatusCreated, meta)
}

// GetSession handles GET /v1/repos/:repoId/sessions/:id.
func (h *Handler) GetSession(c *gin.Context) {
	// In production, read from PostgreSQL
	c.JSON(http.StatusOK, gin.H{
		"id":      c.Param("id"),
		"repo_id": c.Param("repoId"),
	})
}

// UploadSessionChunk handles PUT /v1/repos/:repoId/sessions/:id/chunks/:chunkId.
func (h *Handler) UploadSessionChunk(c *gin.Context) {
	// In production, upload to S3
	c.JSON(http.StatusOK, gin.H{
		"status":   "uploaded",
		"repo_id":  c.Param("repoId"),
		"session":  c.Param("id"),
		"chunk_id": c.Param("chunkId"),
	})
}

// GetSessionSlice handles GET /v1/repos/:repoId/sessions/:id/slice.
func (h *Handler) GetSessionSlice(c *gin.Context) {
	// In production, fetch from S3 and filter by turn range
	c.JSON(http.StatusOK, gin.H{
		"session_id": c.Param("id"),
		"events":     []interface{}{},
	})
}
