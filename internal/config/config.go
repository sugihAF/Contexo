package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	ContexoDir  = ".contexo"
	ConfigFile = "config.json"

	// DefaultServerURL is what `ctx login` and other server-touching
	// commands fall back to when neither --server nor a saved
	// .contexo/config.json entry specifies one. Mirrors the dashboard's
	// hardcoded default so the two sides stay in sync.
	DefaultServerURL = "https://api.contexo.live"

	// DefaultDashboardURL is the public dashboard the browser-based
	// `ctx login` flow opens. Self-hosted setups can override via
	// --dashboard or by editing config.json's DashboardURL.
	DefaultDashboardURL = "https://app.contexo.live"
)

// Config holds the local .contexo configuration.
type Config struct {
	Version      int    `json:"version"`
	RepoID       string `json:"repo_id,omitempty"`
	ServerURL    string `json:"server_url,omitempty"`
	DashboardURL string `json:"dashboard_url,omitempty"`
	LastPullSHA  string `json:"last_pull_sha,omitempty"`
}

// DefaultConfig returns a Config seeded for a fresh .contexo.
func DefaultConfig() *Config {
	return &Config{Version: 1}
}

// ContexoDirPath returns the absolute .contexo directory path for the given root.
func ContexoDirPath(root string) string {
	return filepath.Join(root, ContexoDir)
}

// ContexoConfigPath returns the config.json path within .contexo.
func ContexoConfigPath(root string) string {
	return filepath.Join(ContexoDirPath(root), ConfigFile)
}

// FindProjectRoot walks up from start to the nearest ancestor directory that
// contains a .contexo/ directory and returns it — the way git locates .git.
// If none is found (including start itself), it returns start unchanged so
// callers fall through to the usual "no project here" handling. This lets a
// long-lived process (e.g. the MCP server) launched from a subdirectory still
// bind to the project, instead of only matching an exact cwd.
func FindProjectRoot(start string) string {
	dir := start
	for {
		if fi, err := os.Stat(ContexoDirPath(dir)); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start // reached the filesystem root without a match
		}
		dir = parent
	}
}

// Load reads config from .contexo/config.json, returning a default config
// if the file does not yet exist.
func Load(root string) (*Config, error) {
	data, err := os.ReadFile(ContexoConfigPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("config: read: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	return &cfg, nil
}

// Save writes config to .contexo/config.json, creating parent dirs.
func Save(root string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	path := ContexoConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: create dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	return nil
}
