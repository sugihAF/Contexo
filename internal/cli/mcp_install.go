package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/cli/agentwire"
	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/ui"
)

// mcpWireDeps holds the externally-facing dependencies of the wiring
// commands (the codex CLI runner and the codex-installed probe) so tests can
// inject stubs instead of touching the real environment.
type mcpWireDeps struct {
	runner         agentwire.Runner
	codexInstalled func() bool
}

func defaultMCPWireDeps() mcpWireDeps {
	return mcpWireDeps{
		runner:         agentwire.DefaultRunner,
		codexInstalled: agentwire.CodexInstalled,
	}
}

func newMCPInstallCmd() *cobra.Command {
	var (
		tool      string
		assumeYes bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Wire the Contexo MCP server into an agent's config (claude|cursor|codex|all)",
		Long: "Adds Contexo's MCP server so an agent's tools (ctx_push/pull/write_page,\n" +
			"history/diff, etc.) become available:\n\n" +
			"  claude  -> ./.mcp.json            (project-local)\n" +
			"  cursor  -> ./.cursor/mcp.json     (project-local)\n" +
			"  codex   -> ~/.codex/config.toml   (GLOBAL — prompts unless --yes)\n\n" +
			"--tool=all wires the project-local agents (claude, cursor) and also codex\n" +
			"when it is installed.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPInstall(cmd, GetRootDir(), tool, assumeYes, defaultMCPWireDeps())
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "all", "agent to wire: claude|cursor|codex|all")
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip confirmation prompts (e.g. Codex's global config)")
	return cmd
}

func newMCPUninstallCmd() *cobra.Command {
	var tool string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the Contexo MCP server from an agent's config (claude|cursor|codex|all)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPUninstall(cmd, GetRootDir(), tool, defaultMCPWireDeps())
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "all", "agent to unwire: claude|cursor|codex|all")
	return cmd
}

func newMCPStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show agent integrations for this project (MCP + capture hook)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPStatus(cmd, GetRootDir(), defaultMCPWireDeps())
		},
	}
}

func newMCPGuideCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guide",
		Short: "How to add Contexo to other agents/harnesses (Windsurf, OpenCode, Hermes, OpenClaw, ...)",
		RunE: func(cmd *cobra.Command, args []string) error {
			renderAgentGuide(cmd.OutOrStdout())
			return nil
		},
	}
}

// expandTools turns a --tool value into the concrete set, validating it.
func expandTools(tool string) ([]string, error) {
	switch tool {
	case "claude", "cursor", "codex":
		return []string{tool}, nil
	case "all", "":
		return []string{"claude", "cursor", "codex"}, nil
	default:
		return nil, fmt.Errorf("unknown --tool %q (want claude, cursor, codex, or all)", tool)
	}
}

func runMCPInstall(cmd *cobra.Command, root, tool string, assumeYes bool, deps mcpWireDeps) error {
	if _, err := os.Stat(config.ContexoDirPath(root)); err != nil {
		return fmt.Errorf("mcp install: not a Contexo project (run 'ctx init' first)")
	}
	tools, err := expandTools(tool)
	if err != nil {
		return err
	}
	explicitCodex := tool == "codex"
	out := cmd.OutOrStdout()
	for _, t := range tools {
		switch t {
		case "claude":
			if err := installJSONTarget(out, "Claude Code", agentwire.ClaudeMCPPath(root), ".mcp.json"); err != nil {
				return err
			}
		case "cursor":
			if err := installJSONTarget(out, "Cursor", agentwire.CursorMCPPath(root), ".cursor/mcp.json"); err != nil {
				return err
			}
		case "codex":
			if err := installCodexTarget(cmd, deps, assumeYes, explicitCodex); err != nil {
				return err
			}
		}
	}
	return nil
}

func installJSONTarget(out io.Writer, label, path, rel string) error {
	changed, err := agentwire.WireJSON(path)
	if err != nil {
		return fmt.Errorf("mcp install %s: %w", label, err)
	}
	u := ui.Plain(out)
	if changed {
		u.Success("Wired %s (%s)", label, rel)
	} else {
		noopLine(u, fmt.Sprintf("%s already wired (%s); nothing to do.", label, rel))
	}
	return nil
}

func installCodexTarget(cmd *cobra.Command, deps mcpWireDeps, assumeYes, explicit bool) error {
	u := humanMCPUI(cmd)
	if !deps.codexInstalled() {
		if explicit {
			return fmt.Errorf("mcp install codex: Codex CLI not found (`codex` not on PATH)")
		}
		return nil // under --tool=all, silently skip an agent that isn't installed
	}
	if !assumeYes && !promptYesNo(cmd, "Wire Contexo into Codex's GLOBAL config (~/.codex/config.toml)?") {
		noopLine(u, "Skipped Codex.")
		return nil
	}
	if err := agentwire.WireCodex(deps.runner); err != nil {
		return fmt.Errorf("mcp install codex: %w", err)
	}
	u.Success("Wired Codex (codex mcp add contexo)")
	return nil
}

func runMCPUninstall(cmd *cobra.Command, root, tool string, deps mcpWireDeps) error {
	tools, err := expandTools(tool)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, t := range tools {
		switch t {
		case "claude":
			uninstallJSONTarget(out, "Claude Code", agentwire.ClaudeMCPPath(root), ".mcp.json")
		case "cursor":
			uninstallJSONTarget(out, "Cursor", agentwire.CursorMCPPath(root), ".cursor/mcp.json")
		case "codex":
			if err := uninstallCodexTarget(out, deps); err != nil {
				return err
			}
		}
	}
	return nil
}

func uninstallJSONTarget(out io.Writer, label, path, rel string) {
	removed, deleted, err := agentwire.UnwireJSON(path)
	u := ui.Plain(out)
	switch {
	case err != nil:
		u.Warn("%s: %v", label, err)
	case deleted:
		u.Success("Removed %s and deleted %s", label, rel)
	case removed:
		u.Success("Removed %s entry from %s", label, rel)
	default:
		noopLine(u, fmt.Sprintf("%s not wired (%s); nothing to do.", label, rel))
	}
}

func uninstallCodexTarget(out io.Writer, deps mcpWireDeps) error {
	u := ui.Plain(out)
	if !deps.codexInstalled() {
		noopLine(u, "Codex not installed; nothing to do.")
		return nil
	}
	wired, _ := agentwire.CodexWired(deps.runner)
	if !wired {
		noopLine(u, "Codex not wired; nothing to do.")
		return nil
	}
	if err := agentwire.UnwireCodex(deps.runner); err != nil {
		return fmt.Errorf("mcp uninstall codex: %w", err)
	}
	u.Success("Removed Codex (codex mcp remove contexo)")
	return nil
}

// humanMCPUI builds the UI for the `ctx mcp` SUBcommands.
//
// ui.From is wrong here: it classifies anything under `ctx mcp` as machine
// output, because the bare `ctx mcp` is the JSON-RPC server that owns stdout.
// That rule is right for the server and wrong for install/uninstall/status,
// which are ordinary human commands -- routed through ui.From they would print
// nothing at all. Binding to the writer directly opts these back in.
func humanMCPUI(cmd *cobra.Command) *ui.UI { return ui.Plain(cmd.OutOrStdout()) }

func runMCPStatus(cmd *cobra.Command, root string, deps mcpWireDeps) error {
	out := cmd.OutOrStdout()
	u := humanMCPUI(cmd)
	renderIntegrationTable(out, root, deps)
	fmt.Fprintln(out)
	fmt.Fprintln(out, u.Style(ui.FG3, "Add another agent (Windsurf, OpenCode, Hermes, ...)?"))
	u.Hint("try", "ctx mcp guide")
	return nil
}

func promptYesNo(cmd *cobra.Command, question string) bool {
	in := cmd.InOrStdin()
	if in == nil {
		in = os.Stdin
	}
	ok, _ := confirm(in, cmd.OutOrStdout(), question)
	return ok
}
