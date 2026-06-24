package agentwire

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectAgentViaHomeSubdir(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	homeFn := func() (string, error) { return home, nil }
	noLook := func(string) (string, error) { return "", errors.New("not found") }
	if !detectAgent(".cursor", "cursor", homeFn, noLook) {
		t.Errorf("expected detected via ~/.cursor")
	}
	if detectAgent(".codex", "codex", homeFn, noLook) {
		t.Errorf("did not expect ~/.codex to exist")
	}
}

func TestDetectPathRequiresBinary(t *testing.T) {
	present := func(bin string) (string, error) { return "/usr/bin/" + bin, nil }
	absent := func(string) (string, error) { return "", errors.New("not found") }
	if !detectPath("codex", present) {
		t.Errorf("a binary on PATH should be detected")
	}
	// The bug we're guarding against: a ~/.codex dir must NOT count — only the
	// binary does, since Codex wiring shells out to `codex mcp add`.
	if detectPath("codex", absent) {
		t.Errorf("codex absent from PATH must not be detected")
	}
}

func TestDetectAgentViaPath(t *testing.T) {
	home := t.TempDir() // empty: no agent subdirs
	homeFn := func() (string, error) { return home, nil }
	look := func(bin string) (string, error) {
		if bin == "codex" {
			return "/usr/bin/codex", nil
		}
		return "", errors.New("not found")
	}
	if !detectAgent(".codex", "codex", homeFn, look) {
		t.Errorf("expected detected via PATH")
	}
	if detectAgent(".cursor", "cursor", homeFn, look) {
		t.Errorf("did not expect cursor on PATH")
	}
}

func readMCP(t *testing.T, path string) map[string]interface{} {
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

func contexoEntry(t *testing.T, obj map[string]interface{}) (map[string]interface{}, bool) {
	t.Helper()
	servers, ok := obj["mcpServers"].(map[string]interface{})
	if !ok {
		return nil, false
	}
	entry, ok := servers[ServerName].(map[string]interface{})
	return entry, ok
}

func TestPathHelpers(t *testing.T) {
	if got, want := ClaudeMCPPath("proj"), filepath.Join("proj", ".mcp.json"); got != want {
		t.Errorf("ClaudeMCPPath = %q, want %q", got, want)
	}
	if got, want := CursorMCPPath("proj"), filepath.Join("proj", ".cursor", "mcp.json"); got != want {
		t.Errorf("CursorMCPPath = %q, want %q", got, want)
	}
}

func TestWireJSONCreatesEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.json")
	changed, err := WireJSON(path)
	if err != nil {
		t.Fatalf("WireJSON: %v", err)
	}
	if !changed {
		t.Errorf("expected changed=true for a fresh file")
	}
	entry, ok := contexoEntry(t, readMCP(t, path))
	if !ok {
		t.Fatalf("contexo entry missing")
	}
	if entry["command"] != "ctx" {
		t.Errorf("command = %v, want ctx", entry["command"])
	}
	args, _ := entry["args"].([]interface{})
	if len(args) != 1 || args[0] != "mcp" {
		t.Errorf("args = %v, want [mcp]", args)
	}
}

func TestWireJSONCreatesNestedDir(t *testing.T) {
	// Mirrors Cursor's ./.cursor/mcp.json — parent dir doesn't exist yet.
	path := filepath.Join(t.TempDir(), ".cursor", "mcp.json")
	if _, err := WireJSON(path); err != nil {
		t.Fatalf("WireJSON: %v", err)
	}
	if _, ok := contexoEntry(t, readMCP(t, path)); !ok {
		t.Fatalf("contexo entry missing in nested path")
	}
}

func TestWireJSONIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.json")
	if _, err := WireJSON(path); err != nil {
		t.Fatalf("WireJSON 1: %v", err)
	}
	changed, err := WireJSON(path)
	if err != nil {
		t.Fatalf("WireJSON 2: %v", err)
	}
	if changed {
		t.Errorf("second WireJSON should report changed=false")
	}
}

func TestWireJSONPreservesOtherContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.json")
	pre := map[string]interface{}{
		"mcpServers":    map[string]interface{}{"other": map[string]interface{}{"command": "foo"}},
		"somethingElse": true,
	}
	data, _ := json.MarshalIndent(pre, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WireJSON(path); err != nil {
		t.Fatalf("WireJSON: %v", err)
	}
	obj := readMCP(t, path)
	servers := obj["mcpServers"].(map[string]interface{})
	if _, ok := servers["other"]; !ok {
		t.Errorf("existing 'other' server was dropped")
	}
	if _, ok := servers[ServerName]; !ok {
		t.Errorf("contexo not added")
	}
	if obj["somethingElse"] != true {
		t.Errorf("top-level key 'somethingElse' was dropped")
	}
}

func TestWiredJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.json")
	if wired, err := WiredJSON(path); err != nil || wired {
		t.Fatalf("absent file: wired=%v err=%v, want false,nil", wired, err)
	}
	if _, err := WireJSON(path); err != nil {
		t.Fatal(err)
	}
	if wired, err := WiredJSON(path); err != nil || !wired {
		t.Errorf("after wire: wired=%v err=%v, want true,nil", wired, err)
	}
}

func TestUnwireJSONRemovesEntryAndDeletesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.json")
	if _, err := WireJSON(path); err != nil {
		t.Fatal(err)
	}
	removed, deleted, err := UnwireJSON(path)
	if err != nil {
		t.Fatalf("UnwireJSON: %v", err)
	}
	if !removed {
		t.Errorf("expected removed=true")
	}
	if !deleted {
		t.Errorf("expected file deleted when only the contexo entry existed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be gone, stat err=%v", err)
	}
}

func TestUnwireJSONKeepsFileWithOtherServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.json")
	pre := map[string]interface{}{
		"mcpServers": map[string]interface{}{"other": map[string]interface{}{"command": "foo"}},
	}
	data, _ := json.MarshalIndent(pre, "", "  ")
	os.WriteFile(path, data, 0o644)
	if _, err := WireJSON(path); err != nil {
		t.Fatal(err)
	}
	removed, deleted, err := UnwireJSON(path)
	if err != nil {
		t.Fatalf("UnwireJSON: %v", err)
	}
	if !removed {
		t.Errorf("expected removed=true")
	}
	if deleted {
		t.Errorf("file should be kept because 'other' server remains")
	}
	servers := readMCP(t, path)["mcpServers"].(map[string]interface{})
	if _, ok := servers["other"]; !ok {
		t.Errorf("'other' server was dropped")
	}
	if _, ok := servers[ServerName]; ok {
		t.Errorf("contexo entry should be gone")
	}
}

func TestUnwireJSONMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.json")
	removed, deleted, err := UnwireJSON(path)
	if err != nil || removed || deleted {
		t.Errorf("missing file: got (%v,%v,%v), want (false,false,nil)", removed, deleted, err)
	}
}
