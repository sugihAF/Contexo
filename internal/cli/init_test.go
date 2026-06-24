package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

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

func TestInitInstallsHookByDefault(t *testing.T) {
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := runInit(cmd, false /* mcp */, false /* cursor */, false /* gitignore */, false /* hooks */); err != nil {
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

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := runInit(cmd, false, false, false, true /* skip hooks */); err != nil {
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

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	for i := 0; i < 3; i++ {
		if err := runInit(cmd, false, false, false, false); err != nil {
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
	withDetection(t, false, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, false, false, false); err != nil {
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
	withDetection(t, true /* cursor */, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, false, false, false); err != nil {
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
	withDetection(t, false /* cursor */, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, false, false, false); err != nil {
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
	withDetection(t, false /* cursor */, true /* codex */)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, false, false, false); err != nil {
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
	withDetection(t, true /* cursor */, false /* codex */)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, false, false, false); err != nil {
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
	withDetection(t, false, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, false, false, false); err != nil {
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
	withDetection(t, true /* cursor detected */, false)

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runInit(cmd, false, true /* skipCursor */, false, false); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if wired, _ := agentwire.WiredJSON(agentwire.CursorMCPPath(project)); wired {
		t.Errorf("--no-cursor must suppress Cursor wiring even when detected")
	}
}
