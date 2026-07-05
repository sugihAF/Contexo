// internal/migrate/catalog_test.go
package migrate

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
)

func TestBuildOrdersDedupsAndFlags(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "README.md"), "# Proj\n\nx")            // docs → entity
	writeFile(t, filepath.Join(src, "CLAUDE.md"), "# Rules\n\ny")          // agentfiles → concept
	writeFile(t, filepath.Join(src, "docs", "auth.md"), "# Auth\n\nz")     // docs → concept

	// A .contexo store that already has "proj".
	hub := t.TempDir()
	writeFile(t, filepath.Join(hub, "wiki", "entities", "proj.md"), "---\n---\n")

	cat, err := Build(src, hub, DefaultDetectors())
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Items) != 3 {
		t.Fatalf("want 3 items, got %d", len(cat.Items))
	}
	// IDs are contiguous 1..n.
	for i, it := range cat.Items {
		if it.ID != i+1 {
			t.Errorf("item %d has ID %d", i, it.ID)
		}
	}
	// docs detector (registered before agentfiles) comes first.
	if cat.Items[0].Detector != "docs" {
		t.Errorf("expected docs first, got %q", cat.Items[0].Detector)
	}
	// README maps to entity "proj", which already exists in the hub.
	var readme *Candidate
	for i := range cat.Items {
		if strings.HasSuffix(cat.Items[i].AbsPath, "README.md") {
			readme = &cat.Items[i]
		}
	}
	if readme == nil || readme.SuggestedType != schema.TypeEntity || !readme.AlreadyImported {
		t.Errorf("README candidate wrong: %+v", readme)
	}
	// CLAUDE.md must be owned by agentfiles, not swallowed by the docs root scan.
	for i := range cat.Items {
		if strings.HasSuffix(cat.Items[i].AbsPath, "CLAUDE.md") && cat.Items[i].Detector != "agentfiles" {
			t.Errorf("CLAUDE.md owned by %q, want agentfiles", cat.Items[i].Detector)
		}
	}
}

func TestFilterAndRender(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "README.md"), "# Proj\n\nx")
	writeFile(t, filepath.Join(src, "docs", "auth.md"), "# Auth\n\nz")

	cat, err := Build(src, t.TempDir(), DefaultDetectors())
	if err != nil {
		t.Fatal(err)
	}
	only := cat.Filter("concept", "")
	if len(only.Items) != 1 || only.Items[0].ID != 1 {
		t.Fatalf("filter concept: %+v", only.Items)
	}
	var buf bytes.Buffer
	cat.Render(&buf, RenderOpts{})
	out := buf.String()
	if !strings.Contains(out, "[1]") || !strings.Contains(out, "auth") {
		t.Errorf("render missing rows:\n%s", out)
	}
}
