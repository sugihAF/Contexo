package tests

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/cli"
	"github.com/sugihAF/contexo/internal/config"
)

func TestStory24_RemoteAdd(t *testing.T) {
	root := initTestCtxDir(t)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"remote", "add", "origin", "https://ctxhub.example.com", "--root", root})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "Remote 'origin' added")

	// Verify config
	cfg, err := config.Load(root)
	require.NoError(t, err)
	require.Len(t, cfg.Remotes, 1)
	assert.Equal(t, "origin", cfg.Remotes[0].Name)
	assert.Equal(t, "https://ctxhub.example.com", cfg.Remotes[0].URL)
	assert.Equal(t, "origin", cfg.RemoteName)
	assert.Equal(t, "https://ctxhub.example.com", cfg.ServerURL)
}

func TestStory24_RemoteAddDuplicate(t *testing.T) {
	root := initTestCtxDir(t)

	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"remote", "add", "origin", "https://a.com", "--root", root})
	require.NoError(t, cmd1.Execute())

	cmd2 := cli.NewRootCmd()
	cmd2.SetArgs([]string{"remote", "add", "origin", "https://b.com", "--root", root})
	err := cmd2.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestStory24_RemoteLs(t *testing.T) {
	root := initTestCtxDir(t)

	// Add two remotes
	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"remote", "add", "origin", "https://a.com", "--root", root})
	require.NoError(t, cmd1.Execute())

	cmd2 := cli.NewRootCmd()
	cmd2.SetArgs([]string{"remote", "add", "staging", "https://b.com", "--root", root})
	require.NoError(t, cmd2.Execute())

	var buf bytes.Buffer
	cmd3 := cli.NewRootCmd()
	cmd3.SetOut(&buf)
	cmd3.SetArgs([]string{"remote", "ls", "--root", root})
	require.NoError(t, cmd3.Execute())

	output := buf.String()
	assert.Contains(t, output, "origin")
	assert.Contains(t, output, "staging")
	assert.Contains(t, output, "https://a.com")
	assert.Contains(t, output, "https://b.com")
}

func TestStory24_AuthLoginWithFlag(t *testing.T) {
	root := initTestCtxDir(t)

	// Add a remote first so server URL is set
	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"remote", "add", "origin", "https://ctxhub.example.com", "--root", root})
	require.NoError(t, cmd1.Execute())

	var buf bytes.Buffer
	cmd2 := cli.NewRootCmd()
	cmd2.SetOut(&buf)
	cmd2.SetArgs([]string{"auth", "login", "--api-key", "test-key-123456789", "--root", root})
	require.NoError(t, cmd2.Execute())

	assert.Contains(t, buf.String(), "Authenticated successfully")

	// Verify credentials file
	creds, err := config.LoadCredentials(root)
	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "test-key-123456789", creds.APIKey)
	assert.Equal(t, "https://ctxhub.example.com", creds.ServerURL)
}

func TestStory24_AuthStatus(t *testing.T) {
	root := initTestCtxDir(t)

	// Login first
	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"auth", "login", "--api-key", "test-key-12345678", "--root", root})
	require.NoError(t, cmd1.Execute())

	var buf bytes.Buffer
	cmd2 := cli.NewRootCmd()
	cmd2.SetOut(&buf)
	cmd2.SetArgs([]string{"auth", "status", "--root", root})
	require.NoError(t, cmd2.Execute())

	output := buf.String()
	assert.Contains(t, output, "Authenticated: yes")
	assert.Contains(t, output, "test...5678") // masked API key
}

func TestStory24_AuthStatusNotAuthenticated(t *testing.T) {
	root := initTestCtxDir(t)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"auth", "status", "--root", root})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "Not authenticated")
}

func TestStory24_AuthLoginWithServerFlag(t *testing.T) {
	root := initTestCtxDir(t)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{
		"auth", "login",
		"--api-key", "my-key",
		"--server", "https://custom.server.com",
		"--root", root,
	})
	require.NoError(t, cmd.Execute())

	creds, err := config.LoadCredentials(root)
	require.NoError(t, err)
	assert.Equal(t, "https://custom.server.com", creds.ServerURL)

	crPath := filepath.Join(root, ".ctx", "credentials.json")
	assert.FileExists(t, crPath)
}
