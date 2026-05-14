package tests

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/adapter/codex"
)

func TestStory09_ParseCodexStream(t *testing.T) {
	stream := `{"type":"message","role":"user","content":"Fix the bug"}
{"type":"message","role":"assistant","content":"I'll fix that","model":"o3-mini"}
{"type":"message","role":"user","content":"Thanks"}
`

	events, err := codex.ParseCodexStream(strings.NewReader(stream), "codex-sess-001")
	require.NoError(t, err)
	assert.Len(t, events, 3)
}

func TestStory09_MessageMapping(t *testing.T) {
	stream := `{"type":"message","role":"user","content":"Hello"}
{"type":"message","role":"assistant","content":"Hi there","model":"o3-mini"}
`

	events, err := codex.ParseCodexStream(strings.NewReader(stream), "codex-sess-002")
	require.NoError(t, err)
	require.Len(t, events, 2)

	assert.Equal(t, "user_message", events[0].Type)
	assert.Equal(t, "Hello", events[0].Content.Text)
	assert.Equal(t, "user", events[0].Actor.Role)

	assert.Equal(t, "assistant_message", events[1].Type)
	assert.Equal(t, "Hi there", events[1].Content.Text)
	assert.Equal(t, "assistant", events[1].Actor.Role)
	assert.Equal(t, "o3-mini", events[1].Actor.Model)
}

func TestStory09_ErrorEvent(t *testing.T) {
	stream := `{"type":"error","error":"command failed with exit code 1"}
`

	events, err := codex.ParseCodexStream(strings.NewReader(stream), "codex-sess-003")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "error", events[0].Type)
	assert.Contains(t, events[0].Content.Text, "command failed")
}

func TestStory09_SourceSetToCodexCLI(t *testing.T) {
	stream := `{"type":"message","role":"user","content":"test"}
`

	events, err := codex.ParseCodexStream(strings.NewReader(stream), "codex-sess-004")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "codex_cli", events[0].Session.Source)
}
