package claudecode

import (
	"encoding/json"
	"fmt"
)

// HookConfig represents the Claude Code hooks configuration.
type HookConfig struct {
	Hooks map[string]HookEntry `json:"hooks"`
}

// HookEntry represents a single hook configuration.
type HookEntry struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// GenerateHooksConfig returns a Claude Code hooks JSON config
// that POSTs hook payloads to the recorder HTTP endpoint.
func GenerateHooksConfig(port int) ([]byte, error) {
	config := HookConfig{
		Hooks: map[string]HookEntry{
			"UserPromptSubmit": {
				Command: fmt.Sprintf(`curl -s -X POST http://127.0.0.1:%d/event -H "Content-Type: application/json" -d "$CLAUDE_HOOK_PAYLOAD"`, port),
				Timeout: 5,
			},
			"Stop": {
				Command: fmt.Sprintf(`curl -s -X POST http://127.0.0.1:%d/event -H "Content-Type: application/json" -d "$CLAUDE_HOOK_PAYLOAD"`, port),
				Timeout: 5,
			},
		},
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("claudecode: marshal hooks config: %w", err)
	}
	return data, nil
}
