// internal/migrate/detect_docs_test.go
package migrate

import (
	"path/filepath"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
)

func TestDocsDetector(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "README.md"), "# My Project\n\nWhat it is.")
	writeFile(t, filepath.Join(root, "CHANGELOG.md"), "# Changelog\n\n- v1")
	writeFile(t, filepath.Join(root, "docs", "guide.md"), "# Guide\n\nHow to.")
	writeFile(t, filepath.Join(root, "docs", "adr", "0001-use-go.md"), "# Use Go\n\nContext.")
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "readme.md"), "# noise")

	got, err := newDocsDetector().Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	typ := map[string]schema.PageType{}
	for _, c := range got {
		typ[filepath.Base(c.AbsPath)] = c.SuggestedType
	}
	if typ["README.md"] != schema.TypeEntity {
		t.Errorf("README should be entity, got %q", typ["README.md"])
	}
	if typ["CHANGELOG.md"] != schema.TypeSource {
		t.Errorf("CHANGELOG should be source, got %q", typ["CHANGELOG.md"])
	}
	if typ["guide.md"] != schema.TypeConcept {
		t.Errorf("guide should be concept, got %q", typ["guide.md"])
	}
	if typ["0001-use-go.md"] != schema.TypeAnalysis {
		t.Errorf("ADR should be analysis, got %q", typ["0001-use-go.md"])
	}
	if _, ok := typ["readme.md"]; ok {
		t.Errorf("node_modules content must be ignored")
	}
}
