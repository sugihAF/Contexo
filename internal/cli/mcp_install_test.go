package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/cli/agentwire"
)

func bufCmd() (*cobra.Command, *bytes.Buffer) {
	c := &cobra.Command{}
	out := &bytes.Buffer{}
	c.SetOut(out)
	c.SetErr(&bytes.Buffer{})
	return c, out
}

// testDeps returns wiring deps with a stub codex runner that records argv.
func testDeps(codexInstalled bool, calls *[][]string) mcpWireDeps {
	return mcpWireDeps{
		runner: func(args ...string) (string, error) {
			if calls != nil {
				*calls = append(*calls, args)
			}
			return "", nil
		},
		codexInstalled: func() bool { return codexInstalled },
	}
}

func TestMCPInstallClaudeWritesConfig(t *testing.T) {
	project := tmpContexoProject(t)
	cmd, _ := bufCmd()
	if err := runMCPInstall(cmd, project, "claude", true, testDeps(false, nil)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if wired, _ := agentwire.WiredJSON(agentwire.ClaudeMCPPath(project)); !wired {
		t.Errorf(".mcp.json should contain the contexo entry")
	}
}

func TestMCPInstallCursorWritesConfig(t *testing.T) {
	project := tmpContexoProject(t)
	cmd, _ := bufCmd()
	if err := runMCPInstall(cmd, project, "cursor", true, testDeps(false, nil)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if wired, _ := agentwire.WiredJSON(agentwire.CursorMCPPath(project)); !wired {
		t.Errorf(".cursor/mcp.json should contain the contexo entry")
	}
}

func TestMCPInstallCodexInvokesRunner(t *testing.T) {
	project := tmpContexoProject(t)
	var calls [][]string
	cmd, _ := bufCmd()
	if err := runMCPInstall(cmd, project, "codex", true, testDeps(true, &calls)); err != nil {
		t.Fatalf("install codex: %v", err)
	}
	if len(calls) == 0 || calls[0][0] != "mcp" || calls[0][1] != "add" {
		t.Errorf("expected `codex mcp add` invocation, got %v", calls)
	}
}

func TestMCPInstallRequiresProject(t *testing.T) {
	notProject := t.TempDir()
	cmd, _ := bufCmd()
	if err := runMCPInstall(cmd, notProject, "claude", true, testDeps(false, nil)); err == nil {
		t.Errorf("expected error when .contexo is missing")
	}
}

func TestMCPInstallUnknownTool(t *testing.T) {
	project := tmpContexoProject(t)
	cmd, _ := bufCmd()
	if err := runMCPInstall(cmd, project, "bogus", true, testDeps(false, nil)); err == nil {
		t.Errorf("expected error for an unknown --tool value")
	}
}

func TestMCPInstallAllWiresProjectLocal(t *testing.T) {
	project := tmpContexoProject(t)
	cmd, _ := bufCmd()
	if err := runMCPInstall(cmd, project, "all", true, testDeps(false, nil)); err != nil {
		t.Fatalf("install all: %v", err)
	}
	for _, p := range []string{agentwire.ClaudeMCPPath(project), agentwire.CursorMCPPath(project)} {
		if wired, _ := agentwire.WiredJSON(p); !wired {
			t.Errorf("%s not wired by --tool=all", p)
		}
	}
}

func TestMCPInstallAllIncludesCodexWhenInstalled(t *testing.T) {
	project := tmpContexoProject(t)
	var calls [][]string
	cmd, _ := bufCmd()
	if err := runMCPInstall(cmd, project, "all", true, testDeps(true, &calls)); err != nil {
		t.Fatalf("install all: %v", err)
	}
	if len(calls) == 0 {
		t.Errorf("expected codex wired under --tool=all when codex is installed")
	}
}

func TestMCPInstallAllSkipsCodexWhenAbsent(t *testing.T) {
	project := tmpContexoProject(t)
	var calls [][]string
	cmd, _ := bufCmd()
	if err := runMCPInstall(cmd, project, "all", true, testDeps(false, &calls)); err != nil {
		t.Fatalf("install all: %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("codex must not be touched under --tool=all when not installed; got %v", calls)
	}
}

func TestMCPUninstallRemovesClaude(t *testing.T) {
	project := tmpContexoProject(t)
	cmd, _ := bufCmd()
	if err := runMCPInstall(cmd, project, "claude", true, testDeps(false, nil)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := runMCPUninstall(cmd, project, "claude", testDeps(false, nil)); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if wired, _ := agentwire.WiredJSON(agentwire.ClaudeMCPPath(project)); wired {
		t.Errorf("claude entry should be gone after uninstall")
	}
}

func TestMCPUninstallCodexInvokesRemove(t *testing.T) {
	project := tmpContexoProject(t)
	var calls [][]string
	cmd, _ := bufCmd()
	if err := runMCPUninstall(cmd, project, "codex", testDeps(true, &calls)); err != nil {
		t.Fatalf("uninstall codex: %v", err)
	}
	sawRemove := false
	for _, c := range calls {
		if len(c) >= 2 && c[0] == "mcp" && c[1] == "remove" {
			sawRemove = true
		}
	}
	if !sawRemove {
		t.Errorf("expected `codex mcp remove`, got %v", calls)
	}
}

func TestMCPStatusReportsAllTools(t *testing.T) {
	project := tmpContexoProject(t)
	cmd, out := bufCmd()
	if err := runMCPInstall(cmd, project, "claude", true, testDeps(false, nil)); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := runMCPStatus(cmd, project, testDeps(false, nil)); err != nil {
		t.Fatalf("status: %v", err)
	}
	s := out.String()
	for _, want := range []string{"Claude Code", "Cursor", "Codex", "Add another agent"} {
		if !strings.Contains(s, want) {
			t.Errorf("status output should mention %q; got:\n%s", want, s)
		}
	}
}
