package agentwire

// VS Code (Copilot agent mode) reads workspace MCP servers from
// ./.vscode/mcp.json, whose schema differs from Claude's and Cursor's: the
// top-level key is "servers" (not "mcpServers") and each entry carries
// "type":"stdio". That is why VS Code does not go through WireJSON/UnwireJSON,
// which hard-code mcpServers.
//
// VS Code parses that file as JSONC (comments, trailing commas). Round-tripping
// such a file through encoding/json would fail or silently drop the user's
// comments, so any file that is not strict JSON is refused with
// ErrUnparseableVSCodeConfig and never rewritten; callers print
// VSCodeManualSnippet instead.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// vscodeServersKey is VS Code's top-level MCP key (Claude/Cursor: mcpServers).
const vscodeServersKey = "servers"

// ErrUnparseableVSCodeConfig marks an existing .vscode/mcp.json that Contexo
// will not rewrite: it is not strict JSON, or its "servers" is not an object.
var ErrUnparseableVSCodeConfig = errors.New("agentwire: .vscode/mcp.json is not strict JSON (comments or trailing commas?)")

// VSCodeMCPPath is where VS Code reads workspace-level MCP servers.
func VSCodeMCPPath(root string) string { return filepath.Join(root, ".vscode", "mcp.json") }

// vscodeEntry is the server definition Contexo writes for VS Code. VS Code
// does not promise the server's cwd is the workspace, so the root is passed
// explicitly via ${workspaceFolder}, which VS Code substitutes in args.
// args is []interface{} so it round-trips equal through encoding/json.
func vscodeEntry() map[string]interface{} {
	return map[string]interface{}{
		"type":    "stdio",
		"command": "ctx",
		"args":    []interface{}{"mcp", "--root", "${workspaceFolder}"},
	}
}

// loadVSCodeConfig is loadJSONObject plus the VS Code refusals: a JSON parse
// failure, or a "servers" value that is not an object, becomes
// ErrUnparseableVSCodeConfig. Read failures (e.g. permission denied) pass
// through unchanged so they are not mistaken for a JSONC file. A file holding
// only whitespace is treated like an empty one: there is nothing to protect.
func loadVSCodeConfig(path string) (map[string]interface{}, error) {
	obj, err := loadJSONObject(path)
	if err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.As(err, &syntaxErr):
			if data, rerr := os.ReadFile(path); rerr == nil && len(bytes.TrimSpace(data)) == 0 {
				return map[string]interface{}{}, nil
			}
			return nil, fmt.Errorf("%w: %s: %v", ErrUnparseableVSCodeConfig, path, syntaxErr)
		case errors.As(err, &typeErr):
			return nil, fmt.Errorf("%w: %s: %v", ErrUnparseableVSCodeConfig, path, typeErr)
		}
		return nil, err
	}
	if v, ok := obj[vscodeServersKey]; ok {
		if _, isObj := v.(map[string]interface{}); !isObj {
			return nil, fmt.Errorf("%w: %s: %q is not an object", ErrUnparseableVSCodeConfig, path, vscodeServersKey)
		}
	}
	return obj, nil
}

// WireVSCode ensures the VS Code config at path contains servers.contexo =
// vscodeEntry(), creating .vscode/ and the file when absent and preserving
// every other server and top-level key ("inputs", ...). It returns
// changed=false, without writing, when the entry is already identical.
func WireVSCode(path string) (changed bool, err error) {
	obj, err := loadVSCodeConfig(path)
	if err != nil {
		return false, err
	}
	servers, _ := obj[vscodeServersKey].(map[string]interface{})
	if servers == nil {
		servers = map[string]interface{}{}
	}
	if jsonEqual(servers[ServerName], vscodeEntry()) {
		return false, nil
	}
	servers[ServerName] = vscodeEntry()
	obj[vscodeServersKey] = servers
	if err := writeJSONObject(path, obj); err != nil {
		return false, err
	}
	return true, nil
}

// UnwireVSCode removes servers.contexo from the VS Code config at path. When
// that leaves servers empty and it was the only top-level key, the file is
// deleted (never .vscode/ itself, which holds the user's other settings). A
// missing file or absent entry is a no-op. Returns (removedEntry, deletedFile, err).
func UnwireVSCode(path string) (removedEntry, deletedFile bool, err error) {
	obj, err := loadVSCodeConfig(path)
	if err != nil {
		return false, false, err
	}
	servers, _ := obj[vscodeServersKey].(map[string]interface{})
	if _, ok := servers[ServerName]; !ok {
		return false, false, nil
	}
	delete(servers, ServerName)

	if len(servers) == 0 && len(obj) == 1 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return true, false, fmt.Errorf("agentwire: remove %s: %w", path, err)
		}
		return true, true, nil
	}
	if len(servers) == 0 {
		delete(obj, vscodeServersKey)
	}
	if err := writeJSONObject(path, obj); err != nil {
		return true, false, err
	}
	return true, false, nil
}

// WiredVSCode reports whether the VS Code config at path has the contexo entry.
func WiredVSCode(path string) (bool, error) {
	obj, err := loadVSCodeConfig(path)
	if err != nil {
		return false, err
	}
	servers, _ := obj[vscodeServersKey].(map[string]interface{})
	_, ok := servers[ServerName]
	return ok, nil
}

// VSCodeInstalled reports whether VS Code appears to be installed for this
// user: the ~/.vscode home dir or a `code` CLI on PATH. TERM_PROGRAM is
// deliberately ignored — Cursor's terminal reports "vscode" too.
func VSCodeInstalled() bool {
	return detectAgent(".vscode", "code", os.UserHomeDir, exec.LookPath)
}

// vscodeManualSnippet mirrors vscodeEntry for users whose mcp.json Contexo
// refuses to rewrite.
const vscodeManualSnippet = `{
  "servers": {
    "contexo": {
      "type": "stdio",
      "command": "ctx",
      "args": ["mcp", "--root", "${workspaceFolder}"]
    }
  }
}
`

// VSCodeManualSnippet is the .vscode/mcp.json block callers print when the
// existing file cannot be edited safely (see ErrUnparseableVSCodeConfig).
func VSCodeManualSnippet() string { return vscodeManualSnippet }
