package generate

import (
	"fmt"
	"path/filepath"
	"strings"
)

const sliceHeader = "AUTO-EXTRACTED from your code by Graphify. This is a scaffold, not ground truth — " +
	"VERIFY against the cited source files before writing the page. Edges tagged INFERRED are guesses; prefer EXTRACTED facts.\n"

// sourceRef renders an absolute source reference (projectRoot + file : location).
func sourceRef(projectRoot, file, loc string) string {
	p := filepath.ToSlash(filepath.Join(projectRoot, filepath.FromSlash(file)))
	if loc != "" {
		return p + ":" + loc
	}
	return p
}

func labelByID(g *Graph, id string) string {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n.Label
		}
	}
	return id
}

// renderCommunitySlice renders a subsystem (community) as a markdown scaffold:
// its member nodes with source refs, and the relationships among them.
func renderCommunitySlice(g *Graph, id int, members []Node, projectRoot string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (subsystem — community %d, %d nodes)\n\n", communityName(members, id), id, len(members))
	b.WriteString(sliceHeader + "\n")
	b.WriteString("## Members (source refs)\n")
	for _, n := range members {
		kind := "symbol"
		if isFileNode(n) {
			kind = "file"
		}
		fmt.Fprintf(&b, "- `%s` (%s) — %s\n", n.Label, kind, sourceRef(projectRoot, n.SourceFile, n.SourceLoc))
	}
	b.WriteString("\n## Relationships (within/from this subsystem)\n")
	for _, m := range members {
		for _, l := range g.NeighborsOf(m.ID) {
			if l.Source != m.ID {
				continue // list each edge once, from its source
			}
			fmt.Fprintf(&b, "- `%s` --%s--> `%s` [%s]\n", labelByID(g, l.Source), l.Relation, labelByID(g, l.Target), l.Confidence)
		}
	}
	return b.String()
}

// renderGodNodeSlice renders one high-connectivity symbol (a likely core
// component) as a markdown scaffold: its source ref and its typed connections.
func renderGodNodeSlice(g *Graph, n Node, projectRoot string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (component)\n\n", n.Label)
	b.WriteString(sliceHeader + "\n")
	fmt.Fprintf(&b, "Source: %s\n\n", sourceRef(projectRoot, n.SourceFile, n.SourceLoc))
	b.WriteString("## Connections\n")
	for _, l := range g.NeighborsOf(n.ID) {
		if l.Source == n.ID {
			fmt.Fprintf(&b, "- --%s--> `%s` [%s]\n", l.Relation, labelByID(g, l.Target), l.Confidence)
		} else {
			fmt.Fprintf(&b, "- `%s` --%s--> (this) [%s]\n", labelByID(g, l.Source), l.Relation, l.Confidence)
		}
	}
	return b.String()
}
