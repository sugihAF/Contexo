package migrate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/sugihAF/contexo/internal/schema"
)

const manifestName = "migrate.json"

// ManifestItem is one staged source awaiting agent distillation.
type ManifestItem struct {
	AbsPath       string          `json:"abs_path"`
	RelPath       string          `json:"rel_path"`
	Detector      string          `json:"detector"`
	SuggestedType schema.PageType `json:"suggested_type"`
	SuggestedSlug string          `json:"suggested_slug"`
}

// Manifest is the pending-migration handoff written by the CLI picker and
// consumed by the ctx_migrate MCP tool.
type Manifest struct {
	Version       int            `json:"version"`
	Created       string         `json:"created"`
	SourceRoot    string         `json:"source_root"`
	ExternalCache string         `json:"external_cache,omitempty"`
	Label         string         `json:"label"`
	Kind          string         `json:"kind,omitempty"` // "migrate" (default) | "generate"
	Items         []ManifestItem `json:"items"`
}

func ManifestPath(contexoDir string) string {
	return filepath.Join(contexoDir, manifestName)
}

// LoadManifest returns the pending manifest, or (nil, nil) if none exists.
func LoadManifest(contexoDir string) (*Manifest, error) {
	data, err := os.ReadFile(ManifestPath(contexoDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func SaveManifest(contexoDir string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ManifestPath(contexoDir), data, 0o644)
}

// ClearManifest removes the manifest file and best-effort removes any
// external clone cache it referenced.
func ClearManifest(contexoDir string) error {
	if m, _ := LoadManifest(contexoDir); m != nil && m.ExternalCache != "" {
		_ = os.RemoveAll(m.ExternalCache)
	}
	err := os.Remove(ManifestPath(contexoDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
