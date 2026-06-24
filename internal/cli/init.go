package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/cli/agentwire"
	"github.com/sugihAF/contexo/internal/config"
)

// NewInitCmd creates the ctx init command.
func NewInitCmd() *cobra.Command {
	var (
		skipMCP       bool
		skipCursor    bool
		skipGitignore bool
		skipHooks     bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize .contexo knowledge directory",
		Long: "Creates a .contexo/ tree in the current project for storing AI " +
			"knowledge pages. Wires Contexo's MCP server into Claude Code (.mcp.json) " +
			"and, when Cursor is detected, into Cursor (.cursor/mcp.json); prints how " +
			"to wire Codex when it's detected. Adds .contexo/ to .gitignore so the " +
			"knowledge isn't committed to your project's git history, and registers a " +
			"Claude Code Stop hook so per-turn capture starts working in the next " +
			"agent session. Idempotent — re-running leaves existing entries alone.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, skipMCP, skipCursor, skipGitignore, skipHooks)
		},
	}
	cmd.Flags().BoolVar(&skipMCP, "no-mcp", false, "skip wiring the MCP server into agent configs (.mcp.json, .cursor/mcp.json)")
	cmd.Flags().BoolVar(&skipCursor, "no-cursor", false, "skip auto-wiring Cursor (.cursor/mcp.json) even when Cursor is detected")
	cmd.Flags().BoolVar(&skipGitignore, "no-gitignore", false, "skip adding .contexo/ to .gitignore")
	cmd.Flags().BoolVar(&skipHooks, "no-hooks", false, "skip registering the Claude Code Stop hook for per-turn capture")
	return cmd
}

func runInit(cmd *cobra.Command, skipMCP, skipCursor, skipGitignore, skipHooks bool) error {
	root := GetRootDir()
	hubDir := config.ContexoDirPath(root)

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

	if !skipMCP {
		if err := wireClaudeMCP(cmd, root); err != nil {
			return err
		}
		if !skipCursor && detectCursor() {
			if err := wireCursorMCP(cmd, root); err != nil {
				return err
			}
		}
		if detectCodex() {
			fmt.Fprintln(cmd.OutOrStdout(), "  Codex detected — run `ctx mcp install --tool=codex` to wire it (writes the GLOBAL ~/.codex/config.toml).")
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
		if err := installHook(cmd, root); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "  Warning: could not install Claude Stop hook: %v (run 'ctx hooks install' later)\n", err)
		}
		// Codex capture hooks are project-local and safe to auto-install when
		// Codex is present (same logic as auto-wiring Cursor's MCP).
		if detectCodex() {
			if err := installCodexCaptureHooks(cmd, root); err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "  Warning: could not install Codex capture hooks: %v\n", err)
			}
		}
		if detectCursor() {
			if err := installCursorCaptureHooks(cmd, root); err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "  Warning: could not install Cursor capture hooks: %v\n", err)
			}
		}
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Initialized .contexo in %s\n", root)
	fmt.Fprintln(out)
	renderIntegrationTable(out, root, defaultMCPWireDeps())
	fmt.Fprintln(out)
	renderAgentGuide(out)
	return nil
}

func writeIfAbsent(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// detectCursor / detectCodex are indirections over agentwire's probes so
// tests can simulate an environment with or without those agents installed.
var (
	detectCursor = agentwire.CursorInstalled
	detectCodex  = agentwire.CodexInstalled
)

// wireClaudeMCP adds the contexo MCP server to the project's ./.mcp.json
// (Claude Code's project-level config), merging into any existing file.
func wireClaudeMCP(cmd *cobra.Command, root string) error {
	changed, err := agentwire.WireJSON(agentwire.ClaudeMCPPath(root))
	if err != nil {
		return fmt.Errorf("init: write .mcp.json: %w", err)
	}
	if changed {
		fmt.Fprintln(cmd.OutOrStdout(), "  Wired Claude Code (.mcp.json) — restart your agent to load the contexo server")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "  .mcp.json already has the contexo entry; leaving it alone")
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
	if changed {
		fmt.Fprintln(cmd.OutOrStdout(), "  Cursor detected — wired .cursor/mcp.json")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "  .cursor/mcp.json already has the contexo entry; leaving it alone")
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
		fmt.Fprintln(cmd.OutOrStdout(), "  Created .gitignore with .contexo/")
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
	fmt.Fprintln(cmd.OutOrStdout(), "  Added .contexo/ to .gitignore")
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
