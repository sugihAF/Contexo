package mcp

import (
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/migrate"
	"github.com/sugihAF/contexo/internal/schema"
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
