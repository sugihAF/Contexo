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

func TestMigrateSourceSlug(t *testing.T) {
	if got := migrateSourceSlug("My LLM-Wiki", "2026-07-05"); got != "2026-07-05-migrate-my-llm-wiki" {
		t.Errorf("got %q", got)
	}
	if got := migrateSourceSlug("", "2026-07-05"); got != "2026-07-05-migrate-kb" {
		t.Errorf("empty label: got %q, want 2026-07-05-migrate-kb", got)
	}
	if got := migrateSourceSlug("   ", "2026-07-05"); got != "2026-07-05-migrate-kb" {
		t.Errorf("blank label: got %q, want 2026-07-05-migrate-kb", got)
	}
}

func TestBuildMigrateCatalog(t *testing.T) {
	cat := migrate.Catalog{Items: []migrate.Candidate{
		{ID: 1, Detector: "docs", RelPath: "README.md", SuggestedType: schema.TypeEntity, SuggestedSlug: "proj", EstTokens: 100, Preview: "A project.", AlreadyImported: true},
		{ID: 2, Detector: "docs", RelPath: "docs/auth.md", SuggestedType: schema.TypeConcept, SuggestedSlug: "auth", EstTokens: 200, Preview: "Login."},
	}}
	out := buildMigrateCatalog(cat, "/proj", "2026-07-05-migrate-proj")
	for _, want := range []string{
		"<MIGRATE_CATALOG", "[1]", "README.md", "[2]", "docs/auth.md",
		"ctx_write_page", "no_distill", "REDACT", "already imported",
		"2026-07-05-migrate-proj", "</MIGRATE_CATALOG>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("catalog directive missing %q", want)
		}
	}
	if strings.Contains(out, "ctx_migrate(done=true)") {
		t.Errorf("catalog directive leaked the resume-only done=true step")
	}
}

func TestBuildMigrateCatalogEmpty(t *testing.T) {
	out := buildMigrateCatalog(migrate.Catalog{}, "/proj", "2026-07-05-migrate-proj")
	for _, want := range []string{"<MIGRATE_CATALOG", "No existing knowledge base was detected", "</MIGRATE_CATALOG>"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty catalog directive missing %q", want)
		}
	}
}

func TestBuildMigrateResume(t *testing.T) {
	m := &migrate.Manifest{
		SourceRoot: "/proj", Label: "proj",
		Items: []migrate.ManifestItem{
			{RelPath: "README.md", AbsPath: "/proj/README.md", SuggestedType: schema.TypeEntity, SuggestedSlug: "proj"},
		},
	}
	out := buildMigrateResume(m, "2026-07-05-migrate-proj")
	for _, want := range []string{"<MIGRATE_RESUME", "/proj/README.md", "ctx_migrate(done=true)", "no_distill", "REDACT"} {
		if !strings.Contains(out, want) {
			t.Errorf("resume directive missing %q", want)
		}
	}
	if strings.Contains(out, "ask which to import") {
		t.Errorf("resume directive leaked the catalog-only ask-user step")
	}
}

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	if err := os.MkdirAll(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := pagestore.Open(hub)
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(store), root
}

func TestToolMigrateDiscoverThenDone(t *testing.T) {
	s, root := newTestServer(t)
	// Seed a discoverable file in the project root.
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# Rules\n\nUse tabs."), 0o644); err != nil {
		t.Fatal(err)
	}

	res := s.toolMigrate(map[string]interface{}{})
	if res.IsError || !contains(res, "<MIGRATE_CATALOG") || !contains(res, "CLAUDE.md") {
		t.Fatalf("discover: %+v", res)
	}

	// Stage a manifest → resume mode.
	m := &migrate.Manifest{Version: 1, SourceRoot: root, Label: "proj",
		Items: []migrate.ManifestItem{{RelPath: "CLAUDE.md", AbsPath: filepath.Join(root, "CLAUDE.md"), SuggestedType: schema.TypeConcept, SuggestedSlug: "rules"}}}
	if err := migrate.SaveManifest(s.store.Root, m); err != nil {
		t.Fatal(err)
	}
	res = s.toolMigrate(map[string]interface{}{})
	if !contains(res, "<MIGRATE_RESUME") {
		t.Fatalf("resume: %+v", res)
	}

	// done=true clears it.
	res = s.toolMigrate(map[string]interface{}{"done": true})
	if res.IsError {
		t.Fatalf("done errored: %+v", res)
	}
	if mm, _ := migrate.LoadManifest(s.store.Root); mm != nil {
		t.Errorf("manifest not cleared")
	}
}

func contains(res *ToolResult, sub string) bool {
	for _, c := range res.Content {
		if len(c.Text) >= len(sub) && (indexOf(c.Text, sub) >= 0) {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
