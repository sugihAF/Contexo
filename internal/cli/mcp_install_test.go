package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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
		codexInstalled:  func() bool { return codexInstalled },
		vscodeInstalled: func() bool { return false },
	}
}

// wp05Deps is testDeps with Codex absent and VS Code detection stubbed.
func wp05Deps(vscodeInstalled bool) mcpWireDeps {
	d := testDeps(false, nil)
	d.vscodeInstalled = func() bool { return vscodeInstalled }
	return d
}

// wp05IsolateHome points HOME/USERPROFILE at a temp dir so nothing a test
// runs can read or write the real user's home.
func wp05IsolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// wp05VSCodeJSONC is a .vscode/mcp.json that VS Code accepts (a comment and a
// trailing comma) but strict JSON rejects. It holds a contexo entry.
const wp05VSCodeJSONC = `{
  // keep me
  "servers": {
    "contexo": {"type": "stdio", "command": "ctx", "args": ["mcp", "--root", "${workspaceFolder}"]},
  }
}
`

// wp05SeedVSCode writes body to <project>/.vscode/mcp.json and returns its path.
func wp05SeedVSCode(t *testing.T, project, body string) string {
	t.Helper()
	p := agentwire.VSCodeMCPPath(project)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// wp05AssertBytes fails unless the file at path still holds exactly want.
func wp05AssertBytes(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s was modified:\nbefore %s\nafter  %s", path, want, got)
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
	err := runMCPInstall(cmd, project, "bogus", true, testDeps(false, nil))
	if err == nil {
		t.Fatalf("expected error for an unknown --tool value")
	}
	if !strings.Contains(err.Error(), "vscode") {
		t.Errorf("unknown --tool error should list vscode; got %v", err)
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
	for _, want := range []string{"Claude Code", "Cursor", "VS Code", "Codex", "Add another agent"} {
		if !strings.Contains(s, want) {
			t.Errorf("status output should mention %q; got:\n%s", want, s)
		}
	}
}

func TestMCPInstallToolVSCode(t *testing.T) {
	wp05IsolateHome(t)
	project := tmpContexoProject(t)
	cmd, out := bufCmd()
	// Explicit --tool=vscode wires even when VS Code is not detected.
	if err := runMCPInstall(cmd, project, "vscode", true, wp05Deps(false)); err != nil {
		t.Fatalf("install vscode: %v", err)
	}
	if wired, err := agentwire.WiredVSCode(agentwire.VSCodeMCPPath(project)); !wired || err != nil {
		t.Errorf(".vscode/mcp.json should hold the contexo entry; wired=%v err=%v", wired, err)
	}
	if !strings.Contains(out.String(), "Wired VS Code (.vscode/mcp.json)") {
		t.Errorf("missing success line; got:\n%s", out.String())
	}
	for _, p := range []string{agentwire.ClaudeMCPPath(project), agentwire.CursorMCPPath(project)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("--tool=vscode must not create %s; stat err=%v", p, err)
		}
	}
}

func TestMCPInstallAllIncludesVSCodeOnlyWhenDetected(t *testing.T) {
	for _, tc := range []struct {
		name     string
		detected bool
	}{{"detected", true}, {"not_detected", false}} {
		t.Run(tc.name, func(t *testing.T) {
			wp05IsolateHome(t)
			project := tmpContexoProject(t)
			cmd, _ := bufCmd()
			if err := runMCPInstall(cmd, project, "all", true, wp05Deps(tc.detected)); err != nil {
				t.Fatalf("install all: %v", err)
			}
			p := agentwire.VSCodeMCPPath(project)
			if tc.detected {
				if wired, _ := agentwire.WiredVSCode(p); !wired {
					t.Errorf("--tool=all should wire VS Code when detected")
				}
			} else if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("--tool=all must not create .vscode/mcp.json when VS Code is not detected; stat err=%v", err)
			}
			if wired, _ := agentwire.WiredJSON(agentwire.ClaudeMCPPath(project)); !wired {
				t.Errorf("--tool=all should still wire Claude")
			}
		})
	}
}

func TestMCPUninstallAllAlwaysIncludesVSCode(t *testing.T) {
	wp05IsolateHome(t)
	project := tmpContexoProject(t)
	p := agentwire.VSCodeMCPPath(project)
	if _, err := agentwire.WireVSCode(p); err != nil {
		t.Fatalf("WireVSCode: %v", err)
	}
	cmd, _ := bufCmd()
	// Detection is off: uninstall must still clean up what is on disk.
	if err := runMCPUninstall(cmd, project, "all", wp05Deps(false)); err != nil {
		t.Fatalf("uninstall all: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf(".vscode/mcp.json held only contexo and should be deleted; stat err=%v", err)
	}
}

func TestMCPInstallVSCodeJSONCPrintsSnippet(t *testing.T) {
	wp05IsolateHome(t)
	project := tmpContexoProject(t)
	p := wp05SeedVSCode(t, project, wp05VSCodeJSONC)
	cmd, out := bufCmd()
	err := runMCPInstall(cmd, project, "vscode", true, wp05Deps(false))
	if !errors.Is(err, agentwire.ErrUnparseableVSCodeConfig) {
		t.Errorf("explicit --tool=vscode on JSONC: err = %v, want ErrUnparseableVSCodeConfig", err)
	}
	for _, want := range []string{"${workspaceFolder}", `"servers"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output should carry the manual snippet (%q); got:\n%s", want, out.String())
		}
	}
	wp05AssertBytes(t, p, wp05VSCodeJSONC)
}

func TestMCPInstallAllVSCodeJSONCWarnsAndContinues(t *testing.T) {
	wp05IsolateHome(t)
	project := tmpContexoProject(t)
	p := wp05SeedVSCode(t, project, wp05VSCodeJSONC)
	var calls [][]string
	deps := testDeps(true, &calls) // codex runs AFTER vscode: proves the loop went on
	deps.vscodeInstalled = func() bool { return true }
	cmd, out := bufCmd()
	if err := runMCPInstall(cmd, project, "all", true, deps); err != nil {
		t.Fatalf("install all must not fail on a JSONC VS Code file: %v", err)
	}
	for _, want := range []string{"not strict JSON", "${workspaceFolder}"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q; got:\n%s", want, out.String())
		}
	}
	wp05AssertBytes(t, p, wp05VSCodeJSONC)
	if wired, _ := agentwire.WiredJSON(agentwire.ClaudeMCPPath(project)); !wired {
		t.Errorf("Claude should still be wired")
	}
	if len(calls) == 0 {
		t.Errorf("codex should still be wired after the VS Code warning")
	}
}

func TestMCPUninstallVSCodeJSONCLeftAlone(t *testing.T) {
	wp05IsolateHome(t)
	project := tmpContexoProject(t)
	p := wp05SeedVSCode(t, project, wp05VSCodeJSONC)
	cmd, out := bufCmd()
	if err := runMCPUninstall(cmd, project, "vscode", wp05Deps(false)); err != nil {
		t.Fatalf("uninstall vscode: %v", err)
	}
	if !strings.Contains(out.String(), "remove servers.contexo manually") {
		t.Errorf("missing manual-removal warning; got:\n%s", out.String())
	}
	wp05AssertBytes(t, p, wp05VSCodeJSONC)
}

// TestMCPUninstallVSCodeJSONCWithoutContexo: VS Code documents mcp.json as
// JSONC, so a commented file that never held contexo is ordinary. Uninstall
// (including the default --tool=all) must not ask the user to remove an
// entry that is not there, matching detach.
func TestMCPUninstallVSCodeJSONCWithoutContexo(t *testing.T) {
	const body = "{\n  // only my own server\n  \"servers\": {\"github\": {\"type\": \"http\", \"url\": \"https://api.githubcopilot.com/mcp/\"}}\n}\n"
	for _, tool := range []string{"all", "vscode"} {
		t.Run(tool, func(t *testing.T) {
			wp05IsolateHome(t)
			project := tmpContexoProject(t)
			p := wp05SeedVSCode(t, project, body)
			cmd, out := bufCmd()
			if err := runMCPUninstall(cmd, project, tool, wp05Deps(false)); err != nil {
				t.Fatalf("uninstall %s: %v", tool, err)
			}
			s := out.String()
			if strings.Contains(s, "remove servers.contexo manually") {
				t.Errorf("no contexo entry exists, so no manual step is needed; got:\n%s", s)
			}
			if !strings.Contains(s, "VS Code not wired (.vscode/mcp.json); nothing to do.") {
				t.Errorf("missing the VS Code no-op line; got:\n%s", s)
			}
			wp05AssertBytes(t, p, body)
		})
	}
}
