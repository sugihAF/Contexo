package agentwire

import (
	"fmt"
	"os/exec"
)

// Runner executes the `codex` CLI with the given args and returns its
// combined output. It's a seam so tests can stub the codex binary.
type Runner func(args ...string) (string, error)

// DefaultRunner runs the real `codex` binary found on PATH.
func DefaultRunner(args ...string) (string, error) {
	out, err := exec.Command("codex", args...).CombinedOutput()
	return string(out), err
}

// WireCodex registers Contexo as an MCP server in Codex's global config by
// invoking `codex mcp add contexo -- ctx mcp`. Codex owns its TOML format,
// so we never parse it ourselves.
func WireCodex(run Runner) error {
	if _, err := run("mcp", "add", ServerName, "--", "ctx", "mcp"); err != nil {
		return fmt.Errorf("agentwire: codex mcp add: %w", err)
	}
	return nil
}

// UnwireCodex removes the Contexo MCP server via `codex mcp remove contexo`.
func UnwireCodex(run Runner) error {
	if _, err := run("mcp", "remove", ServerName); err != nil {
		return fmt.Errorf("agentwire: codex mcp remove: %w", err)
	}
	return nil
}

// CodexWired reports whether Codex already has the Contexo MCP server, using
// `codex mcp get contexo` — a non-nil error is treated as "not wired" rather
// than surfaced, since that's exactly what `get` returns for an absent server.
func CodexWired(run Runner) (bool, error) {
	if _, err := run("mcp", "get", ServerName); err != nil {
		return false, nil
	}
	return true, nil
}
