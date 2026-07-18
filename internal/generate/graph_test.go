package generate

import (
	"os"
	"path/filepath"
	"testing"
)

func loadFixture(t *testing.T) *Graph {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := ParseGraph(data)
	if err != nil {
		t.Fatalf("ParseGraph: %v", err)
	}
	return g
}

func TestParseGraph(t *testing.T) {
	g := loadFixture(t)
	if len(g.Nodes) != 8 || len(g.Links) != 9 {
		t.Fatalf("want 8 nodes / 9 links, got %d / %d", len(g.Nodes), len(g.Links))
	}
	var build *Node
	for i := range g.Nodes {
		if g.Nodes[i].ID == "migrate_build" {
			build = &g.Nodes[i]
		}
	}
	if build == nil || build.Label != "Build()" || build.Community != 0 || build.CommunityName != "DefaultDetectors" || build.SourceFile != "catalog.go" || build.SourceLoc != "L28" {
		t.Errorf("node fields wrong: %+v", build)
	}
	if g.Links[0].Source != "catalog" || g.Links[0].Relation != "contains" || g.Links[0].Confidence != "EXTRACTED" {
		t.Errorf("link fields wrong: %+v", g.Links[0])
	}
}

func TestParseGraphErrors(t *testing.T) {
	if _, err := ParseGraph([]byte("not json")); err == nil {
		t.Error("want error on bad json")
	}
	if _, err := ParseGraph([]byte(`{"nodes":[],"links":[]}`)); err == nil {
		t.Error("want error on empty nodes")
	}
}
