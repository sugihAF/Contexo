package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/cli"
	"github.com/sugihAF/contexo/internal/config"
)

func initTestCtxDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"init", "--root", root})
	require.NoError(t, cmd.Execute())
	return root
}

func TestStory11_CaptureOnStartsRecorder(t *testing.T) {
	// Use a manual directory to avoid t.TempDir() cleanup issues
	// (BoltDB holds file lock that Windows won't release during cleanup)
	root, err := os.MkdirTemp("", "story11-capture-on-*")
	require.NoError(t, err)
	defer os.RemoveAll(root)

	// Init ctx dir
	cmd0 := cli.NewRootCmd()
	cmd0.SetArgs([]string{"init", "--root", root})
	require.NoError(t, cmd0.Execute())

	// Use port 0 in config to avoid port conflicts
	cfg, err := config.Load(root)
	require.NoError(t, err)
	cfg.RecorderPort = 0
	require.NoError(t, config.Save(root, cfg))

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"capture", "on", "--client", "claude-code", "--root", root})

	// Run capture on in a goroutine since it now blocks on signal
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- cmd.Execute()
	}()

	// Wait for server to start by polling the state file
	ctxDir := config.CtxDirPath(root)
	var port int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(ctxDir, "capture_state.json"))
		if err == nil {
			var state map[string]interface{}
			if json.Unmarshal(data, &state) == nil {
				if active, ok := state["active"].(bool); ok && active {
					if p, ok := state["port"].(float64); ok {
						port = int(p)
						break
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Greater(t, port, 0, "recorder should have started on a port")

	// Verify the HTTP server is running
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	resp.Body.Close()

	// Verify hooks file was created
	hooksPath := filepath.Join(ctxDir, "hooks.json")
	_, err = os.Stat(hooksPath)
	assert.NoError(t, err)

	assert.Contains(t, buf.String(), "Capture started")

	// Send SIGINT to stop (on Windows, we'll just kill the process via the channel)
	// We can't easily send SIGINT on Windows in-process, so verify state and accept
	// Note: The goroutine is blocked waiting for signal, which is correct behavior
}

func TestStory11_CaptureStatus(t *testing.T) {
	root := initTestCtxDir(t)

	// Manually create state file (no need to start real server)
	ctxDir := config.CtxDirPath(root)
	state := map[string]interface{}{
		"active":   true,
		"paused":   false,
		"port":     19476,
		"adapters": []string{"claude_code"},
	}
	stateData, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(filepath.Join(ctxDir, "capture_state.json"), stateData, 0o644)

	// Check status
	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"capture", "status", "--root", root})
	require.NoError(t, cmd.Execute())

	output := buf.String()
	assert.Contains(t, output, "active")
	assert.Contains(t, output, "Port:")
}

func TestStory11_CapturePause(t *testing.T) {
	root := initTestCtxDir(t)

	// Manually create state file
	ctxDir := config.CtxDirPath(root)
	state := map[string]interface{}{
		"active":   true,
		"paused":   false,
		"port":     19476,
		"adapters": []string{"claude_code"},
	}
	stateData, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(filepath.Join(ctxDir, "capture_state.json"), stateData, 0o644)

	// Pause
	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"capture", "pause", "--root", root})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "paused")

	// Verify state file updated
	stateData2, err := os.ReadFile(filepath.Join(ctxDir, "capture_state.json"))
	require.NoError(t, err)
	var state2 map[string]interface{}
	json.Unmarshal(stateData2, &state2)
	assert.True(t, state2["paused"].(bool))
}

func TestStory11_CaptureResume(t *testing.T) {
	root := initTestCtxDir(t)

	// Manually create paused state file
	ctxDir := config.CtxDirPath(root)
	state := map[string]interface{}{
		"active":   true,
		"paused":   true,
		"port":     19476,
		"adapters": []string{"claude_code"},
	}
	stateData, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(filepath.Join(ctxDir, "capture_state.json"), stateData, 0o644)

	// Resume
	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"capture", "resume", "--root", root})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "resumed")

	// Verify state file updated
	stateData2, err := os.ReadFile(filepath.Join(ctxDir, "capture_state.json"))
	require.NoError(t, err)
	var state2 map[string]interface{}
	json.Unmarshal(stateData2, &state2)
	assert.False(t, state2["paused"].(bool))
}

func TestStory11_CaptureOff(t *testing.T) {
	root := initTestCtxDir(t)

	// Manually create active state + PID file
	ctxDir := config.CtxDirPath(root)
	state := map[string]interface{}{
		"active":   true,
		"paused":   false,
		"port":     19476,
		"adapters": []string{"claude_code"},
	}
	stateData, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(filepath.Join(ctxDir, "capture_state.json"), stateData, 0o644)
	os.WriteFile(filepath.Join(ctxDir, "recorder.pid"), []byte("12345"), 0o644)

	// Off
	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"capture", "off", "--root", root})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "stopped")

	// PID file should be removed
	_, err := os.Stat(filepath.Join(ctxDir, "recorder.pid"))
	assert.True(t, os.IsNotExist(err))
}

// TestStory11_CaptureOnAcceptsEvents verifies the recorder HTTP server accepts events.
func TestStory11_CaptureOnAcceptsEvents(t *testing.T) {
	root, err := os.MkdirTemp("", "story11-events-*")
	require.NoError(t, err)
	defer os.RemoveAll(root)

	// Init ctx dir
	cmd0 := cli.NewRootCmd()
	cmd0.SetArgs([]string{"init", "--root", root})
	require.NoError(t, cmd0.Execute())

	cfg, err := config.Load(root)
	require.NoError(t, err)
	cfg.RecorderPort = 0
	require.NoError(t, config.Save(root, cfg))

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"capture", "on", "--client", "claude-code", "--root", root})

	go func() {
		cmd.Execute()
	}()

	// Wait for the server to start
	ctxDir := config.CtxDirPath(root)
	var port int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(ctxDir, "capture_state.json"))
		if err == nil {
			var state map[string]interface{}
			if json.Unmarshal(data, &state) == nil {
				if active, ok := state["active"].(bool); ok && active {
					if p, ok := state["port"].(float64); ok {
						port = int(p)
						break
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Greater(t, port, 0, "recorder should have started")

	// Send invalid JSON - should get 400
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/event", port), "application/json", strings.NewReader("not json"))
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)
	resp.Body.Close()

	// Send valid event
	event := `{
		"schema": "ctx.session_event.v1",
		"event_id": "test-event-001",
		"ts": "2025-01-01T00:00:00Z",
		"session": {"id": "test-session-001", "source": "claude_code", "started_at": "2025-01-01T00:00:00Z"},
		"type": "user_message",
		"turn": 1,
		"content": {"text": "Hello world"}
	}`
	resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%d/event", port), "application/json", strings.NewReader(event))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	resp.Body.Close()

	// Verify JSONL file was created
	sessionDir := filepath.Join(ctxDir, "sessions", "claude_code")
	entries, err := os.ReadDir(sessionDir)
	require.NoError(t, err)
	assert.Greater(t, len(entries), 0, "JSONL session file should exist")
}
