package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
)

// NewInitCmd creates the ctx init command.
func NewInitCmd() *cobra.Command {
	var (
		skipMCP       bool
		skipGitignore bool
		skipHooks     bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize .contexo knowledge directory",
		Long: "Creates a .contexo/ tree in the current project for storing AI " +
			"knowledge pages. Also writes .mcp.json so your agent picks up Contexo's " +
			"MCP server, adds .contexo/ to .gitignore so the knowledge isn't " +
			"committed to your project's git history, and registers a Claude Code " +
			"Stop hook so per-turn capture starts working in the next agent session. " +
			"Idempotent — re-running leaves existing entries alone.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, skipMCP, skipGitignore, skipHooks)
		},
	}
	cmd.Flags().BoolVar(&skipMCP, "no-mcp", false, "skip creating .mcp.json")
	cmd.Flags().BoolVar(&skipGitignore, "no-gitignore", false, "skip adding .contexo/ to .gitignore")
	cmd.Flags().BoolVar(&skipHooks, "no-hooks", false, "skip registering the Claude Code Stop hook for per-turn capture")
	return cmd
}

func runInit(cmd *cobra.Command, skipMCP, skipGitignore, skipHooks bool) error {
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
		if err := writeMCPConfig(cmd, root); err != nil {
			return err
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
			fmt.Fprintf(cmd.OutOrStdout(), "  Warning: could not install Stop hook: %v (run 'ctx hooks install' later to retry)\n", err)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "  (Hook is Claude Code-specific; other agents need separate wiring.)")
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Initialized .contexo in %s\n", root)
	return nil
}

func writeIfAbsent(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// writeMCPConfig writes .mcp.json at the project root if absent, so AI agents
// (Claude Code, Cursor, etc.) pick up the local Contexo MCP server on next
// session start.
func writeMCPConfig(cmd *cobra.Command, root string) error {
	mcpPath := filepath.Join(root, ".mcp.json")
	if _, err := os.Stat(mcpPath); err == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "  .mcp.json already exists; leaving it alone (add a contexo entry manually if missing)")
		return nil
	}
	if err := os.WriteFile(mcpPath, []byte(mcpConfigContent), 0o644); err != nil {
		return fmt.Errorf("init: write .mcp.json: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "  Created .mcp.json (restart your AI agent to load the contexo server)")
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

const mcpConfigContent = `{
  "mcpServers": {
    "contexo": { "command": "ctx", "args": ["mcp"] }
  }
}
`

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
