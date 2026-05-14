package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	CtxhubDir  = ".ctxhub"
	ConfigFile = "config.json"
)

// Config holds the local .ctxhub configuration.
type Config struct {
	Version     int    `json:"version"`
	RepoID      string `json:"repo_id,omitempty"`
	ServerURL   string `json:"server_url,omitempty"`
	LastPullSHA string `json:"last_pull_sha,omitempty"`
}

// DefaultHubConfig returns a Config seeded for a fresh .ctxhub.
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

// LoadHub reads config from .ctxhub/config.json, returning a default config
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
