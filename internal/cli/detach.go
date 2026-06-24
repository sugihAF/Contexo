package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/cli/agentwire"
	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/sync"
)

// codexRunner is the codex CLI runner used when detaching the global Codex
// MCP entry; overridable in tests. (detectCodex is shared with init.go.)
var codexRunner = agentwire.Runner(agentwire.DefaultRunner)

// newDetachCmd is the inverse of `ctx init`: it removes the wiring that
// init added (.mcp.json / .cursor/mcp.json entries, the global Codex entry
// when wired, .gitignore line, Stop hook) and, by default, deletes the
// .contexo/ knowledge directory itself.
//
// Default is aggressive (purges everything) because the most common use
// case is "I'm done evaluating, get this off my project." Users who only
// want to disconnect the agent integration pass --keep-knowledge.
func newDetachCmd() *cobra.Command {
	var (
		assumeYes     bool
		keepKnowledge bool
	)
	cmd := &cobra.Command{
		Use:   "detach",
		Short: "Reverse `ctx init` — remove Contexo wiring (and, by default, the .contexo/ directory)",
		Long: "Removes the Contexo MCP entries (.mcp.json, .cursor/mcp.json, and " +
			"Codex's global config when wired), the .gitignore line, and the Claude " +
			"Code Stop hook installed by `ctx init`. By default also deletes the " +
			".contexo/ knowledge directory; pass --keep-knowledge to preserve it " +
			"(useful when only the agent integration should go).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDetach(cmd, GetRootDir(), assumeYes, keepKnowledge)
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&keepKnowledge, "keep-knowledge", false, "keep the .contexo/ directory; only remove the wiring (MCP entries, .gitignore line, Stop hook)")
	return cmd
}

// detachPlan captures what runDetach intends to do, so we can print it
// to the user before any destructive action.
type detachPlan struct {
	removeContexoDir  bool
	removeMCPEntry    bool
	removeMCPFile     bool // true when .mcp.json had only the contexo entry
	removeCursorEntry bool
	removeCursorFile  bool // true when .cursor/mcp.json had only the contexo entry
	removeCodex       bool // true when codex is installed and has the contexo entry
	removeGitignoreLn bool
	removeStopHook    bool
	removeCodexHooks  bool // true when .codex/hooks.json has our capture hook
	removeCursorHooks bool // true when .cursor/hooks.json has our capture hook
	unpushedPageCount int
	contexoDirPath    string
	mcpPath           string
	cursorPath        string
	gitignorePath     string
	hookSettingsPath  string
}

func runDetach(cmd *cobra.Command, root string, assumeYes, keepKnowledge bool) error {
	plan, err := buildDetachPlan(root, keepKnowledge)
	if err != nil {
		return err
	}
	if !plan.anyAction() {
		fmt.Fprintln(cmd.OutOrStdout(), "Nothing to detach — no Contexo wiring or .contexo/ directory found here.")
		return nil
	}

	printDetachPlan(cmd, plan, keepKnowledge)

	if !assumeYes {
		ok, err := confirmDetach(cmd)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
			return nil
		}
	}

	return executeDetachPlan(cmd, root, plan)
}

func buildDetachPlan(root string, keepKnowledge bool) (detachPlan, error) {
	plan := detachPlan{
		contexoDirPath:   config.ContexoDirPath(root),
		mcpPath:          filepath.Join(root, ".mcp.json"),
		cursorPath:       agentwire.CursorMCPPath(root),
		gitignorePath:    filepath.Join(root, ".gitignore"),
		hookSettingsPath: filepath.Join(root, filepath.FromSlash(claudeSettingsRel)),
	}

	if _, err := os.Stat(plan.contexoDirPath); err == nil {
		plan.removeContexoDir = !keepKnowledge
		// Count unpushed pages so we can surface the risk before purging.
		// Failure here is non-fatal — better to detach than to block on a
		// page-store quirk.
		if store, err := pagestore.Open(plan.contexoDirPath); err == nil {
			pages, _ := store.List(pagestore.Filter{})
			state, _ := sync.LoadState(plan.contexoDirPath)
			for _, p := range pages {
				if state == nil || state.PageSHAs == nil {
					plan.unpushedPageCount = len(pages)
					break
				}
				if _, known := state.PageSHAs[p.Frontmatter.RelPath()]; !known {
					plan.unpushedPageCount++
				}
			}
		}
	}

	// Claude (./.mcp.json) and Cursor (./.cursor/mcp.json) share the same JSON
	// shape: drop the contexo entry, deleting the file when it held nothing else.
	plan.removeMCPEntry, plan.removeMCPFile = jsonMCPState(plan.mcpPath)
	plan.removeCursorEntry, plan.removeCursorFile = jsonMCPState(plan.cursorPath)

	// Codex's MCP server lives in its GLOBAL config; only remove it when codex
	// is installed and actually has our entry.
	if detectCodex() {
		if wired, _ := agentwire.CodexWired(codexRunner); wired {
			plan.removeCodex = true
		}
	}

	if data, err := os.ReadFile(plan.gitignorePath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(line)
			if t == ".contexo/" || t == ".contexo" {
				plan.removeGitignoreLn = true
				break
			}
		}
	}

	installed, _ := hookInstalled(root)
	plan.removeStopHook = installed

	codexHooks, _ := agentwire.CodexHooksWired(root)
	plan.removeCodexHooks = codexHooks

	cursorHooks, _ := agentwire.CursorHooksWired(root)
	plan.removeCursorHooks = cursorHooks

	return plan, nil
}

func (p detachPlan) anyAction() bool {
	return p.removeContexoDir || p.removeMCPEntry || p.removeMCPFile ||
		p.removeCursorEntry || p.removeCursorFile || p.removeCodex ||
		p.removeGitignoreLn || p.removeStopHook || p.removeCodexHooks || p.removeCursorHooks
}

func printDetachPlan(cmd *cobra.Command, plan detachPlan, keepKnowledge bool) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "This will:")
	if plan.removeMCPFile {
		fmt.Fprintf(out, "  - delete .mcp.json (only contained the contexo entry)\n")
	} else if plan.removeMCPEntry {
		fmt.Fprintf(out, "  - remove the \"contexo\" entry from .mcp.json (other servers left alone)\n")
	}
	if plan.removeCursorFile {
		fmt.Fprintf(out, "  - delete .cursor/mcp.json (only contained the contexo entry)\n")
	} else if plan.removeCursorEntry {
		fmt.Fprintf(out, "  - remove the \"contexo\" entry from .cursor/mcp.json (other servers left alone)\n")
	}
	if plan.removeCodex {
		fmt.Fprintf(out, "  - remove contexo from Codex's GLOBAL config (codex mcp remove contexo)\n")
	}
	if plan.removeGitignoreLn {
		fmt.Fprintf(out, "  - remove the .contexo/ line from .gitignore\n")
	}
	if plan.removeStopHook {
		fmt.Fprintf(out, "  - remove the Contexo Stop hook from %s\n", claudeSettingsRel)
	}
	if plan.removeCodexHooks {
		fmt.Fprintf(out, "  - remove the Contexo capture hooks from .codex/hooks.json\n")
	}
	if plan.removeCursorHooks {
		fmt.Fprintf(out, "  - remove the Contexo capture hooks from .cursor/hooks.json\n")
	}
	if plan.removeContexoDir {
		fmt.Fprintf(out, "  - DELETE the .contexo/ directory and everything in it\n")
		if plan.unpushedPageCount > 0 {
			fmt.Fprintf(out, "    ! WARNING: %d local page(s) have never been pushed to the server.\n", plan.unpushedPageCount)
			fmt.Fprintf(out, "    ! Run `ctx push` first, or re-run with --keep-knowledge to preserve them.\n")
		}
	} else if keepKnowledge {
		fmt.Fprintln(out, "  - (keeping .contexo/ as requested)")
	}
}

func confirmDetach(cmd *cobra.Command) (bool, error) {
	fmt.Fprint(cmd.OutOrStdout(), "Proceed? [y/N] ")
	in := cmd.InOrStdin()
	if in == nil {
		in = os.Stdin
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, nil // EOF or read error → treat as no
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func executeDetachPlan(cmd *cobra.Command, root string, plan detachPlan) error {
	out := cmd.OutOrStdout()
	var firstErr error
	noteErr := func(stage string, err error) {
		if err == nil {
			return
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "  warning: %s: %v\n", stage, err)
		if firstErr == nil {
			firstErr = err
		}
	}

	if plan.removeMCPFile {
		if err := os.Remove(plan.mcpPath); err == nil {
			fmt.Fprintln(out, "Removed .mcp.json")
		} else if !os.IsNotExist(err) {
			noteErr("remove .mcp.json", err)
		}
	} else if plan.removeMCPEntry {
		if err := removeMCPContexoEntry(plan.mcpPath); err != nil {
			noteErr("update .mcp.json", err)
		} else {
			fmt.Fprintln(out, "Removed contexo entry from .mcp.json")
		}
	}

	if plan.removeCursorEntry {
		if _, deleted, err := agentwire.UnwireJSON(plan.cursorPath); err != nil {
			noteErr("update .cursor/mcp.json", err)
		} else if deleted {
			fmt.Fprintln(out, "Removed .cursor/mcp.json")
		} else {
			fmt.Fprintln(out, "Removed contexo entry from .cursor/mcp.json")
		}
	}

	if plan.removeCodex {
		if err := agentwire.UnwireCodex(codexRunner); err != nil {
			noteErr("codex mcp remove", err)
		} else {
			fmt.Fprintln(out, "Removed contexo from Codex (~/.codex/config.toml)")
		}
	}

	if plan.removeGitignoreLn {
		if err := removeGitignoreContexoLine(plan.gitignorePath); err != nil {
			noteErr("update .gitignore", err)
		} else {
			fmt.Fprintln(out, "Removed .contexo/ from .gitignore")
		}
	}

	if plan.removeStopHook {
		if err := uninstallHook(cmd, root); err != nil {
			noteErr("uninstall Stop hook", err)
		}
	}

	if plan.removeCodexHooks {
		if _, _, err := agentwire.UnwireCodexHooks(root); err != nil {
			noteErr("remove Codex capture hooks", err)
		} else {
			fmt.Fprintln(out, "Removed Codex capture hooks from .codex/hooks.json")
		}
	}

	if plan.removeCursorHooks {
		if _, _, err := agentwire.UnwireCursorHooks(root); err != nil {
			noteErr("remove Cursor capture hooks", err)
		} else {
			fmt.Fprintln(out, "Removed Cursor capture hooks from .cursor/hooks.json")
		}
	}

	if plan.removeContexoDir {
		if err := os.RemoveAll(plan.contexoDirPath); err != nil {
			noteErr("remove .contexo/", err)
		} else {
			fmt.Fprintln(out, "Deleted .contexo/")
		}
	}

	if firstErr == nil {
		fmt.Fprintln(out, "Detach complete.")
	}
	return firstErr
}

// jsonMCPState inspects a JSON MCP config file (.mcp.json / .cursor/mcp.json)
// and reports whether it has the contexo entry and whether that entry is the
// file's only content (so the whole file can be deleted).
func jsonMCPState(path string) (hasEntry, onlyContent bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	var obj map[string]interface{}
	if json.Unmarshal(data, &obj) != nil {
		return false, false
	}
	servers, _ := obj["mcpServers"].(map[string]interface{})
	if _, ok := servers["contexo"]; !ok {
		return false, false
	}
	return true, len(servers) == 1 && len(obj) == 1
}

// removeMCPContexoEntry edits .mcp.json in place: drops mcpServers.contexo,
// preserves any other servers/keys the user added. Writes pretty-printed
// JSON (2-space indent) to match what init originally wrote.
func removeMCPContexoEntry(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	servers, _ := obj["mcpServers"].(map[string]interface{})
	delete(servers, "contexo")
	if len(servers) == 0 {
		delete(obj, "mcpServers")
	} else {
		obj["mcpServers"] = servers
	}
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o644)
}

// removeGitignoreContexoLine drops the `.contexo/` (or `.contexo`) line(s)
// and the immediately preceding Contexo header comment that init wrote.
// Leaves every other line — including blank lines elsewhere — untouched.
func removeGitignoreContexoLine(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	header := strings.TrimRight(gitignoreHeader, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == ".contexo/" || t == ".contexo" {
			// If the previous emitted line is our header comment, drop it
			// too so we don't leave a dangling header.
			if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) == strings.TrimSpace(header) {
				out = out[:n-1]
			}
			continue
		}
		out = append(out, lines[i])
	}
	// Trim trailing empty lines we may have introduced.
	for len(out) > 1 && strings.TrimSpace(out[len(out)-1]) == "" && strings.TrimSpace(out[len(out)-2]) == "" {
		out = out[:len(out)-1]
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644)
}
