package codex

import (
	"time"

	"github.com/google/uuid"

	"github.com/sugihAF/contexo/internal/schema"
)

func normalizeCodexMessage(msg *CodexMessage, sessionID string, turn *int) *schema.SessionEvent {
	var eventType string
	var actor schema.ActorRef

	switch msg.Role {
	case "user":
		*turn++
		eventType = "user_message"
		actor = schema.ActorRef{Role: "user"}
	case "assistant":
		eventType = "assistant_message"
		actor = schema.ActorRef{Role: "assistant", Model: msg.Model}
	default:
		if msg.Type == "error" {
			eventType = "error"
			actor = schema.ActorRef{Role: "system"}
		} else {
			return nil
		}
	}

	return &schema.SessionEvent{
		Schema:  "ctx.session_event.v1",
		EventID: uuid.Must(uuid.NewV7()).String(),
		Ts:      time.Now().UTC(),
		Session: schema.SessionRef{
			ID:     sessionID,
			Source: "codex_cli",
		},
		Type:  eventType,
		Turn:  *turn,
		Actor: actor,
		Content: schema.Content{
			Text: content(msg),
		},
	}
}

func content(msg *CodexMessage) string {
	if msg.Error != "" {
		return msg.Error
	}
	return msg.Content
}
