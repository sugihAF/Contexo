package tests

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/cli"
	"github.com/sugihAF/contexo/internal/config"
)

func TestStory25_StatusShowsInitialized(t *testing.T) {
	root := initTestCtxDir(t)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"status", "--root", root})
	require.NoError(t, cmd.Execute())

	output := buf.String()
	assert.Contains(t, output, "Initialized: yes")
	assert.Contains(t, output, "Capture: inactive")
	assert.Contains(t, output, "Authenticated: no")
	assert.Contains(t, output, "Sessions: 0")
	assert.Contains(t, output, "Commits: 0")
}

func TestStory25_StatusShowsNotInitialized(t *testing.T) {
	root := t.TempDir() // empty dir, no .ctx

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"status", "--root", root})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "Not initialized")
}

func TestStory25_StatusShowsAuthenticated(t *testing.T) {
	root := initTestCtxDir(t)

	// Login
	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"auth", "login", "--api-key", "test-key", "--root", root})
	require.NoError(t, cmd1.Execute())

	var buf bytes.Buffer
	cmd2 := cli.NewRootCmd()
	cmd2.SetOut(&buf)
	cmd2.SetArgs([]string{"status", "--root", root})
	require.NoError(t, cmd2.Execute())

	assert.Contains(t, buf.String(), "Authenticated: yes")
}

func TestStory25_ConfigSet(t *testing.T) {
	root := initTestCtxDir(t)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"config", "set", "server_url", "https://example.com", "--root", root})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "Set server_url = https://example.com")

	// Verify
	cfg, err := config.Load(root)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com", cfg.ServerURL)
}

func TestStory25_ConfigSetRepoID(t *testing.T) {
	root := initTestCtxDir(t)

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"config", "set", "repo_id", "my-repo-123", "--root", root})
	require.NoError(t, cmd.Execute())

	cfg, _ := config.Load(root)
	assert.Equal(t, "my-repo-123", cfg.RepoID)
}

func TestStory25_ConfigSetInvalidKey(t *testing.T) {
	root := initTestCtxDir(t)

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"config", "set", "invalid_key", "value", "--root", root})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown config key")
}

func TestStory25_ConfigGet(t *testing.T) {
	root := initTestCtxDir(t)

	// Set a value first
	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"config", "set", "server_url", "https://example.com", "--root", root})
	require.NoError(t, cmd1.Execute())

	var buf bytes.Buffer
	cmd2 := cli.NewRootCmd()
	cmd2.SetOut(&buf)
	cmd2.SetArgs([]string{"config", "get", "server_url", "--root", root})
	require.NoError(t, cmd2.Execute())

	assert.Contains(t, buf.String(), "https://example.com")
}

func TestStory25_ConfigGetAll(t *testing.T) {
	root := initTestCtxDir(t)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"config", "get", "--root", root})
	require.NoError(t, cmd.Execute())

	output := buf.String()
	assert.Contains(t, output, "recorder_port")
	assert.Contains(t, output, "default_client")
}

func TestStory25_ConfigSetRecorderPort(t *testing.T) {
	root := initTestCtxDir(t)

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"config", "set", "recorder_port", "9999", "--root", root})
	require.NoError(t, cmd.Execute())

	cfg, _ := config.Load(root)
	assert.Equal(t, 9999, cfg.RecorderPort)
}

func TestStory25_ConfigSetInvalidPort(t *testing.T) {
	root := initTestCtxDir(t)

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"config", "set", "recorder_port", "not-a-number", "--root", root})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid port")
}
