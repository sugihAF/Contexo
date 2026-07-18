package generate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
)

func TestBuildCandidates(t *testing.T) {
	g := loadFixture(t)
	cache := t.TempDir()
	outDir := t.TempDir()
	// a GRAPH_REPORT.md present → an overview candidate
	if err := os.WriteFile(filepath.Join(outDir, "GRAPH_REPORT.md"), []byte("# Graph Report\n\noverview"), 0o644); err != nil {
		t.Fatal(err)
	}

	cands, err := BuildCandidates(g, "/proj", outDir, cache, DefaultCaps())
	if err != nil {
		t.Fatal(err)
	}
	var haveConcept, haveEntity, haveAnalysis bool
	for _, c := range cands {
		switch c.SuggestedType {
		case schema.TypeConcept:
			haveConcept = true // communities
		case schema.TypeEntity:
			haveEntity = true // god nodes
		case schema.TypeAnalysis:
			haveAnalysis = true // GRAPH_REPORT.md
		}
		if c.Detector != "graphify" || c.AbsPath == "" {
			t.Errorf("bad candidate: %+v", c)
		}
	}
	if !haveConcept || !haveEntity || !haveAnalysis {
		t.Fatalf("missing a type: concept=%v entity=%v analysis=%v", haveConcept, haveEntity, haveAnalysis)
	}
	// slice files were materialized under cache/slices/
	entries, _ := os.ReadDir(filepath.Join(cache, "slices"))
	if len(entries) == 0 {
		t.Error("no slice files written")
	}
}

func TestBuildCandidatesGodNodeSlugCollision(t *testing.T) {
	// Two DIFFERENT god nodes with the SAME label ("Build()") in different files
	// must produce two DISTINCT slice files + candidates (no last-write-wins).
	g := &Graph{
		Nodes: []Node{
			{ID: "pkga_build", Label: "Build()", SourceFile: "a/build.go", SourceLoc: "L1", Community: 0, CommunityName: "A"},
			{ID: "pkgb_build", Label: "Build()", SourceFile: "b/build.go", SourceLoc: "L1", Community: 1, CommunityName: "B"},
			{ID: "pkga_x", Label: "X()", SourceFile: "a/build.go", SourceLoc: "L5", Community: 0, CommunityName: "A"},
			{ID: "pkgb_y", Label: "Y()", SourceFile: "b/build.go", SourceLoc: "L5", Community: 1, CommunityName: "B"},
		},
		Links: []Link{
			{Source: "pkga_build", Target: "pkga_x", Relation: "calls", Confidence: "EXTRACTED"},
			{Source: "pkgb_build", Target: "pkgb_y", Relation: "calls", Confidence: "EXTRACTED"},
		},
	}
	cache := t.TempDir()
	cands, err := BuildCandidates(g, "/proj", t.TempDir(), cache, DefaultCaps())
	if err != nil {
		t.Fatal(err)
	}
	// Collect the AbsPaths of the two "Build()" entity candidates.
	var buildPaths []string
	for _, c := range cands {
		if c.Title == "Build()" {
			buildPaths = append(buildPaths, c.AbsPath)
		}
	}
	if len(buildPaths) != 2 {
		t.Fatalf("want 2 Build() candidates, got %d", len(buildPaths))
	}
	if buildPaths[0] == buildPaths[1] {
		t.Errorf("the two Build() god-nodes collided onto one slice file: %q", buildPaths[0])
	}
}

func TestBuildCandidatesDegradesToReportOnly(t *testing.T) {
	cache := t.TempDir()
	outDir := t.TempDir()
	os.WriteFile(filepath.Join(outDir, "GRAPH_REPORT.md"), []byte("# overview"), 0o644)
	// g == nil (parse failed) → only the report candidate
	cands, err := BuildCandidates(nil, "/proj", outDir, cache, DefaultCaps())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].SuggestedType != schema.TypeAnalysis {
		t.Fatalf("degradation should yield 1 analysis candidate, got %+v", cands)
	}
}
