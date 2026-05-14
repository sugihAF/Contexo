package sync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// State tracks per-page sync metadata so the client knows which parent_sha
// to send on each push. Lives at .ctxhub/.sync/state.json.
type State struct {
	LastPullSHA string            `json:"last_pull_sha,omitempty"`
	PageSHAs    map[string]string `json:"page_shas,omitempty"`
}

// StatePath returns the sidecar file location under the hub dir.
func StatePath(hubDir string) string {
	return filepath.Join(hubDir, ".sync", "state.json")
}

// LoadState reads the sidecar; returns a zero-value State if the file is absent.
func LoadState(hubDir string) (*State, error) {
	data, err := os.ReadFile(StatePath(hubDir))
	if err != nil {
		if os.IsNotExist(err) {
			return &State{PageSHAs: map[string]string{}}, nil
		}
		return nil, fmt.Errorf("sync: load state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("sync: parse state: %w", err)
	}
	if s.PageSHAs == nil {
		s.PageSHAs = map[string]string{}
	}
	return &s, nil
}

// SaveState writes the sidecar, creating parent dirs as needed.
func SaveState(hubDir string, s *State) error {
	path := StatePath(hubDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("sync: state dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("sync: marshal state: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
