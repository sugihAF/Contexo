package agentwire

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CaptureCommandCursor is the shell command Contexo installs into Cursor's
// beforeSubmitPrompt and afterAgentResponse hooks. Cursor runs it with the
// hook payload on stdin; `ctx capture turn` dispatches on hook_event_name.
const CaptureCommandCursor = "ctx capture turn --agent cursor"

// cursorCaptureEvents: beforeSubmitPrompt stashes the prompt, afterAgentResponse
// pairs it with the inline assistant `text`.
var cursorCaptureEvents = []string{"beforeSubmitPrompt", "afterAgentResponse"}

// CursorHooksPath is Cursor's project-local hooks config.
func CursorHooksPath(root string) string { return filepath.Join(root, ".cursor", "hooks.json") }

// WireCursorHooks adds the Contexo capture command to Cursor's beforeSubmitPrompt
// and afterAgentResponse hooks in <root>/.cursor/hooks.json (flat schema:
// {"version":1,"hooks":{"<event>":[{"command":"..."}]}}), creating/merging the
// file and preserving any other hooks. Returns changed=false when both were
// already present.
func WireCursorHooks(root string) (changed bool, err error) {
	path := CursorHooksPath(root)
	obj, err := loadJSONObject(path)
	if err != nil {
		return false, err
	}
	added := false
	for _, ev := range cursorCaptureEvents {
		if wireFlatCommandHook(obj, ev, CaptureCommandCursor) {
			added = true
		}
	}
	if !added {
		return false, nil
	}
	if _, ok := obj["version"]; !ok {
		obj["version"] = 1 // Cursor's hooks.json requires a version
	}
	if err := writeJSONObject(path, obj); err != nil {
		return false, err
	}
	return true, nil
}

// UnwireCursorHooks removes the Contexo capture command from Cursor's hooks,
// deleting the file if nothing else of substance remains.
func UnwireCursorHooks(root string) (removed bool, deletedFile bool, err error) {
	path := CursorHooksPath(root)
	obj, err := loadJSONObject(path)
	if err != nil {
		return false, false, err
	}
	for _, ev := range cursorCaptureEvents {
		if unwireFlatCommandHook(obj, ev, CaptureCommandCursor) {
			removed = true
		}
	}
	if !removed {
		return false, false, nil
	}

	if hooks, _ := obj["hooks"].(map[string]interface{}); len(hooks) == 0 {
		delete(obj, "hooks")
	}
	// If only our husk remains (empty, or just the "version" we added), delete it.
	_, hasVersion := obj["version"]
	if len(obj) == 0 || (len(obj) == 1 && hasVersion) {
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

// CursorHooksWired reports whether Cursor's afterAgentResponse hook has the
// Contexo capture command.
func CursorHooksWired(root string) (bool, error) {
	obj, err := loadJSONObject(CursorHooksPath(root))
	if err != nil {
		return false, err
	}
	return hasFlatCommandHook(obj, "afterAgentResponse", CaptureCommandCursor), nil
}

// --- generic FLAT command-hook helpers (Cursor's hooks/{event}/[{command}]) ---

// wireFlatCommandHook ensures obj["hooks"][event] (a flat array of {command,...})
// contains an entry whose command == command. Returns true if it added one.
func wireFlatCommandHook(obj map[string]interface{}, event, command string) bool {
	hooks, _ := obj["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
	}
	entries, _ := hooks[event].([]interface{})
	if hasFlatEntry(entries, command) {
		obj["hooks"] = hooks
		return false
	}
	hooks[event] = append(entries, map[string]interface{}{"command": command})
	obj["hooks"] = hooks
	return true
}

func hasFlatCommandHook(obj map[string]interface{}, event, command string) bool {
	hooks, _ := obj["hooks"].(map[string]interface{})
	if hooks == nil {
		return false
	}
	entries, _ := hooks[event].([]interface{})
	return hasFlatEntry(entries, command)
}

func hasFlatEntry(entries []interface{}, command string) bool {
	for _, e := range entries {
		em, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if c, _ := em["command"].(string); c == command {
			return true
		}
	}
	return false
}

// unwireFlatCommandHook removes entries with command==command from event's array,
// dropping the event key if it becomes empty. Returns true if anything was removed.
func unwireFlatCommandHook(obj map[string]interface{}, event, command string) bool {
	hooks, _ := obj["hooks"].(map[string]interface{})
	if hooks == nil {
		return false
	}
	entries, _ := hooks[event].([]interface{})
	if len(entries) == 0 {
		return false
	}
	removed := false
	filtered := make([]interface{}, 0, len(entries))
	for _, e := range entries {
		if em, ok := e.(map[string]interface{}); ok {
			if c, _ := em["command"].(string); c == command {
				removed = true
				continue
			}
		}
		filtered = append(filtered, e)
	}
	if !removed {
		return false
	}
	if len(filtered) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = filtered
	}
	return true
}
