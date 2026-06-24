package capture

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// pendingPromptPath is the sidecar holding the not-yet-paired user prompt for
// a session. Used by the Codex two-hook capture: UserPromptSubmit writes it,
// Stop consumes it. Lives beside the session buffer in _pending/.
func pendingPromptPath(contexoDir, sessionID string) string {
	return filepath.Join(contexoDir, filepath.FromSlash(PendingDirRel), sessionID+".prompt")
}

// WritePendingPrompt stores the latest user prompt for a session, overwriting
// any previous unpaired prompt.
func WritePendingPrompt(contexoDir, sessionID, prompt string) error {
	path := pendingPromptPath(contexoDir, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("capture: mkdir pending: %w", err)
	}
	if err := os.WriteFile(path, []byte(prompt), 0o644); err != nil {
		return fmt.Errorf("capture: write pending prompt: %w", err)
	}
	return nil
}

// TakePendingPrompt reads and removes the pending prompt for a session.
// Returns "" (no error) when there is none.
func TakePendingPrompt(contexoDir, sessionID string) (string, error) {
	path := pendingPromptPath(contexoDir, sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("capture: read pending prompt: %w", err)
	}
	_ = os.Remove(path)
	return string(data), nil
}
