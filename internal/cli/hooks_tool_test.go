package cli

import (
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/cli/agentwire"
)

func TestExpandHookTools(t *testing.T) {
	for in, want := range map[string]int{"claude": 1, "codex": 1, "cursor": 1, "all": 3, "": 3} {
		got, err := expandHookTools(in)
		if err != nil || len(got) != want {
			t.Errorf("expandHookTools(%q) = %v, %v; want len %d", in, got, err, want)
		}
	}
	if _, err := expandHookTools("bogus"); err == nil {
		t.Errorf("expected error for unknown tool")
	}
}

func TestHooksInstallCodex(t *testing.T) {
	project := tmpContexoProject(t)
	cmd, _ := bufCmd()
	if err := runHooksInstall(cmd, project, "codex"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if wired, _ := agentwire.CodexHooksWired(project); !wired {
		t.Errorf("codex capture hooks should be installed")
	}
}

func TestHooksInstallCursor(t *testing.T) {
	project := tmpContexoProject(t)
	cmd, _ := bufCmd()
	if err := runHooksInstall(cmd, project, "cursor"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if wired, _ := agentwire.CursorHooksWired(project); !wired {
		t.Errorf("cursor capture hooks should be installed")
	}
}

func TestHooksInstallClaude(t *testing.T) {
	project := tmpContexoProject(t)
	cmd, _ := bufCmd()
	if err := runHooksInstall(cmd, project, "claude"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if on, _ := hookInstalled(project); !on {
		t.Errorf("claude stop hook should be installed")
	}
}

func TestHooksInstallAllWithCodexDetected(t *testing.T) {
	project := tmpContexoProject(t)
	withDetection(t, false, true) // codex detected
	cmd, _ := bufCmd()
	if err := runHooksInstall(cmd, project, "all"); err != nil {
		t.Fatalf("install all: %v", err)
	}
	if on, _ := hookInstalled(project); !on {
		t.Errorf("claude hook missing under --tool=all")
	}
	if wired, _ := agentwire.CodexHooksWired(project); !wired {
		t.Errorf("codex hooks missing under --tool=all when codex detected")
	}
}

func TestHooksInstallAllWithoutCodex(t *testing.T) {
	project := tmpContexoProject(t)
	withDetection(t, false, false) // codex NOT detected
	cmd, _ := bufCmd()
	if err := runHooksInstall(cmd, project, "all"); err != nil {
		t.Fatalf("install all: %v", err)
	}
	if on, _ := hookInstalled(project); !on {
		t.Errorf("claude hook should still install under --tool=all")
	}
	if wired, _ := agentwire.CodexHooksWired(project); wired {
		t.Errorf("codex hooks must NOT install under --tool=all when codex absent")
	}
}

func TestHooksInstallRequiresProject(t *testing.T) {
	cmd, _ := bufCmd()
	if err := runHooksInstall(cmd, t.TempDir(), "claude"); err == nil {
		t.Errorf("expected error when .contexo is missing")
	}
}

func TestHooksUninstallCodex(t *testing.T) {
	project := tmpContexoProject(t)
	cmd, _ := bufCmd()
	if err := runHooksInstall(cmd, project, "codex"); err != nil {
		t.Fatal(err)
	}
	if err := runHooksUninstall(cmd, project, "codex"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if wired, _ := agentwire.CodexHooksWired(project); wired {
		t.Errorf("codex hooks should be gone after uninstall")
	}
}

func TestHooksStatusMentionsBothTools(t *testing.T) {
	project := tmpContexoProject(t)
	cmd, out := bufCmd()
	if err := runHooksStatus(cmd, project, "all"); err != nil {
		t.Fatalf("status: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "claude") || !strings.Contains(s, "codex") {
		t.Errorf("status should mention both tools; got:\n%s", s)
	}
}
