package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInitInstallsHookByDefault(t *testing.T) {
	project := t.TempDir()
	prev := rootDir
	rootDir = project
	t.Cleanup(func() { rootDir = prev })

	cmd := NewInitCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := runInit(cmd, false /* mcp */, false /* gitignore */, false /* hooks */); err != nil {
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

	if err := runInit(cmd, false, false, true /* skip hooks */); err != nil {
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
		if err := runInit(cmd, false, false, false); err != nil {
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
