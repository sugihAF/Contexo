package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/cli"
	"github.com/sugihAF/contexo/internal/config"
)

func TestStory05_InitCreatesDirectories(t *testing.T) {
	root := t.TempDir()

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"init", "--root", root})
	err := cmd.Execute()
	require.NoError(t, err)

	// Check directories exist
	for _, dir := range []string{".ctx", ".ctx/sessions", ".ctx/commits", ".ctx/blobs"} {
		path := filepath.Join(root, dir)
		info, err := os.Stat(path)
		require.NoError(t, err, "directory %s should exist", dir)
		assert.True(t, info.IsDir())
	}
}

func TestStory05_InitCreatesConfig(t *testing.T) {
	root := t.TempDir()

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"init", "--root", root})
	require.NoError(t, cmd.Execute())

	cfgPath := config.ConfigPath(root)
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	var cfg map[string]interface{}
	err = json.Unmarshal(data, &cfg)
	require.NoError(t, err)
	assert.Equal(t, float64(1), cfg["version"])
}

func TestStory05_InitIdempotent(t *testing.T) {
	root := t.TempDir()

	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"init", "--root", root})
	require.NoError(t, cmd1.Execute())

	// Run again — should not error or overwrite
	cmd2 := cli.NewRootCmd()
	cmd2.SetArgs([]string{"init", "--root", root})
	require.NoError(t, cmd2.Execute())
}

func TestStory05_InitCreatesSQLite(t *testing.T) {
	root := t.TempDir()

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"init", "--root", root})
	require.NoError(t, cmd.Execute())

	dbPath := filepath.Join(root, ".ctx", "index.sqlite")
	_, err := os.Stat(dbPath)
	assert.NoError(t, err)
}

func TestStory05_InitCreatesBoltDB(t *testing.T) {
	root := t.TempDir()

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"init", "--root", root})
	require.NoError(t, cmd.Execute())

	boltPath := filepath.Join(root, ".ctx", "blobs.db")
	_, err := os.Stat(boltPath)
	assert.NoError(t, err)
}
