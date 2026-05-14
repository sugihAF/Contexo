package claudecode

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sugihAF/contexo/internal/schema"
)

// HookPayload represents a Claude Code hook payload.
type HookPayload struct {
	Event     string          `json:"event"`
	SessionID string          `json:"session_id"`
	Turn      int             `json:"turn,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
	Prompt    string          `json:"prompt,omitempty"`
	Model     string          `json:"model,omitempty"`
	CWD       string          `json:"cwd,omitempty"`
}

// NormalizeUserPromptSubmit converts a UserPromptSubmit hook payload to a SessionEvent.
func NormalizeUserPromptSubmit(payload []byte) (*schema.SessionEvent, error) {
	var hp HookPayload
	if err := json.Unmarshal(payload, &hp); err != nil {
		return nil, fmt.Errorf("claudecode: unmarshal payload: %w", err)
	}

	event := &schema.SessionEvent{
		Schema:  "ctx.session_event.v1",
		EventID: newEventID(),
		Ts:      time.Now().UTC(),
		Session: schema.SessionRef{
			ID:     hp.SessionID,
			Source: "claude_code",
		},
		Type: "user_message",
		Turn: hp.Turn,
		Actor: schema.ActorRef{
			Role: "user",
		},
		Content: schema.Content{
			Text: hp.Prompt,
		},
	}

	return event, nil
}

// NormalizeStop converts a Stop hook payload to a SessionEvent.
func NormalizeStop(payload []byte) (*schema.SessionEvent, error) {
	var hp HookPayload
	if err := json.Unmarshal(payload, &hp); err != nil {
		return nil, fmt.Errorf("claudecode: unmarshal payload: %w", err)
	}

	text := ""
	if hp.Message != nil {
		text = string(hp.Message)
	}

	event := &schema.SessionEvent{
		Schema:  "ctx.session_event.v1",
		EventID: newEventID(),
		Ts:      time.Now().UTC(),
		Session: schema.SessionRef{
			ID:     hp.SessionID,
			Source: "claude_code",
		},
		Type: "assistant_message",
		Turn: hp.Turn,
		Actor: schema.ActorRef{
			Role:  "assistant",
			Model: hp.Model,
		},
		Content: schema.Content{
			Text: text,
		},
	}

	return event, nil
}

func newEventID() string {
	return uuid.Must(uuid.NewV7()).String()
}
