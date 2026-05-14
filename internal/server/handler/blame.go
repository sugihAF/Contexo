package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// GetSymbolBlame handles GET /v1/repos/:repoId/symbols/:symbolKey/blame.
func (h *Handler) GetSymbolBlame(c *gin.Context) {
	repoID := c.Param("repoId")
	symbolKey := c.Param("symbolKey")

	commits, err := h.svc.GetBySymbol(c.Request.Context(), repoID, symbolKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"symbol_key": symbolKey,
		"repo_id":    repoID,
		"commits":    commits,
	})
}
