package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	CtxDir     = ".ctx"
	ConfigFile = "config.json"
)

// Remote describes a named remote server.
type Remote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Config holds the local .ctx configuration.
type Config struct {
	Version        int      `json:"version"`
	RecorderPort   int      `json:"recorder_port"`
	DefaultClient  string   `json:"default_client"`
	RedactionLevel string   `json:"redaction_level"`
	ServerURL      string   `json:"server_url,omitempty"`
	RepoID         string   `json:"repo_id,omitempty"`
	RemoteName     string   `json:"remote_name,omitempty"`
	Remotes        []Remote `json:"remotes,omitempty"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Version:        1,
		RecorderPort:   19476,
		DefaultClient:  "claude_code",
		RedactionLevel: "standard",
	}
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
