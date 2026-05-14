package vscode

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sugihAF/contexo/internal/schema"
)

// VSCodeChatExport represents a VS Code chat session export.
type VSCodeChatExport struct {
	SessionID string          `json:"session_id"`
	Messages  []VSCodeMessage `json:"messages"`
	CreatedAt string          `json:"created_at,omitempty"`
}

// VSCodeMessage represents a single message in a VS Code chat export.
type VSCodeMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
	Model     string `json:"model,omitempty"`
}

// ImportChatJSON parses a VS Code chat export JSON and returns SessionEvents.
func ImportChatJSON(data []byte) ([]*schema.SessionEvent, error) {
	var export VSCodeChatExport
	if err := json.Unmarshal(data, &export); err != nil {
		return nil, fmt.Errorf("vscode: unmarshal export: %w", err)
	}

	sessionID := export.SessionID
	if sessionID == "" {
		sessionID = uuid.Must(uuid.NewV7()).String()
	}

	var events []*schema.SessionEvent
	turn := 0

	for _, msg := range export.Messages {
		var eventType string
		var actor schema.ActorRef

		switch msg.Role {
		case "user":
			turn++
			eventType = "user_message"
			actor = schema.ActorRef{Role: "user"}
		case "assistant":
			eventType = "assistant_message"
			actor = schema.ActorRef{Role: "assistant", Model: msg.Model}
		default:
			continue
		}

		ts := parseTimestamp(msg.Timestamp)

		evt := &schema.SessionEvent{
			Schema:  "ctx.session_event.v1",
			EventID: uuid.Must(uuid.NewV7()).String(),
			Ts:      ts,
			Session: schema.SessionRef{
				ID:     sessionID,
				Source: "vscode",
			},
			Type:  eventType,
			Turn:  turn,
			Actor: actor,
			Content: schema.Content{
				Text: msg.Content,
			},
		}
		events = append(events, evt)
	}

	return events, nil
}

func parseTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Now().UTC()
	}

	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}

	for _, f := range formats {
		if t, err := time.Parse(f, ts); err == nil {
			return t
		}
	}

	return time.Now().UTC()
}
