// Package agentwire wires the Contexo MCP server (`ctx mcp`) into the
// configuration of the AI coding agents that support MCP — Claude Code,
// Cursor, and Codex. Each agent reads MCP servers from a different place:
//
//   - Claude Code: project-local ./.mcp.json            (JSON, mcpServers)
//   - Cursor:      project-local ./.cursor/mcp.json      (JSON, mcpServers)
//   - Codex:       global ~/.codex/config.toml           (TOML, via `codex mcp`)
//
// Claude and Cursor share the same JSON shape, so they go through the same
// merge-safe JSON helpers here. Codex's config is owned by the `codex` CLI,
// so we shell out to `codex mcp add/remove` (see codex.go) rather than
// editing its TOML by hand.
package agentwire

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ServerName is the MCP server key Contexo writes into agent configs.
const ServerName = "contexo"

// ClaudeMCPPath is where Claude Code reads project-level MCP servers.
func ClaudeMCPPath(root string) string { return filepath.Join(root, ".mcp.json") }

// CursorMCPPath is where Cursor reads project-level MCP servers.
func CursorMCPPath(root string) string { return filepath.Join(root, ".cursor", "mcp.json") }

// mcpEntry is the server definition Contexo writes: it launches `ctx mcp`.
// args is []interface{} so it round-trips equal through encoding/json.
func mcpEntry() map[string]interface{} {
	return map[string]interface{}{
		"command": "ctx",
		"args":    []interface{}{"mcp"},
	}
}

// WireJSON ensures the JSON file at path contains
// mcpServers.contexo = {"command":"ctx","args":["mcp"]}, creating the file
// (and any parent directories) when absent and preserving every other key.
// It returns changed=false when the entry was already present and identical.
func WireJSON(path string) (changed bool, err error) {
	obj, err := loadJSONObject(path)
	if err != nil {
		return false, err
	}
	servers, _ := obj["mcpServers"].(map[string]interface{})
	if servers == nil {
		servers = map[string]interface{}{}
	}
	if existing, ok := servers[ServerName].(map[string]interface{}); ok && jsonEqual(existing, mcpEntry()) {
		return false, nil
	}
	servers[ServerName] = mcpEntry()
	obj["mcpServers"] = servers
	if err := writeJSONObject(path, obj); err != nil {
		return false, err
	}
	return true, nil
}

// UnwireJSON removes mcpServers.contexo from the JSON file at path. When that
// leaves mcpServers empty and it was the only top-level key, the file is
// deleted. A missing file is a no-op. Returns (removedEntry, deletedFile, err).
func UnwireJSON(path string) (removedEntry bool, deletedFile bool, err error) {
	obj, err := loadJSONObject(path)
	if err != nil {
		return false, false, err
	}
	servers, _ := obj["mcpServers"].(map[string]interface{})
	if servers == nil {
		return false, false, nil
	}
	if _, ok := servers[ServerName]; !ok {
		return false, false, nil
	}
	delete(servers, ServerName)

	// If contexo was the only server AND mcpServers is the only top-level
	// key, the file is now an empty husk — delete it instead of leaving it.
	if len(servers) == 0 && len(obj) == 1 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return true, false, fmt.Errorf("agentwire: remove %s: %w", path, err)
		}
		return true, true, nil
	}
	if len(servers) == 0 {
		delete(obj, "mcpServers")
	} else {
		obj["mcpServers"] = servers
	}
	if err := writeJSONObject(path, obj); err != nil {
		return true, false, err
	}
	return true, false, nil
}

// WiredJSON reports whether the JSON file at path has the contexo MCP entry.
func WiredJSON(path string) (bool, error) {
	obj, err := loadJSONObject(path)
	if err != nil {
		return false, err
	}
	servers, _ := obj["mcpServers"].(map[string]interface{})
	if servers == nil {
		return false, nil
	}
	_, ok := servers[ServerName]
	return ok, nil
}

// loadJSONObject reads path into a generic map. A missing or empty file
// yields an empty map (not an error) so callers can treat "wire" uniformly.
func loadJSONObject(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]interface{}{}, nil
		}
		return nil, fmt.Errorf("agentwire: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]interface{}{}, nil
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("agentwire: parse %s: %w", path, err)
	}
	if obj == nil {
		obj = map[string]interface{}{}
	}
	return obj, nil
}

// writeJSONObject writes obj as 2-space-indented JSON, creating parent dirs.
func writeJSONObject(path string, obj map[string]interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("agentwire: mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("agentwire: marshal %s: %w", path, err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// jsonEqual compares two values by their canonical JSON encoding — robust to
// the map iteration order that reflect.DeepEqual is not bothered by anyway,
// but also to int-vs-float and similar decode quirks.
func jsonEqual(a, b interface{}) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ab) == string(bb)
}

// CursorInstalled reports whether Cursor appears to be installed for this
// user. Detected by the ~/.cursor home dir or a `cursor` CLI on PATH — wiring
// Cursor only writes a harmless project-local .cursor/mcp.json.
func CursorInstalled() bool {
	return detectAgent(".cursor", "cursor", os.UserHomeDir, exec.LookPath)
}

// CodexInstalled reports whether the Codex CLI is installed. Detected by the
// `codex` binary on PATH ONLY — wiring Codex shells out to `codex mcp add`, so
// a bare ~/.codex directory (which other tools also create) is not enough.
func CodexInstalled() bool {
	return detectPath("codex", exec.LookPath)
}

// detectAgent reports an agent as present when ~/<homeSubdir> exists or <bin>
// is on PATH. home and look are injected so the logic is testable. Used for
// Cursor, whose GUI doesn't always put a CLI on PATH and whose wiring only
// writes a harmless project-local file.
func detectAgent(homeSubdir, bin string, home func() (string, error), look func(string) (string, error)) bool {
	if h, err := home(); err == nil {
		if info, err := os.Stat(filepath.Join(h, homeSubdir)); err == nil && info.IsDir() {
			return true
		}
	}
	return detectPath(bin, look)
}

// detectPath reports whether <bin> is on PATH. Used for Codex, where wiring
// shells out to `codex mcp add` — so the binary must actually exist. A bare
// ~/.codex directory is NOT sufficient (other tools create ~/.codex too,
// which would otherwise cause a false "Codex detected").
func detectPath(bin string, look func(string) (string, error)) bool {
	_, err := look(bin)
	return err == nil
}
