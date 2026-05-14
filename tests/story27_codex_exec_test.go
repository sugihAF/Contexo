package tests

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/cli"
)

func TestStory27_CodexExecDryRun(t *testing.T) {
	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"codex", "exec", "Fix the login bug", "--dry-run"})
	require.NoError(t, cmd.Execute())

	output := buf.String()
	assert.Contains(t, output, "Would run: codex --json")
	assert.Contains(t, output, "Fix the login bug")
}

func TestStory27_ParseCodexOutput(t *testing.T) {
	jsonLines := `{"type":"task_start","role":"system","content":"Starting task"}
{"type":"user_message","role":"user","content":"Fix the login bug"}
{"type":"assistant_message","role":"assistant","content":"I'll fix the login bug"}
{"type":"tool_use","role":"assistant","message":"Reading file auth.go"}
`

	reader := strings.NewReader(jsonLines)
	events, err := cli.ParseCodexOutput("test-session", reader)
	require.NoError(t, err)

	assert.Len(t, events, 4)

	assert.Equal(t, "task_start", events[0].Type)
	assert.Equal(t, "system", events[0].Actor.Role)
	assert.Equal(t, "Starting task", events[0].Content.Text)

	assert.Equal(t, "user_message", events[1].Type)
	assert.Equal(t, "user", events[1].Actor.Role)

	assert.Equal(t, "assistant_message", events[2].Type)
	assert.Equal(t, "assistant", events[2].Actor.Role)
	assert.Equal(t, "I'll fix the login bug", events[2].Content.Text)

	// tool_use has message, not content
	assert.Equal(t, "tool_use", events[3].Type)
	assert.Equal(t, "Reading file auth.go", events[3].Content.Text)

	// All events should have the session ID
	for _, evt := range events {
		assert.Equal(t, "test-session", evt.Session.ID)
		assert.Equal(t, "codex", evt.Session.Source)
		assert.Equal(t, "ctx.session_event.v1", evt.Schema)
	}

	// Turn numbers are sequential
	for i, evt := range events {
		assert.Equal(t, i+1, evt.Turn)
	}
}

func TestStory27_ParseCodexOutputInvalidJSON(t *testing.T) {
	jsonLines := `not valid json
{"type":"valid","role":"user","content":"Hello"}
more invalid stuff
`

	reader := strings.NewReader(jsonLines)
	events, err := cli.ParseCodexOutput("test-session", reader)
	require.NoError(t, err)

	// Only the valid line should be parsed
	assert.Len(t, events, 1)
	assert.Equal(t, "valid", events[0].Type)
}

func TestStory27_ParseCodexOutputEmpty(t *testing.T) {
	reader := strings.NewReader("")
	events, err := cli.ParseCodexOutput("test-session", reader)
	require.NoError(t, err)
	assert.Len(t, events, 0)
}
