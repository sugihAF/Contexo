package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/sync"
)

type activityBody struct {
	UserID    string          `json:"user_id"`
	Email     string          `json:"email"`
	Action    string          `json:"action"`
	Detail    json.RawMessage `json:"detail,omitempty"`
	CreatedAt int64           `json:"created_at"`
}

// Activity handles GET /v1/repos/:id/activity?limit=N&offset=M. Any member may
// view the repo's push/pull feed (newest first); total drives pagination.
func (h *Handler) Activity(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireMember(c, repoID) {
		return
	}
	if h.users == nil {
		c.JSON(http.StatusOK, gin.H{"events": []activityBody{}, "total": 0})
		return
	}
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}
	if limit > 500 {
		limit = 500
	}
	offset := 0
	if o, err := strconv.Atoi(c.Query("offset")); err == nil && o > 0 {
		offset = o
	}
	events, err := h.users.ListActivity(repoID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	total, err := h.users.CountActivity(repoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]activityBody, 0, len(events))
	for _, e := range events {
		out = append(out, activityBody{
			UserID:    e.UserID,
			Email:     e.Email,
			Action:    e.Action,
			Detail:    rawDetail(e.Detail),
			CreatedAt: e.CreatedAt.Unix(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"events": out, "total": total})
}

// rawDetail passes a stored detail JSON string straight through to the
// response, or nil (omitted) when there's none.
func rawDetail(detail string) json.RawMessage {
	if detail == "" {
		return nil
	}
	return json.RawMessage(detail)
}

// pushDetail builds the JSON detail for a push event: the paths committed
// (capped so a giant push doesn't bloat the row).
func pushDetail(pushed []sync.PushedFile) string {
	const maxPaths = 50
	paths := make([]string, 0, len(pushed))
	for i, p := range pushed {
		if i >= maxPaths {
			break
		}
		paths = append(paths, p.Path)
	}
	b, err := json.Marshal(map[string][]string{"paths": paths})
	if err != nil {
		return ""
	}
	return string(b)
}

// pullDetail builds the JSON detail for a pull event from the announced client
// name (X-Contexo-Client), or "" when the client didn't send one.
func pullDetail(client string) string {
	if client == "" {
		return ""
	}
	b, err := json.Marshal(map[string]string{"client": client})
	if err != nil {
		return ""
	}
	return string(b)
}

// recordActivity is a best-effort log of a member's action on a repo. detail is
// an optional JSON blob (pushed paths, pull client). It skips legacy auth (no
// user identity) and swallows errors so a failed write never breaks the
// underlying push/pull.
func (h *Handler) recordActivity(c *gin.Context, repoID, action, detail string) {
	uid := h.userID(c)
	if auth.IsLegacy(uid) || h.users == nil {
		return
	}
	_ = h.users.RecordActivity(repoID, uid, action, detail)
}
