package mcp

import (
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/migrate"
	"github.com/sugihAF/contexo/internal/schema"
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
