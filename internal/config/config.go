package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	CtxDir     = ".ctx"     // deprecated — kept until legacy CLI is removed
	CtxhubDir  = ".ctxhub"  // current per-project knowledge directory
	ConfigFile = "config.json"
)

// Remote describes a named remote server.
type Remote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Config holds the local .ctxhub configuration.
type Config struct {
	Version     int    `json:"version"`
	RepoID      string `json:"repo_id,omitempty"`
	ServerURL   string `json:"server_url,omitempty"`
	LastPullSHA string `json:"last_pull_sha,omitempty"`

	// Legacy fields preserved for backward compat with .ctx/config.json.
	RecorderPort   int      `json:"recorder_port,omitempty"`
	DefaultClient  string   `json:"default_client,omitempty"`
	RedactionLevel string   `json:"redaction_level,omitempty"`
	RemoteName     string   `json:"remote_name,omitempty"`
	Remotes        []Remote `json:"remotes,omitempty"`
}

// DefaultConfig returns a legacy Config with .ctx-era defaults.
// Deprecated: use DefaultHubConfig for new .ctxhub setups.
func DefaultConfig() *Config {
	return &Config{
		Version:        1,
		RecorderPort:   19476,
		DefaultClient:  "claude_code",
		RedactionLevel: "standard",
	}
}

// DefaultHubConfig returns a Config seeded for the .ctxhub layout.
func DefaultHubConfig() *Config {
	return &Config{Version: 1}
}

// CtxhubDirPath returns the absolute .ctxhub directory path for the given root.
func CtxhubDirPath(root string) string {
	return filepath.Join(root, CtxhubDir)
}

// CtxhubConfigPath returns the config.json path within .ctxhub.
func CtxhubConfigPath(root string) string {
	return filepath.Join(CtxhubDirPath(root), ConfigFile)
}

// LoadHub reads config from .ctxhub/config.json, returning default config
// if the file does not yet exist.
func LoadHub(root string) (*Config, error) {
	data, err := os.ReadFile(CtxhubConfigPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultHubConfig(), nil
		}
		return nil, fmt.Errorf("config: read: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	return &cfg, nil
}

// SaveHub writes config to .ctxhub/config.json, creating parent dirs.
func SaveHub(root string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	path := CtxhubConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: create dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	return nil
}

// CtxDirPath returns the absolute .ctx directory path for the given root.
func CtxDirPath(root string) string {
	return filepath.Join(root, CtxDir)
}

// ConfigPath returns the config.json path within .ctx.
func ConfigPath(root string) string {
	return filepath.Join(CtxDirPath(root), ConfigFile)
}

// Load reads config from .ctx/config.json.
func Load(root string) (*Config, error) {
	data, err := os.ReadFile(ConfigPath(root))
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

// Save writes config to .ctx/config.json.
func Save(root string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	path := ConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: create dir: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	return nil
}
