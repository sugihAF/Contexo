package migrate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
)

func TestManifestRoundTripAndClear(t *testing.T) {
	hub := t.TempDir()

	if m, err := LoadManifest(hub); err != nil || m != nil {
		t.Fatalf("absent manifest: got %v, %v", m, err)
	}

	cache := filepath.Join(hub, ".migrate-cache", "abc")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	in := &Manifest{
		Version: 1, Created: "2026-07-05T00:00:00Z", SourceRoot: "/proj",
		ExternalCache: cache, Label: "llm-wiki",
		Items: []ManifestItem{{AbsPath: "/proj/a.md", RelPath: "a.md", Detector: "docs", SuggestedType: schema.TypeConcept, SuggestedSlug: "a"}},
	}
	if err := SaveManifest(hub, in); err != nil {
		t.Fatal(err)
	}
	got, err := LoadManifest(hub)
	if err != nil || got == nil {
		t.Fatalf("load: %v %v", got, err)
	}
	if got.Label != "llm-wiki" || len(got.Items) != 1 || got.Items[0].SuggestedSlug != "a" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	if err := ClearManifest(hub); err != nil {
		t.Fatal(err)
	}
	if fileExists(ManifestPath(hub)) {
		t.Errorf("manifest not removed")
	}
	if isDir(cache) {
		t.Errorf("external cache not removed")
	}
}
