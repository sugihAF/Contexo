package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/cli/agentwire"
)

// withCursorDetected overrides the Cursor/Codex detection probes for one test.
func withDetection(t *testing.T, cursor, codex bool) {
	t.Helper()
	pc, px := detectCursor, detectCodex
	detectCursor = func() bool { return cursor }
	detectCodex = func() bool { return codex }
	t.Cleanup(func() { detectCursor, detectCodex = pc, px })
}

// wp05WithVSCodeDetected overrides the VS Code detection probe for one test.
// Developer machines often have a real ~/.vscode, so every init and detach
// test sets it explicitly.
func wp05WithVSCodeDetected(t *testing.T, on bool) {
	t.Helper()
	prev := detectVSCode
	detectVSCode = func() bool { return on }
	t.Cleanup(func() { detectVSCode = prev })
}

// wp05InitProject points rootDir at a fresh temp project with HOME isolated,
// Cursor/Codex undetected and VS Code detection set to vscode.
func wp05InitProject(t *testing.T, vscode bool) string {
	t.Helper()
	wp05IsolateHome(t)
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })
	withDetection(t, false, false)
	wp05WithVSCodeDetected(t, vscode)
	return project
}

// wp05InitCmd is NewInitCmd writing stdout into the returned buffer.
func wp05InitCmd() (*cobra.Command, *bytes.Buffer) {
	cmd := NewInitCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	return cmd, out
}

func TestInitInstallsHookByDefault(t *testing.T) {
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })
	wp05WithVSCodeDetected(t, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := runInit(cmd, false /* mcp */, false /* cursor */, false /* vscode */, false /* gitignore */, false /* hooks */); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	installed, err := hookInstalled(project)
	if err != nil {
		t.Fatalf("hookInstalled: %v", err)
	}
	if !installed {
		t.Errorf("ctx init should install the Stop hook by default")
	}
	if _, err := os.Stat(filepath.Join(project, ".contexo")); err != nil {
		t.Errorf("ctx init must create .contexo/: %v", err)
	}
}

func TestInitSkipsHookWhenFlagged(t *testing.T) {
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })
	wp05WithVSCodeDetected(t, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := runInit(cmd, false, false, false, false, true /* skip hooks */); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	installed, err := hookInstalled(project)
	if err != nil {
		t.Fatalf("hookInstalled: %v", err)
	}
	if installed {
		t.Errorf("--no-hooks should suppress hook install")
	}
	// .contexo must still be created.
	if _, err := os.Stat(filepath.Join(project, ".contexo")); err != nil {
		t.Errorf("ctx init must still create .contexo/ even with --no-hooks: %v", err)
	}
}

func TestInitIsIdempotentForHookEntry(t *testing.T) {
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })
	wp05WithVSCodeDetected(t, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	for i := 0; i < 3; i++ {
		if err := runInit(cmd, false, false, false, false, false); err != nil {
			t.Fatalf("runInit iteration %d: %v", i, err)
		}
	}

	settings, err := loadSettings(filepath.Join(project, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	hooks := settings["hooks"].(map[string]interface{})
	stop := hooks["Stop"].([]interface{})
	contexoEntries := 0
	for _, g := range stop {
		group := g.(map[string]interface{})
		for _, h := range group["hooks"].([]interface{}) {
			hm := h.(map[string]interface{})
			if hm["_contexo"] == contexoHookMarker {
				contexoEntries++
			}
		}
	}
	if contexoEntries != 1 {
		t.Errorf("repeated init should not duplicate hook entries; got %d, want 1", contexoEntries)
	}
}

func TestInitWiresClaudeMCP(t *testing.T) {
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })
	wp05WithVSCodeDetected(t, false)
	withDetection(t, false, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, false, false, false, false); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if wired, _ := agentwire.WiredJSON(agentwire.ClaudeMCPPath(project)); !wired {
		t.Errorf("ctx init should wire Claude Code (.mcp.json) by default")
	}
}

func TestInitWiresCursorWhenDetected(t *testing.T) {
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })
	wp05WithVSCodeDetected(t, false)
	withDetection(t, true /* cursor */, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, false, false, false, false); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if wired, _ := agentwire.WiredJSON(agentwire.CursorMCPPath(project)); !wired {
		t.Errorf("ctx init should auto-wire Cursor (.cursor/mcp.json) when Cursor is detected")
	}
}

func TestInitSkipsCursorWhenNotDetected(t *testing.T) {
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })
	wp05WithVSCodeDetected(t, false)
	withDetection(t, false /* cursor */, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, false, false, false, false); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if wired, _ := agentwire.WiredJSON(agentwire.CursorMCPPath(project)); wired {
		t.Errorf("Cursor must not be wired when not detected")
	}
	// Claude is always wired regardless of Cursor detection.
	if wired, _ := agentwire.WiredJSON(agentwire.ClaudeMCPPath(project)); !wired {
		t.Errorf("Claude should still be wired")
	}
}

func TestInitInstallsCodexHooksWhenDetected(t *testing.T) {
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })
	wp05WithVSCodeDetected(t, false)
	withDetection(t, false /* cursor */, true /* codex */)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, false, false, false, false); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if wired, _ := agentwire.CodexHooksWired(project); !wired {
		t.Errorf("ctx init should install Codex capture hooks when Codex is detected")
	}
}

func TestInitInstallsCursorHooksWhenDetected(t *testing.T) {
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })
	wp05WithVSCodeDetected(t, false)
	withDetection(t, true /* cursor */, false /* codex */)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, false, false, false, false); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if wired, _ := agentwire.CursorHooksWired(project); !wired {
		t.Errorf("ctx init should install Cursor capture hooks when Cursor is detected")
	}
}

func TestInitSkipsCodexHooksWhenNotDetected(t *testing.T) {
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })
	wp05WithVSCodeDetected(t, false)
	withDetection(t, false, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, false, false, false, false); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if wired, _ := agentwire.CodexHooksWired(project); wired {
		t.Errorf("Codex hooks must not install when Codex isn't detected")
	}
	if on, _ := hookInstalled(project); !on {
		t.Errorf("Claude hook should install regardless of Codex")
	}
}

func TestInitNoCursorFlagSkipsCursor(t *testing.T) {
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })
	wp05WithVSCodeDetected(t, false)
	withDetection(t, true /* cursor detected */, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, true /* skipCursor */, false, false, false); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if wired, _ := agentwire.WiredJSON(agentwire.CursorMCPPath(project)); wired {
		t.Errorf("--no-cursor must suppress Cursor wiring even when detected")
	}
}

func TestInitAutoWiresVSCodeWhenDetected(t *testing.T) {
	project := wp05InitProject(t, true)
	cmd, out := wp05InitCmd()
	if err := runInit(cmd, false, false, false, false, false); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if wired, err := agentwire.WiredVSCode(agentwire.VSCodeMCPPath(project)); !wired || err != nil {
		t.Errorf("ctx init should wire .vscode/mcp.json when VS Code is detected; wired=%v err=%v", wired, err)
	}
	s := out.String()
	if !strings.Contains(s, "teammates need ctx on PATH") {
		t.Errorf("missing the committed-.vscode hint; got:\n%s", s)
	}
	if !strings.Contains(s, "wired (.vscode/mcp.json)") || strings.Contains(s, "not wired (.vscode/mcp.json)") {
		t.Errorf("integration table should show VS Code wired; got:\n%s", s)
	}
}

func TestInitVSCodeRerunLeavesEntryAlone(t *testing.T) {
	project := wp05InitProject(t, true)
	cmd, out := wp05InitCmd()
	if err := runInit(cmd, false, false, false, false, false); err != nil {
		t.Fatalf("first runInit: %v", err)
	}
	p := agentwire.VSCodeMCPPath(project)
	first, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	out.Reset()
	if err := runInit(cmd, false, false, false, false, false); err != nil {
		t.Fatalf("second runInit: %v", err)
	}
	wp05AssertBytes(t, p, string(first))
	if !strings.Contains(out.String(), ".vscode/mcp.json already has the contexo entry") {
		t.Errorf("re-run should report the existing entry; got:\n%s", out.String())
	}
}

func TestInitNoVSCodeFlag(t *testing.T) {
	project := wp05InitProject(t, true)
	cmd, _ := wp05InitCmd()
	cmd.SetArgs([]string{"--no-vscode"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ctx init --no-vscode: %v", err)
	}
	if _, err := os.Stat(agentwire.VSCodeMCPPath(project)); !os.IsNotExist(err) {
		t.Errorf("--no-vscode must suppress VS Code wiring even when detected; stat err=%v", err)
	}
	if wired, _ := agentwire.WiredJSON(agentwire.ClaudeMCPPath(project)); !wired {
		t.Errorf("Claude should still be wired")
	}
}

func TestInitNoMCPSkipsVSCode(t *testing.T) {
	project := wp05InitProject(t, true)
	cmd, _ := wp05InitCmd()
	if err := runInit(cmd, true /* skipMCP */, false, false, false, false); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if _, err := os.Stat(agentwire.VSCodeMCPPath(project)); !os.IsNotExist(err) {
		t.Errorf("--no-mcp must skip VS Code too; stat err=%v", err)
	}
}

func TestInitVSCodeJSONCWarnsAndContinues(t *testing.T) {
	project := wp05InitProject(t, true)
	p := wp05SeedVSCode(t, project, wp05VSCodeJSONC)
	cmd, out := wp05InitCmd()
	if err := runInit(cmd, false, false, false, false, false); err != nil {
		t.Fatalf("init must not fail on a JSONC .vscode/mcp.json: %v", err)
	}
	wp05AssertBytes(t, p, wp05VSCodeJSONC)
	s := out.String()
	// The table and the guide also mention "not strict JSON" and
	// ${workspaceFolder}, so look for the warning and the full snippet.
	for _, want := range []string{"not strict JSON (comments or trailing commas?); left it untouched", agentwire.VSCodeManualSnippet()} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q; got:\n%s", want, s)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".contexo")); err != nil {
		t.Errorf("ctx init must still create .contexo/: %v", err)
	}
	if wired, _ := agentwire.WiredJSON(agentwire.ClaudeMCPPath(project)); !wired {
		t.Errorf("Claude should still be wired")
	}
}

// TestInitVSCodeWireErrorWarnsAndContinues: VS Code wiring runs on detection
// alone, so a broken .vscode/mcp.json (not JSONC: unreadable, dangling
// symlink, .vscode a plain file) must not abort init before .contexo/ is
// git-ignored and the Stop hook is installed.
func TestInitVSCodeWireErrorWarnsAndContinues(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(t *testing.T, project string)
	}{
		{"vscode_is_a_file", func(t *testing.T, project string) {
			writeFile(t, filepath.Join(project, ".vscode"), "not a directory\n")
		}},
		{"dangling_symlink", func(t *testing.T, project string) {
			if runtime.GOOS == "windows" {
				t.Skip("symlinks need extra privileges on windows")
			}
			if err := os.Mkdir(filepath.Join(project, ".vscode"), 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "dotfiles-not-cloned", "mcp.json")
			if err := os.Symlink(target, agentwire.VSCodeMCPPath(project)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := wp05InitProject(t, true)
			if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			tc.seed(t, project)
			cmd, out := wp05InitCmd()
			if err := runInit(cmd, false, false, false, false, false); err != nil {
				t.Fatalf("a VS Code wiring failure must not abort init: %v", err)
			}
			s := out.String()
			for _, want := range []string{"could not wire VS Code", "ctx mcp install --tool=vscode"} {
				if !strings.Contains(s, want) {
					t.Errorf("output missing %q; got:\n%s", want, s)
				}
			}
			gi, err := os.ReadFile(filepath.Join(project, ".gitignore"))
			if err != nil || !strings.Contains(string(gi), ".contexo/") {
				t.Errorf(".contexo/ must still be git-ignored; .gitignore=%q err=%v", gi, err)
			}
			if on, _ := hookInstalled(project); !on {
				t.Errorf("the Claude Stop hook must still be installed")
			}
			if wired, _ := agentwire.WiredJSON(agentwire.ClaudeMCPPath(project)); !wired {
				t.Errorf("Claude should still be wired")
			}
		})
	}
}

func TestInitSkipsVSCodeWhenNotDetected(t *testing.T) {
	project := wp05InitProject(t, false)
	cmd, _ := wp05InitCmd()
	if err := runInit(cmd, false, false, false, false, false); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if _, err := os.Stat(agentwire.VSCodeMCPPath(project)); !os.IsNotExist(err) {
		t.Errorf("VS Code must not be wired when not detected; stat err=%v", err)
	}
}
