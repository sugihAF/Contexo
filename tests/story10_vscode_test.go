package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/adapter/vscode"
)

func TestStory10_ImportChatJSON(t *testing.T) {
	data := []byte(`{
		"session_id": "vscode-sess-001",
		"messages": [
			{"role": "user", "content": "What does this function do?", "timestamp": "2026-01-15T10:30:00Z"},
			{"role": "assistant", "content": "This function handles authentication.", "timestamp": "2026-01-15T10:30:05Z", "model": "copilot-gpt4"},
			{"role": "user", "content": "Can you improve it?", "timestamp": "2026-01-15T10:31:00Z"},
			{"role": "assistant", "content": "Here's an improved version.", "timestamp": "2026-01-15T10:31:10Z", "model": "copilot-gpt4"}
		]
	}`)

	events, err := vscode.ImportChatJSON(data)
	require.NoError(t, err)
	assert.Len(t, events, 4)
}

func TestStory10_MessageTypes(t *testing.T) {
	data := []byte(`{
		"session_id": "vscode-sess-002",
		"messages": [
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi there"}
		]
	}`)

	events, err := vscode.ImportChatJSON(data)
	require.NoError(t, err)
	require.Len(t, events, 2)

	assert.Equal(t, "user_message", events[0].Type)
	assert.Equal(t, "assistant_message", events[1].Type)
}

func TestStory10_TimestampsPreserved(t *testing.T) {
	data := []byte(`{
		"session_id": "vscode-sess-003",
		"messages": [
			{"role": "user", "content": "test", "timestamp": "2026-01-15T10:30:00Z"}
		]
	}`)

	events, err := vscode.ImportChatJSON(data)
	require.NoError(t, err)
	require.Len(t, events, 1)

	assert.Equal(t, 2026, events[0].Ts.Year())
	assert.Equal(t, 1, int(events[0].Ts.Month()))
	assert.Equal(t, 15, events[0].Ts.Day())
}

func TestStory10_SourceSetToVSCode(t *testing.T) {
	data := []byte(`{
		"session_id": "vscode-sess-004",
		"messages": [
			{"role": "user", "content": "test"}
		]
	}`)

	events, err := vscode.ImportChatJSON(data)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "vscode", events[0].Session.Source)
}
