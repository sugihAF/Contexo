package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/generate"
	"github.com/sugihAF/contexo/internal/migrate"
)

// stubGenRunner points genRunner at the trimmed Graphify fixture.
func stubGenRunner(t *testing.T) {
	t.Helper()
	fixture, _ := os.ReadFile(filepath.Join("..", "generate", "testdata", "graph.json"))
	prev := genRunner
	genRunner = func(projectRoot, cacheDir, backend string) (string, error) {
		out := filepath.Join(cacheDir, "graphify-out")
		os.MkdirAll(out, 0o755)
		os.WriteFile(filepath.Join(out, "graph.json"), fixture, 0o644)
		os.WriteFile(filepath.Join(out, "GRAPH_REPORT.md"), []byte("# overview"), 0o644)
		return out, nil
	}
	t.Cleanup(func() { genRunner = prev })
}

// runGen runs `ctx generate` with a stubbed runner and returns (output, err).
// It does NOT stub graphifyInstalled — the install-flow tests control that.
func runGen(t *testing.T, root, stdin string, args ...string) (string, error) {
	t.Helper()
	prev := rootDir
	rootDir = root
	t.Cleanup(func() { rootDir = prev })
	stubGenRunner(t)

	cmd := newGenerateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// execGenerate is the happy-path harness: Graphify is present, so the install
// prompt is skipped. It fatals on error.
func execGenerate(t *testing.T, root, stdin string, args ...string) string {
	t.Helper()
	prev := graphifyInstalled
	graphifyInstalled = func() bool { return true }
	t.Cleanup(func() { graphifyInstalled = prev })

	out, err := runGen(t, root, stdin, args...)
	if err != nil {
		t.Fatalf("generate execute: %v\n%s", err, out)
	}
	return out
}

func TestGenerateListDoesNotStage(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	os.MkdirAll(hub, 0o755)
	out := execGenerate(t, root, "", "--list")
	if !strings.Contains(out, "slices/") && !strings.Contains(out, "codebase-overview") {
		t.Errorf("--list should show candidates:\n%s", out)
	}
	if m, _ := migrate.LoadManifest(hub); m != nil {
		t.Errorf("--list must not stage a manifest")
	}
}

func TestGenerateStagesManifest(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	os.MkdirAll(hub, 0o755)
	out := execGenerate(t, root, "all\ny\n")
	if !strings.Contains(out, "finish the contexo generation") {
		t.Errorf("expected handoff:\n%s", out)
	}
	m, err := migrate.LoadManifest(hub)
	if err != nil || m == nil || m.Kind != "generate" || len(m.Items) == 0 {
		t.Fatalf("generate manifest not staged: %v %+v", err, m)
	}
	_ = generate.CacheDir // keep import used
}

// When Graphify is absent, saying "y" installs it (stub), then generation
// proceeds and stages the manifest.
func TestGenerateAutoInstallsOnConsent(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	os.MkdirAll(hub, 0o755)

	installed := false
	prevInstalled := graphifyInstalled
	graphifyInstalled = func() bool { return installed }
	t.Cleanup(func() { graphifyInstalled = prevInstalled })

	prevInstall := installGraphify
	installGraphify = func() (string, error) { installed = true; return "uv", nil }
	t.Cleanup(func() { installGraphify = prevInstall })

	out, err := runGen(t, root, "y\nall\ny\n")
	if err != nil {
		t.Fatalf("generate execute: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Installed Graphify via uv") {
		t.Errorf("expected install confirmation:\n%s", out)
	}
	if !strings.Contains(out, "finish the contexo generation") {
		t.Errorf("expected handoff after auto-install:\n%s", out)
	}
	m, err := migrate.LoadManifest(hub)
	if err != nil || m == nil || len(m.Items) == 0 {
		t.Fatalf("manifest not staged after auto-install: %v %+v", err, m)
	}
}

// Declining the install is an error and never calls installGraphify.
func TestGenerateDeclineInstallErrors(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	os.MkdirAll(hub, 0o755)

	prevInstalled := graphifyInstalled
	graphifyInstalled = func() bool { return false }
	t.Cleanup(func() { graphifyInstalled = prevInstalled })

	prevInstall := installGraphify
	installGraphify = func() (string, error) {
		t.Fatal("installGraphify must not run when the user declines")
		return "", nil
	}
	t.Cleanup(func() { installGraphify = prevInstall })

	out, err := runGen(t, root, "n\n")
	if err == nil {
		t.Fatalf("declining install should error:\n%s", out)
	}
	if m, _ := migrate.LoadManifest(hub); m != nil {
		t.Errorf("declining install must not stage a manifest")
	}
}
