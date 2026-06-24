package agentwire

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CaptureCommandCodex is the shell command Contexo installs into Codex's
// Stop and UserPromptSubmit hooks. Codex runs it with the hook payload on
// stdin; `ctx capture turn` dispatches on the payload's hook_event_name.
const CaptureCommandCodex = "ctx capture turn --agent codex"

// codexCaptureEvents are the Codex hook events Contexo wires for capture:
// UserPromptSubmit stashes the prompt, Stop pairs it with the assistant reply.
var codexCaptureEvents = []string{"Stop", "UserPromptSubmit"}

// CodexHooksPath is Codex's project-local hooks config.
func CodexHooksPath(root string) string { return filepath.Join(root, ".codex", "hooks.json") }

// WireCodexHooks adds the Contexo capture command to Codex's Stop and
// UserPromptSubmit hooks in <root>/.codex/hooks.json, creating/merging the
// file and preserving any other hooks. Returns changed=false when both were
// already present.
func WireCodexHooks(root string) (changed bool, err error) {
	path := CodexHooksPath(root)
	obj, err := loadJSONObject(path)
	if err != nil {
		return false, err
	}
	added := false
	for _, ev := range codexCaptureEvents {
		if wireCommandHook(obj, ev, CaptureCommandCodex) {
			added = true
		}
	}
	if !added {
		return false, nil
	}
	if err := writeJSONObject(path, obj); err != nil {
		return false, err
	}
	return true, nil
}

// UnwireCodexHooks removes the Contexo capture command from Codex's Stop and
// UserPromptSubmit hooks, deleting the file if nothing else remains.
func UnwireCodexHooks(root string) (removed bool, deletedFile bool, err error) {
	path := CodexHooksPath(root)
	obj, err := loadJSONObject(path)
	if err != nil {
		return false, false, err
	}
	for _, ev := range codexCaptureEvents {
		if unwireCommandHook(obj, ev, CaptureCommandCodex) {
			removed = true
		}
	}
	if !removed {
		return false, false, nil
	}

	// Collapse an emptied hooks map, then delete a now-empty file outright.
	if hooks, _ := obj["hooks"].(map[string]interface{}); len(hooks) == 0 {
		delete(obj, "hooks")
	}
	if len(obj) == 0 {
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return true, false, fmt.Errorf("agentwire: remove %s: %w", path, rmErr)
		}
		return true, true, nil
	}
	if err := writeJSONObject(path, obj); err != nil {
		return true, false, err
	}
	return true, false, nil
}

// CodexHooksWired reports whether Codex's Stop hook has the Contexo capture
// command.
func CodexHooksWired(root string) (bool, error) {
	obj, err := loadJSONObject(CodexHooksPath(root))
	if err != nil {
		return false, err
	}
	return hasCommandHook(obj, "Stop", CaptureCommandCodex), nil
}

// --- generic command-hook helpers (Claude-style nested hooks/{event}/[groups]) ---

// wireCommandHook ensures obj["hooks"][event] contains a group with a
// command-hook whose "command" == command. Returns true if it added one.
func wireCommandHook(obj map[string]interface{}, event, command string) bool {
	hooks, _ := obj["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
	}
	groups, _ := hooks[event].([]interface{})
	if hasCommandInGroups(groups, command) {
		obj["hooks"] = hooks
		return false
	}
	group := map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{"type": "command", "command": command},
		},
	}
	hooks[event] = append(groups, group)
	obj["hooks"] = hooks
	return true
}

// hasCommandHook reports whether obj["hooks"][event] has a command-hook for command.
func hasCommandHook(obj map[string]interface{}, event, command string) bool {
	hooks, _ := obj["hooks"].(map[string]interface{})
	if hooks == nil {
		return false
	}
	groups, _ := hooks[event].([]interface{})
	return hasCommandInGroups(groups, command)
}

func hasCommandInGroups(groups []interface{}, command string) bool {
	for _, g := range groups {
		group, ok := g.(map[string]interface{})
		if !ok {
			continue
		}
		nested, _ := group["hooks"].([]interface{})
		for _, h := range nested {
			hm, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			if cmd, _ := hm["command"].(string); cmd == command {
				return true
			}
		}
	}
	return false
}

// unwireCommandHook removes command-hooks matching command from event's groups,
// dropping emptied groups and the event key if it becomes empty. Returns true
// if anything was removed.
func unwireCommandHook(obj map[string]interface{}, event, command string) bool {
	hooks, _ := obj["hooks"].(map[string]interface{})
	if hooks == nil {
		return false
	}
	groups, _ := hooks[event].([]interface{})
	if len(groups) == 0 {
		return false
	}
	removed := false
	cleaned := make([]interface{}, 0, len(groups))
	for _, g := range groups {
		group, ok := g.(map[string]interface{})
		if !ok {
			cleaned = append(cleaned, g)
			continue
		}
		nested, _ := group["hooks"].([]interface{})
		filtered := make([]interface{}, 0, len(nested))
		for _, h := range nested {
			if hm, ok := h.(map[string]interface{}); ok {
				if cmd, _ := hm["command"].(string); cmd == command {
					removed = true
					continue
				}
			}
			filtered = append(filtered, h)
		}
		if len(filtered) == 0 {
			continue // drop the emptied group
		}
		group["hooks"] = filtered
		cleaned = append(cleaned, group)
	}
	if !removed {
		return false
	}
	if len(cleaned) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = cleaned
	}
	return true
}
