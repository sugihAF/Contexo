package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

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

// pushDetail builds the JSON detail for a push event: the paths actually
// committed (capped so a giant push doesn't bloat the row) and the announced
// client name (X-Contexo-Client), omitted when the client sent none. The
// shape is sync.ActivityDetail.
func pushDetail(changed []string, client string) string {
	const maxPaths = 50
	if len(changed) > maxPaths {
		changed = changed[:maxPaths]
	}
	b, err := json.Marshal(sync.ActivityDetail{Paths: changed, Client: client})
	if err != nil {
		return ""
	}
	return string(b)
}

// maxClientLen caps the stored X-Contexo-Client value. Real names look like
// "contexo-vscode/0.2.0" or "ctx-mcp"; the header is client-controlled and is
// persisted into every member's activity feed.
const maxClientLen = 128

// activityClient returns the request's X-Contexo-Client header, trimmed, with
// control and format characters dropped and capped at maxClientLen bytes on a
// rune boundary. Format characters (Cf: bidi overrides, zero-width) would let
// a member make the stored name render as another client in the feed.
func activityClient(c *gin.Context) string {
	raw := strings.TrimSpace(c.GetHeader("X-Contexo-Client"))
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > maxClientLen {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
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
