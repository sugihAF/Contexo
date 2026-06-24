package agentwire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// countFlatCommandHook counts entries in obj.hooks[event] (a flat array of
// {command,...}) whose command == cmd — Cursor's hooks.json shape.
func countFlatCommandHook(obj map[string]interface{}, event, cmd string) int {
	hooks, _ := obj["hooks"].(map[string]interface{})
	entries, _ := hooks[event].([]interface{})
	n := 0
	for _, e := range entries {
		em, _ := e.(map[string]interface{})
		if c, _ := em["command"].(string); c == cmd {
			n++
		}
	}
	return n
}

func TestCursorHooksPath(t *testing.T) {
	if got, want := CursorHooksPath("proj"), filepath.Join("proj", ".cursor", "hooks.json"); got != want {
		t.Errorf("CursorHooksPath = %q, want %q", got, want)
	}
}

func TestWireCursorHooksCreatesBothEvents(t *testing.T) {
	root := t.TempDir()
	changed, err := WireCursorHooks(root)
	if err != nil {
		t.Fatalf("WireCursorHooks: %v", err)
	}
	if !changed {
		t.Errorf("expected changed=true on first wire")
	}
	obj := loadHooks(t, CursorHooksPath(root))
	if countFlatCommandHook(obj, "beforeSubmitPrompt", CaptureCommandCursor) != 1 {
		t.Errorf("beforeSubmitPrompt should have our command")
	}
	if countFlatCommandHook(obj, "afterAgentResponse", CaptureCommandCursor) != 1 {
		t.Errorf("afterAgentResponse should have our command")
	}
	if _, ok := obj["version"]; !ok {
		t.Errorf("Cursor hooks.json must include a version field")
	}
	if wired, _ := CursorHooksWired(root); !wired {
		t.Errorf("CursorHooksWired should be true after wire")
	}
}

func TestWireCursorHooksIdempotent(t *testing.T) {
	root := t.TempDir()
	if _, err := WireCursorHooks(root); err != nil {
		t.Fatal(err)
	}
	changed, err := WireCursorHooks(root)
	if err != nil {
		t.Fatalf("WireCursorHooks 2: %v", err)
	}
	if changed {
		t.Errorf("second wire should report changed=false")
	}
	obj := loadHooks(t, CursorHooksPath(root))
	if countFlatCommandHook(obj, "afterAgentResponse", CaptureCommandCursor) != 1 {
		t.Errorf("should still have exactly 1 command, got duplicates")
	}
}

func TestWireCursorHooksPreservesExisting(t *testing.T) {
	root := t.TempDir()
	path := CursorHooksPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	pre := map[string]interface{}{
		"version": 1,
		"hooks": map[string]interface{}{
			"afterFileEdit": []interface{}{
				map[string]interface{}{"command": "format.sh"},
			},
		},
	}
	data, _ := json.MarshalIndent(pre, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := WireCursorHooks(root); err != nil {
		t.Fatalf("WireCursorHooks: %v", err)
	}
	obj := loadHooks(t, path)
	if countFlatCommandHook(obj, "afterFileEdit", "format.sh") != 1 {
		t.Errorf("pre-existing afterFileEdit hook was lost")
	}
	if countFlatCommandHook(obj, "afterAgentResponse", CaptureCommandCursor) != 1 {
		t.Errorf("our afterAgentResponse hook not added alongside the existing one")
	}
}

func TestCursorHooksWiredFalseWhenAbsent(t *testing.T) {
	if wired, err := CursorHooksWired(t.TempDir()); err != nil || wired {
		t.Errorf("absent file: wired=%v err=%v, want false,nil", wired, err)
	}
}

func TestUnwireCursorHooksRemovesAndDeletesEmpty(t *testing.T) {
	root := t.TempDir()
	if _, err := WireCursorHooks(root); err != nil {
		t.Fatal(err)
	}
	removed, deleted, err := UnwireCursorHooks(root)
	if err != nil {
		t.Fatalf("UnwireCursorHooks: %v", err)
	}
	if !removed || !deleted {
		t.Errorf("expected removed+deleted when only our hooks (and version) remained; got r=%v d=%v", removed, deleted)
	}
	if _, err := os.Stat(CursorHooksPath(root)); !os.IsNotExist(err) {
		t.Errorf("hooks.json should be gone; stat err=%v", err)
	}
}

func TestUnwireCursorHooksPreservesOthers(t *testing.T) {
	root := t.TempDir()
	path := CursorHooksPath(root)
	os.MkdirAll(filepath.Dir(path), 0o755)
	pre := map[string]interface{}{
		"version": 1,
		"hooks": map[string]interface{}{
			"afterFileEdit": []interface{}{map[string]interface{}{"command": "format.sh"}},
		},
	}
	data, _ := json.MarshalIndent(pre, "", "  ")
	os.WriteFile(path, data, 0o644)

	if _, err := WireCursorHooks(root); err != nil {
		t.Fatal(err)
	}
	removed, deleted, err := UnwireCursorHooks(root)
	if err != nil {
		t.Fatalf("UnwireCursorHooks: %v", err)
	}
	if !removed || deleted {
		t.Errorf("should remove but keep the file (afterFileEdit remains); got r=%v d=%v", removed, deleted)
	}
	obj := loadHooks(t, path)
	if countFlatCommandHook(obj, "afterFileEdit", "format.sh") != 1 {
		t.Errorf("unrelated afterFileEdit hook was dropped")
	}
	if wired, _ := CursorHooksWired(root); wired {
		t.Errorf("CursorHooksWired should be false after uninstall")
	}
}

func TestUnwireCursorHooksMissingFile(t *testing.T) {
	removed, deleted, err := UnwireCursorHooks(t.TempDir())
	if err != nil || removed || deleted {
		t.Errorf("missing file: got (%v,%v,%v), want (false,false,nil)", removed, deleted, err)
	}
}
