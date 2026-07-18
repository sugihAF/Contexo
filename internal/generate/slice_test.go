package generate

import (
	"strings"
	"testing"
)

func TestRenderCommunitySlice(t *testing.T) {
	g := loadFixture(t)
	members := g.Communities()[0]
	out := renderCommunitySlice(g, 0, members, "/proj")
	for _, want := range []string{
		"DefaultDetectors", "VERIFY", "AUTO-EXTRACTED",
		"Build()", "catalog.go:L28", "/proj", // absolute source ref
		"calls", "INFERRED", // relationships + confidence tags surfaced
	} {
		if !strings.Contains(out, want) {
			t.Errorf("community slice missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderGodNodeSlice(t *testing.T) {
	g := loadFixture(t)
	var build Node
	for _, n := range g.Nodes {
		if n.ID == "migrate_build" {
			build = n
		}
	}
	out := renderGodNodeSlice(g, build, "/proj")
	for _, want := range []string{"Build()", "catalog.go:L28", "VERIFY", "-->"} {
		if !strings.Contains(out, want) {
			t.Errorf("god-node slice missing %q\n---\n%s", want, out)
		}
	}
}
