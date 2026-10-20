package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sugihAF/contexo/internal/fsutil"
)

// State tracks per-page sync metadata so the client knows which parent_sha
// to send on each push. Lives at .contexo/.sync/state.json.
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

// SaveState writes the sidecar atomically, so a concurrent reader never sees
// a torn file. It first creates the .sync dir with its self-ignoring
// .gitignore (E41). The JSON format is unchanged for old binaries.
func SaveState(hubDir string, s *State) error {
	path := StatePath(hubDir)
	if err := fsutil.EnsureSelfIgnoringDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync: state dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("sync: marshal state: %w", err)
	}
	return fsutil.WriteFileAtomic(path, data, 0o644)
}

// LoadStateOrEmpty is LoadState for readers that must not fail on a corrupt
// file: a parse failure gives an empty State with corrupt=true and a nil
// error. err reports only other I/O errors.
func LoadStateOrEmpty(hubDir string) (s *State, corrupt bool, err error) {
	data, err := os.ReadFile(StatePath(hubDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &State{PageSHAs: map[string]string{}}, false, nil
		}
		return nil, false, fmt.Errorf("sync: load state: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return &State{PageSHAs: map[string]string{}}, true, nil
	}
	if st.PageSHAs == nil {
		st.PageSHAs = map[string]string{}
	}
	return &st, false, nil
}
