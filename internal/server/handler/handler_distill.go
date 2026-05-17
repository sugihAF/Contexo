package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Distill handles POST /v1/repos/:id/sync/distill.
//
// This is a Phase 2 stub. The real implementation (server-side reasoning-
// trail distillation as a fallback for clients that cannot run the
// agent-as-distiller flow) is Phase 4 in the rollout plan. Until then we
// return 501 so the CLI's --fallback-server flag has something to talk to
// and the contract is visible to the dashboard team.
func (h *Handler) Distill(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireMember(c, repoID) {
		return
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error":   "server-side distillation not implemented",
		"reason":  "Phase 4 of the agent-reasoning-capture rollout; use the agent-as-distiller flow (ctx_push via MCP) for now",
		"planned": true,
	})
}
