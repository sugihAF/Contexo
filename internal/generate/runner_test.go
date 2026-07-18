package generate

import (
	"os"
	"path/filepath"
	"testing"
)

// A stub Runner drops a graphify-out with our fixture graph.json + a report.
func stubRunner(t *testing.T) Runner {
	fixture, err := os.ReadFile(filepath.Join("testdata", "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	return func(projectRoot, cacheDir, backend string) (string, error) {
		out := filepath.Join(cacheDir, "graphify-out")
		if err := os.MkdirAll(out, 0o755); err != nil {
			return "", err
		}
		os.WriteFile(filepath.Join(out, "graph.json"), fixture, 0o644)
		os.WriteFile(filepath.Join(out, "GRAPH_REPORT.md"), []byte("# overview"), 0o644)
		return out, nil
	}
}

func TestDiscover(t *testing.T) {
	contexo := t.TempDir()
	cands, cacheDir, err := Discover("/proj", contexo, "", stubRunner(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) == 0 {
		t.Fatal("no candidates from Discover")
	}
	if cacheDir != filepath.Join(contexo, ".generate-cache") {
		t.Errorf("cacheDir = %q", cacheDir)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "slices")); err != nil {
		t.Errorf("slices dir not created: %v", err)
	}
}

func TestDiscoverDegradesOnBadGraph(t *testing.T) {
	contexo := t.TempDir()
	bad := func(projectRoot, cacheDir, backend string) (string, error) {
		out := filepath.Join(cacheDir, "graphify-out")
		os.MkdirAll(out, 0o755)
		os.WriteFile(filepath.Join(out, "graph.json"), []byte("{bad"), 0o644)
		os.WriteFile(filepath.Join(out, "GRAPH_REPORT.md"), []byte("# overview"), 0o644)
		return out, nil
	}
	cands, _, err := Discover("/proj", contexo, "", bad)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("bad graph should degrade to 1 report candidate, got %d", len(cands))
	}
}
