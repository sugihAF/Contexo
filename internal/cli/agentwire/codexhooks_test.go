package agentwire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func loadHooks(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return obj
}

// countCommandHook counts command-hooks in obj.hooks[event] whose command == cmd.
func countCommandHook(obj map[string]interface{}, event, cmd string) int {
	hooks, _ := obj["hooks"].(map[string]interface{})
	groups, _ := hooks[event].([]interface{})
	n := 0
	for _, g := range groups {
		gm, _ := g.(map[string]interface{})
		nested, _ := gm["hooks"].([]interface{})
		for _, h := range nested {
			hm, _ := h.(map[string]interface{})
			if c, _ := hm["command"].(string); c == cmd {
				n++
			}
		}
	}
	return n
}

func TestCodexHooksPath(t *testing.T) {
	if got, want := CodexHooksPath("proj"), filepath.Join("proj", ".codex", "hooks.json"); got != want {
		t.Errorf("CodexHooksPath = %q, want %q", got, want)
	}
}

func TestWireCodexHooksCreatesBothEvents(t *testing.T) {
	root := t.TempDir()
	changed, err := WireCodexHooks(root)
	if err != nil {
		t.Fatalf("WireCodexHooks: %v", err)
	}
	if !changed {
		t.Errorf("expected changed=true on first wire")
	}
	obj := loadHooks(t, CodexHooksPath(root))
	if n := countCommandHook(obj, "Stop", CaptureCommandCodex); n != 1 {
		t.Errorf("Stop should have 1 capture command, got %d", n)
	}
	if n := countCommandHook(obj, "UserPromptSubmit", CaptureCommandCodex); n != 1 {
		t.Errorf("UserPromptSubmit should have 1 capture command, got %d", n)
	}
	if wired, _ := CodexHooksWired(root); !wired {
		t.Errorf("CodexHooksWired should be true after wire")
	}
}

func TestWireCodexHooksIdempotent(t *testing.T) {
	root := t.TempDir()
	if _, err := WireCodexHooks(root); err != nil {
		t.Fatal(err)
	}
	changed, err := WireCodexHooks(root)
	if err != nil {
		t.Fatalf("WireCodexHooks 2: %v", err)
	}
	if changed {
		t.Errorf("second wire should report changed=false")
	}
	obj := loadHooks(t, CodexHooksPath(root))
	if n := countCommandHook(obj, "Stop", CaptureCommandCodex); n != 1 {
		t.Errorf("Stop should still have exactly 1 capture command, got %d", n)
	}
}

func TestWireCodexHooksPreservesExisting(t *testing.T) {
	root := t.TempDir()
	path := CodexHooksPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	pre := map[string]interface{}{
		"hooks": map[string]interface{}{
			"PreToolUse": []interface{}{
				map[string]interface{}{"hooks": []interface{}{
					map[string]interface{}{"type": "command", "command": "echo other"},
				}},
			},
			"Stop": []interface{}{
				map[string]interface{}{"hooks": []interface{}{
					map[string]interface{}{"type": "command", "command": "echo existing-stop"},
				}},
			},
		},
	}
	data, _ := json.MarshalIndent(pre, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := WireCodexHooks(root); err != nil {
		t.Fatalf("WireCodexHooks: %v", err)
	}
	obj := loadHooks(t, path)
	if countCommandHook(obj, "PreToolUse", "echo other") != 1 {
		t.Errorf("pre-existing PreToolUse hook was lost")
	}
	if countCommandHook(obj, "Stop", "echo existing-stop") != 1 {
		t.Errorf("pre-existing Stop hook was lost")
	}
	if countCommandHook(obj, "Stop", CaptureCommandCodex) != 1 {
		t.Errorf("our Stop capture command not added alongside the existing one")
	}
}

func TestCodexHooksWiredFalseWhenAbsent(t *testing.T) {
	root := t.TempDir()
	if wired, err := CodexHooksWired(root); err != nil || wired {
		t.Errorf("absent file: wired=%v err=%v, want false,nil", wired, err)
	}
}

func TestUnwireCodexHooksRemovesOursAndDeletesEmptyFile(t *testing.T) {
	root := t.TempDir()
	if _, err := WireCodexHooks(root); err != nil {
		t.Fatal(err)
	}
	removed, deleted, err := UnwireCodexHooks(root)
	if err != nil {
		t.Fatalf("UnwireCodexHooks: %v", err)
	}
	if !removed {
		t.Errorf("expected removed=true")
	}
	if !deleted {
		t.Errorf("expected the file deleted when only our hooks remained")
	}
	if _, err := os.Stat(CodexHooksPath(root)); !os.IsNotExist(err) {
		t.Errorf("hooks.json should be gone; stat err=%v", err)
	}
}

func TestUnwireCodexHooksPreservesOthers(t *testing.T) {
	root := t.TempDir()
	path := CodexHooksPath(root)
	os.MkdirAll(filepath.Dir(path), 0o755)
	pre := map[string]interface{}{
		"hooks": map[string]interface{}{
			"PreToolUse": []interface{}{
				map[string]interface{}{"hooks": []interface{}{
					map[string]interface{}{"type": "command", "command": "echo other"},
				}},
			},
		},
	}
	data, _ := json.MarshalIndent(pre, "", "  ")
	os.WriteFile(path, data, 0o644)

	if _, err := WireCodexHooks(root); err != nil {
		t.Fatal(err)
	}
	removed, deleted, err := UnwireCodexHooks(root)
	if err != nil {
		t.Fatalf("UnwireCodexHooks: %v", err)
	}
	if !removed {
		t.Errorf("expected removed=true")
	}
	if deleted {
		t.Errorf("file should be kept because PreToolUse remains")
	}
	obj := loadHooks(t, path)
	if countCommandHook(obj, "PreToolUse", "echo other") != 1 {
		t.Errorf("unrelated PreToolUse hook was dropped")
	}
	if countCommandHook(obj, "Stop", CaptureCommandCodex) != 0 {
		t.Errorf("our Stop capture command should be gone")
	}
	if wired, _ := CodexHooksWired(root); wired {
		t.Errorf("CodexHooksWired should be false after uninstall")
	}
}

func TestUnwireCodexHooksMissingFile(t *testing.T) {
	root := t.TempDir()
	removed, deleted, err := UnwireCodexHooks(root)
	if err != nil || removed || deleted {
		t.Errorf("missing file: got (%v,%v,%v), want (false,false,nil)", removed, deleted, err)
	}
}
