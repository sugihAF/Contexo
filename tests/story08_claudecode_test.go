package tests

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/adapter/claudecode"
)

func TestStory08_GenerateHooksConfig(t *testing.T) {
	data, err := claudecode.GenerateHooksConfig(19476)
	require.NoError(t, err)

	var config map[string]interface{}
	err = json.Unmarshal(data, &config)
	require.NoError(t, err)

	hooks, ok := config["hooks"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, hooks, "UserPromptSubmit")
	assert.Contains(t, hooks, "Stop")
}

func TestStory08_NormalizeUserPromptSubmit(t *testing.T) {
	payload := `{
		"event": "UserPromptSubmit",
		"session_id": "sess-123",
		"turn": 1,
		"prompt": "Fix the bug in main.go"
	}`

	evt, err := claudecode.NormalizeUserPromptSubmit([]byte(payload))
	require.NoError(t, err)

	assert.Equal(t, "ctx.session_event.v1", evt.Schema)
	assert.Equal(t, "sess-123", evt.Session.ID)
	assert.Equal(t, "claude_code", evt.Session.Source)
	assert.Equal(t, "user_message", evt.Type)
	assert.Equal(t, 1, evt.Turn)
	assert.Equal(t, "Fix the bug in main.go", evt.Content.Text)
	assert.Equal(t, "user", evt.Actor.Role)
}

func TestStory08_NormalizeStop(t *testing.T) {
	payload := `{
		"event": "Stop",
		"session_id": "sess-123",
		"turn": 2,
		"model": "claude-3.5-sonnet",
		"message": "I fixed the bug."
	}`

	evt, err := claudecode.NormalizeStop([]byte(payload))
	require.NoError(t, err)

	assert.Equal(t, "ctx.session_event.v1", evt.Schema)
	assert.Equal(t, "sess-123", evt.Session.ID)
	assert.Equal(t, "assistant_message", evt.Type)
	assert.Equal(t, "assistant", evt.Actor.Role)
	assert.Equal(t, "claude-3.5-sonnet", evt.Actor.Model)
}

func TestStory08_MissingOptionalFields(t *testing.T) {
	payload := `{
		"event": "UserPromptSubmit",
		"session_id": "sess-456",
		"prompt": "Hello"
	}`

	evt, err := claudecode.NormalizeUserPromptSubmit([]byte(payload))
	require.NoError(t, err)
	assert.Equal(t, "sess-456", evt.Session.ID)
	assert.Equal(t, 0, evt.Turn)
}

func TestStory08_EventIDsAreUUIDv7(t *testing.T) {
	payload := `{"event":"UserPromptSubmit","session_id":"s1","prompt":"hi"}`

	evt1, err := claudecode.NormalizeUserPromptSubmit([]byte(payload))
	require.NoError(t, err)
	evt2, err := claudecode.NormalizeUserPromptSubmit([]byte(payload))
	require.NoError(t, err)

	// UUIDs should be non-empty and different
	assert.NotEmpty(t, evt1.EventID)
	assert.NotEmpty(t, evt2.EventID)
	assert.NotEqual(t, evt1.EventID, evt2.EventID)
	// UUIDv7 has version 7 in the 13th character
	assert.Equal(t, byte('7'), evt1.EventID[14])
}
