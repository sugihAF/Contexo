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

func execGenerate(t *testing.T, root, stdin string, args ...string) string {
	t.Helper()
	prev := rootDir
	rootDir = root
	t.Cleanup(func() { rootDir = prev })

	fixture, _ := os.ReadFile(filepath.Join("..", "generate", "testdata", "graph.json"))
	prevRunner := genRunner
	genRunner = func(projectRoot, cacheDir, backend string) (string, error) {
		out := filepath.Join(cacheDir, "graphify-out")
		os.MkdirAll(out, 0o755)
		os.WriteFile(filepath.Join(out, "graph.json"), fixture, 0o644)
		os.WriteFile(filepath.Join(out, "GRAPH_REPORT.md"), []byte("# overview"), 0o644)
		return out, nil
	}
	t.Cleanup(func() { genRunner = prevRunner })

	cmd := newGenerateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("generate execute: %v\n%s", err, out.String())
	}
	return out.String()
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
