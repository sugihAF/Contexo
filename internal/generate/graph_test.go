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

func TestCommunitiesAndGodNodes(t *testing.T) {
	g := loadFixture(t)
	comms := g.Communities()
	if len(comms) != 2 || len(comms[0]) != 4 || len(comms[1]) != 4 {
		t.Fatalf("communities: %v", map[int]int{0: len(comms[0]), 1: len(comms[1])})
	}
	// God nodes are SYMBOL nodes only (files excluded), ranked by degree.
	gods := g.GodNodes(3)
	if len(gods) != 3 {
		t.Fatalf("want 3 god nodes, got %d", len(gods))
	}
	for _, n := range gods {
		if isFileNode(n) {
			t.Errorf("god node %q is a file node", n.Label)
		}
	}
	// migrate_build (contains<-, calls->, references->) and migrate_catalog and
	// migrate_defaultdetectors are the most-connected symbols; top god node is a symbol.
	if isFileNode(gods[0]) {
		t.Errorf("top god node should be a symbol: %+v", gods[0])
	}
}

func TestIsFileNodeAndNeighbors(t *testing.T) {
	g := loadFixture(t)
	byID := map[string]Node{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	if !isFileNode(byID["catalog"]) { // label "catalog.go" == base(source_file)
		t.Error("catalog.go should be a file node")
	}
	if isFileNode(byID["migrate_build"]) { // label "Build()" != "catalog.go"
		t.Error("Build() should be a symbol node")
	}
	if len(g.NeighborsOf("migrate_build")) == 0 {
		t.Error("migrate_build should have neighbors")
	}
}
