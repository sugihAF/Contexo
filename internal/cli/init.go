package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/cli/agentwire"
	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/ui"
)

// NewInitCmd creates the ctx init command.
func NewInitCmd() *cobra.Command {
	var (
		skipMCP       bool
		skipCursor    bool
		skipVSCode    bool
		skipGitignore bool
		skipHooks     bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize .contexo knowledge directory",
		Long: "Creates a .contexo/ tree in the current project for storing AI " +
			"knowledge pages. Wires Contexo's MCP server into Claude Code (.mcp.json) " +
			"and, when they are detected, into Cursor (.cursor/mcp.json) and VS Code " +
			"(.vscode/mcp.json); prints how to wire Codex when it's detected. Adds " +
			".contexo/ to .gitignore so the " +
			"knowledge isn't committed to your project's git history, and registers a " +
			"Claude Code Stop hook so per-turn capture starts working in the next " +
			"agent session. Idempotent — re-running leaves existing entries alone.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, skipMCP, skipCursor, skipVSCode, skipGitignore, skipHooks)
		},
	}
	cmd.Flags().BoolVar(&skipMCP, "no-mcp", false, "skip wiring the MCP server into agent configs (.mcp.json, .cursor/mcp.json, .vscode/mcp.json)")
	cmd.Flags().BoolVar(&skipCursor, "no-cursor", false, "skip auto-wiring Cursor (.cursor/mcp.json) even when Cursor is detected")
	cmd.Flags().BoolVar(&skipVSCode, "no-vscode", false, "skip auto-wiring VS Code (.vscode/mcp.json) even when VS Code is detected")
	cmd.Flags().BoolVar(&skipGitignore, "no-gitignore", false, "skip adding .contexo/ to .gitignore")
	cmd.Flags().BoolVar(&skipHooks, "no-hooks", false, "skip registering the Claude Code Stop hook for per-turn capture")
	return cmd
}

func runInit(cmd *cobra.Command, skipMCP, skipCursor, skipVSCode, skipGitignore, skipHooks bool) error {
	root := GetRootDir()
	hubDir := config.ContexoDirPath(root)
	u := ui.From(cmd)

	dirs := []string{
		hubDir,
		filepath.Join(hubDir, "wiki", "concepts"),
		filepath.Join(hubDir, "wiki", "entities"),
		filepath.Join(hubDir, "wiki", "analyses"),
		filepath.Join(hubDir, "raw", "sessions"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("init: create %s: %w", d, err)
		}
	}

	cfgPath := config.ContexoConfigPath(root)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := config.Save(root, config.DefaultConfig()); err != nil {
			return fmt.Errorf("init: save config: %w", err)
		}
	}

	if err := writeIfAbsent(filepath.Join(hubDir, "index.md"), seedIndex); err != nil {
		return err
	}
	if err := writeIfAbsent(filepath.Join(hubDir, "tags.md"), seedTags); err != nil {
		return err
	}

	// One eyebrow over the whole wiring run, so the per-step lines below read
	// as a section rather than as loose noise. Skipped entirely when every
	// step is turned off and there would be nothing under the label.
	if !skipMCP || !skipGitignore || !skipHooks {
		u.Eyebrow("SETUP")
	}

	if !skipMCP {
		if err := wireClaudeMCP(cmd, root); err != nil {
			return err
		}
		if !skipCursor && detectCursor() {
			if err := wireCursorMCP(cmd, root); err != nil {
				return err
			}
		}
		if !skipVSCode && detectVSCode() {
			if err := wireVSCodeMCP(cmd, root); err != nil {
				return err
			}
		}
		if detectCodex() {
			u.Step("Codex detected — run `ctx mcp install --tool=codex` to wire it (writes the GLOBAL ~/.codex/config.toml).")
		}
	}
	if !skipGitignore {
		if err := updateGitignore(cmd, root); err != nil {
			return err
		}
	}
	if !skipHooks {
		// Failing here would be more annoying than helpful — the hook is a
		// nice-to-have, not load-bearing for init's core job. Warn and move on.
		if err := installHook(cmd, root, true); err != nil {
			u.Warn("could not install Claude Stop hook: %v (run 'ctx hooks install' later)", err)
		}
		// Codex capture hooks are project-local and safe to auto-install when
		// Codex is present (same logic as auto-wiring Cursor's MCP).
		if detectCodex() {
			if err := installCodexCaptureHooks(cmd, root, true); err != nil {
				u.Warn("could not install Codex capture hooks: %v", err)
			}
		}
		if detectCursor() {
			if err := installCursorCaptureHooks(cmd, root, true); err != nil {
				u.Warn("could not install Cursor capture hooks: %v", err)
			}
		}
	}

	out := cmd.OutOrStdout()
	fmt.Fprintln(out)
	u.Success("Initialized .contexo in %s", root)
	fmt.Fprintln(out)
	renderIntegrationTable(out, root, defaultMCPWireDeps())
	fmt.Fprintln(out)
	renderAgentGuide(out)
	return nil
}

// stepLine renders one line of a per-agent wiring log (init's SETUP section,

func writeIfAbsent(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// detectCursor / detectCodex / detectVSCode are indirections over agentwire's
// probes so tests can simulate an environment with or without those agents
// installed.
var (
	detectCursor = agentwire.CursorInstalled
	detectCodex  = agentwire.CodexInstalled
	detectVSCode = agentwire.VSCodeInstalled
)

// wireClaudeMCP adds the contexo MCP server to the project's ./.mcp.json
// (Claude Code's project-level config), merging into any existing file.
func wireClaudeMCP(cmd *cobra.Command, root string) error {
	changed, err := agentwire.WireJSON(agentwire.ClaudeMCPPath(root))
	if err != nil {
		return fmt.Errorf("init: write .mcp.json: %w", err)
	}
	u := ui.From(cmd)
	if changed {
		u.Step("Wired Claude Code (.mcp.json) — restart your agent to load the contexo server")
	} else {
		u.Step(".mcp.json already has the contexo entry; leaving it alone")
	}
	return nil
}

// wireCursorMCP adds the contexo MCP server to ./.cursor/mcp.json (Cursor's
// project-level config). Called only when Cursor is detected.
func wireCursorMCP(cmd *cobra.Command, root string) error {
	changed, err := agentwire.WireJSON(agentwire.CursorMCPPath(root))
	if err != nil {
		return fmt.Errorf("init: write .cursor/mcp.json: %w", err)
	}
	u := ui.From(cmd)
	if changed {
		u.Step("Cursor detected — wired .cursor/mcp.json")
	} else {
		u.Step(".cursor/mcp.json already has the contexo entry; leaving it alone")
	}
	return nil
}

// wireVSCodeMCP adds the contexo MCP server to ./.vscode/mcp.json (VS Code's
// workspace config). Called only when VS Code is detected. A JSONC file is
// left untouched: init prints the entry to add by hand and carries on.
func wireVSCodeMCP(cmd *cobra.Command, root string) error {
	u := ui.From(cmd)
	changed, err := agentwire.WireVSCode(agentwire.VSCodeMCPPath(root))
	switch {
	case errors.Is(err, agentwire.ErrUnparseableVSCodeConfig):
		warnVSCodeManual(u)
	case err != nil:
		return fmt.Errorf("init: write .vscode/mcp.json: %w", err)
	case changed:
		u.Step("VS Code detected — wired .vscode/mcp.json")
		u.Step(".vscode/ is usually committed: teammates need ctx on PATH")
	default:
		u.Step(".vscode/mcp.json already has the contexo entry; leaving it alone")
	}
	return nil
}

// updateGitignore adds .contexo/ to the project's .gitignore so the local
// knowledge isn't committed to the project's git history (knowledge syncs
// via ctx push/pull instead). Skips silently if the project isn't a git repo.
func updateGitignore(cmd *cobra.Command, root string) error {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return nil
	}

	giPath := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(giPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("init: read .gitignore: %w", err)
		}
		content := gitignoreHeader + ".contexo/\n"
		if err := os.WriteFile(giPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("init: write .gitignore: %w", err)
		}
		ui.From(cmd).Step("Created .gitignore with .contexo/")
		return nil
	}

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == ".contexo/" || trimmed == ".contexo" {
			return nil
		}
	}

	addition := gitignoreHeader + ".contexo/\n"
	if !strings.HasSuffix(string(data), "\n") {
		addition = "\n" + addition
	} else {
		addition = "\n" + addition
	}
	f, err := os.OpenFile(giPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("init: open .gitignore: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(addition); err != nil {
		return fmt.Errorf("init: append .gitignore: %w", err)
	}
	ui.From(cmd).Step("Added .contexo/ to .gitignore")
	return nil
}

const gitignoreHeader = "# Contexo local knowledge (synced via ctx push/pull, not git)\n"

const seedIndex = `# Knowledge Index

Always-loaded index for this project's Contexo knowledge base. Find what's
relevant here, then read individual pages on demand.

## Concepts
(none yet)

## Entities
(none yet)

## Analyses
(none yet)

## Sources
(none yet)
`

const seedTags = `# Tags

(none yet)
`
