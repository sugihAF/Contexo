package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallCreatesSettings(t *testing.T) {
	project := tmpContexoProject(t)
	cmd := newHooksInstallCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := installHook(cmd, project); err != nil {
		t.Fatalf("install: %v", err)
	}

	path := filepath.Join(project, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if findContexoStopHook(settings) < 0 {
		t.Errorf("Contexo hook marker missing from settings: %s", data)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	project := tmpContexoProject(t)
	cmd := newHooksInstallCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := installHook(cmd, project); err != nil {
		t.Fatalf("install 1: %v", err)
	}
	if err := installHook(cmd, project); err != nil {
		t.Fatalf("install 2: %v", err)
	}

	settings, _ := loadSettings(filepath.Join(project, ".claude", "settings.json"))
	hooksField := settings["hooks"].(map[string]interface{})
	stop := hooksField["Stop"].([]interface{})
	contexoCount := 0
	for _, g := range stop {
		group := g.(map[string]interface{})
		for _, h := range group["hooks"].([]interface{}) {
			hm := h.(map[string]interface{})
			if hm["_contexo"] == contexoHookMarker {
				contexoCount++
			}
		}
	}
	if contexoCount != 1 {
		t.Errorf("got %d Contexo entries after double install, want 1", contexoCount)
	}
}

func TestInstallPreservesExistingHooks(t *testing.T) {
	project := tmpContexoProject(t)
	settingsPath := filepath.Join(project, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	pre := map[string]interface{}{
		"hooks": map[string]interface{}{
			"Stop": []interface{}{
				map[string]interface{}{
					"matcher": "",
					"hooks": []interface{}{
						map[string]interface{}{"type": "command", "command": "echo other"},
					},
				},
			},
			"PreCompact": []interface{}{
				map[string]interface{}{"hooks": []interface{}{map[string]interface{}{"type": "command", "command": "echo pre"}}},
			},
		},
	}
	data, _ := json.MarshalIndent(pre, "", "  ")
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		t.Fatalf("write pre: %v", err)
	}

	cmd := newHooksInstallCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := installHook(cmd, project); err != nil {
		t.Fatalf("install: %v", err)
	}

	settings, _ := loadSettings(settingsPath)
	hooks := settings["hooks"].(map[string]interface{})
	stop := hooks["Stop"].([]interface{})
	if len(stop) != 2 {
		t.Fatalf("Stop entries: got %d, want 2 (pre + contexo)", len(stop))
	}
	if _, hasPre := hooks["PreCompact"]; !hasPre {
		t.Errorf("PreCompact hook was wiped")
	}
}

func TestUninstallRemovesOnlyContexo(t *testing.T) {
	project := tmpContexoProject(t)
	cmd := newHooksInstallCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	settingsPath := filepath.Join(project, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pre := map[string]interface{}{
		"hooks": map[string]interface{}{
			"Stop": []interface{}{
				map[string]interface{}{
					"matcher": "",
					"hooks": []interface{}{
						map[string]interface{}{"type": "command", "command": "echo other"},
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(pre, "", "  ")
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		t.Fatalf("write pre: %v", err)
	}

	if err := installHook(cmd, project); err != nil {
		t.Fatalf("install: %v", err)
	}
	un := newHooksUninstallCmd()
	un.SetOut(&bytes.Buffer{})
	un.SetErr(&bytes.Buffer{})
	if err := uninstallHook(un, project); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	settings, _ := loadSettings(settingsPath)
	if findContexoStopHook(settings) >= 0 {
		t.Errorf("Contexo entry should be gone")
	}
	hooks, _ := settings["hooks"].(map[string]interface{})
	stop, _ := hooks["Stop"].([]interface{})
	if len(stop) != 1 {
		t.Errorf("other Stop hook was wiped: %v", stop)
	}
}

func TestInstallRequiresContexoProject(t *testing.T) {
	notProject := t.TempDir()
	cmd := newHooksInstallCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := installHook(cmd, notProject); err == nil {
		t.Errorf("expected error when .contexo missing")
	}
}

func TestStatusReportsInstalled(t *testing.T) {
	project := tmpContexoProject(t)
	cmd := newHooksInstallCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := installHook(cmd, project); err != nil {
		t.Fatalf("install: %v", err)
	}

	got, err := hookInstalled(project)
	if err != nil {
		t.Fatalf("hookInstalled: %v", err)
	}
	if !got {
		t.Errorf("expected installed=true after install")
	}
}

func TestUninstallNoSettings(t *testing.T) {
	project := tmpContexoProject(t)
	cmd := newHooksUninstallCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := uninstallHook(cmd, project); err != nil {
		t.Errorf("uninstall on absent settings should be no-op, got %v", err)
	}
}
