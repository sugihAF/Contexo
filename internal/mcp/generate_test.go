package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/migrate"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
)

func TestGenerateSourceSlug(t *testing.T) {
	if got := generateSourceSlug("My Proj", "2026-07-05"); got != "2026-07-05-generate-my-proj" {
		t.Errorf("got %q", got)
	}
	if got := generateSourceSlug("  ", "2026-07-05"); got != "2026-07-05-generate-codebase" {
		t.Errorf("blank label: got %q", got)
	}
}

func TestBuildGenerateCatalog(t *testing.T) {
	cat := migrate.Catalog{Items: []migrate.Candidate{
		{ID: 1, Detector: "graphify", RelPath: "slices/community-auth.md", SuggestedType: schema.TypeConcept, SuggestedSlug: "auth", EstTokens: 300, Preview: "Subsystem"},
		{ID: 2, Detector: "graphify", RelPath: "slices/component-apirouter.md", SuggestedType: schema.TypeEntity, SuggestedSlug: "apirouter", EstTokens: 120, Preview: "routing.go:L10"},
	}}
	out := buildGenerateCatalog(cat, "/proj", "2026-07-05-generate-proj")
	for _, want := range []string{
		"<GENERATE_CATALOG", "[1]", "community-auth.md", "[2]",
		"VERIFY", "EXTRACTED", "ctx_write_page", "no_distill",
		"REDACT", "data to verify",
		"ctx_generate(done=true)", "2026-07-05-generate-proj", "</GENERATE_CATALOG>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("catalog directive missing %q", want)
		}
	}
	if strings.Contains(out, "Import exactly these") {
		t.Errorf("catalog leaked resume-only phrasing")
	}
}

func TestBuildGenerateResume(t *testing.T) {
	m := &migrate.Manifest{SourceRoot: "/proj", Label: "proj", Kind: "generate",
		Items: []migrate.ManifestItem{{RelPath: "slices/community-auth.md", AbsPath: "/c/.generate-cache/slices/community-auth.md", SuggestedType: schema.TypeConcept, SuggestedSlug: "auth"}}}
	out := buildGenerateResume(m, "2026-07-05-generate-proj")
	for _, want := range []string{"<GENERATE_RESUME", "community-auth.md", "VERIFY", "no_distill", "ctx_generate(done=true)"} {
		if !strings.Contains(out, want) {
			t.Errorf("resume directive missing %q", want)
		}
	}
}

func TestToolGenerateDiscoverThenDone(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	os.MkdirAll(hub, 0o755)
	store, err := pagestore.Open(hub)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(store)

	// stub the runner so no real graphify is needed
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

	res := s.toolGenerate(map[string]interface{}{})
	if res.IsError || !contains(res, "<GENERATE_CATALOG") {
		t.Fatalf("discover: %+v", res)
	}
	// discovery staged a generate manifest (for cleanup)
	if m, _ := migrate.LoadManifest(hub); m == nil || m.Kind != "generate" {
		t.Fatalf("expected staged generate manifest, got %+v", m)
	}
	// resume returns the generate resume directive
	if res2 := s.toolGenerate(map[string]interface{}{}); !contains(res2, "<GENERATE_RESUME") {
		t.Fatalf("resume: %+v", res2)
	}
	// done clears it
	if res3 := s.toolGenerate(map[string]interface{}{"done": true}); res3.IsError {
		t.Fatalf("done errored: %+v", res3)
	}
	if m, _ := migrate.LoadManifest(hub); m != nil {
		t.Errorf("manifest not cleared")
	}
}

func TestToolMigrateIgnoresGenerateManifest(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	if err := os.MkdirAll(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := pagestore.Open(hub)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(store)
	m := &migrate.Manifest{
		Version: 1, Kind: "generate", SourceRoot: root, Label: "x",
		Items: []migrate.ManifestItem{{RelPath: "slices/community-x.md", AbsPath: filepath.Join(hub, ".generate-cache", "slices", "community-x.md"), SuggestedType: schema.TypeConcept, SuggestedSlug: "x"}},
	}
	if err := migrate.SaveManifest(hub, m); err != nil {
		t.Fatal(err)
	}
	// ctx_migrate must NOT resume a generate-kind manifest (it should fall through to discovery).
	res := s.toolMigrate(map[string]interface{}{})
	if contains(res, "<MIGRATE_RESUME") {
		t.Errorf("ctx_migrate wrongly resumed a generate manifest: %+v", res)
	}
	// and the generate manifest must be left intact for ctx_generate to resume.
	if got, _ := migrate.LoadManifest(hub); got == nil || got.Kind != "generate" {
		t.Errorf("generate manifest was clobbered by ctx_migrate: %+v", got)
	}
}
