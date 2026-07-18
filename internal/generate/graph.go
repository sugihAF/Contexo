// Package generate builds a seed Contexo knowledge base from a codebase by
// running the external Graphify CLI, parsing its graph.json, and mapping the
// code graph into migrate Candidates the agent then distills into pages.
package generate

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Node is one graph.json node (a file or a code symbol).
type Node struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Community     int    `json:"community"`
	CommunityName string `json:"community_name"`
	SourceFile    string `json:"source_file"`
	SourceLoc     string `json:"source_location"`
	FileType      string `json:"file_type"`
	NormLabel     string `json:"norm_label"`
}

// Link is one graph.json edge.
type Link struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Relation   string  `json:"relation"`
	Confidence string  `json:"confidence"`
	SourceFile string  `json:"source_file"`
	SourceLoc  string  `json:"source_location"`
	Weight     float64 `json:"weight"`
}

// Graph is Graphify's networkx node-link output (only the parts we use).
type Graph struct {
	Nodes []Node `json:"nodes"`
	Links []Link `json:"links"`
}

// ParseGraph decodes a graph.json. Errors on malformed JSON or an empty node
// set (callers degrade to the report-only path on error).
func ParseGraph(data []byte) (*Graph, error) {
	var g Graph
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("generate: parse graph.json: %w", err)
	}
	if len(g.Nodes) == 0 {
		return nil, fmt.Errorf("generate: graph.json has no nodes")
	}
	return &g, nil
}

// isFileNode reports whether a node represents a source file (label equals the
// base name of its source_file) rather than a code symbol.
func isFileNode(n Node) bool {
	return n.SourceFile != "" && strings.EqualFold(n.Label, filepath.Base(n.SourceFile))
}

func (g *Graph) degrees() map[string]int {
	d := make(map[string]int, len(g.Nodes))
	for _, l := range g.Links {
		d[l.Source]++
		d[l.Target]++
	}
	return d
}

// Communities groups nodes by their community id.
func (g *Graph) Communities() map[int][]Node {
	m := map[int][]Node{}
	for _, n := range g.Nodes {
		m[n.Community] = append(m[n.Community], n)
	}
	return m
}

// GodNodes returns the topM most-connected SYMBOL nodes (files excluded),
// by degree descending, id ascending as a deterministic tiebreak.
func (g *Graph) GodNodes(topM int) []Node {
	deg := g.degrees()
	var syms []Node
	for _, n := range g.Nodes {
		if !isFileNode(n) {
			syms = append(syms, n)
		}
	}
	sort.SliceStable(syms, func(i, j int) bool {
		if di, dj := deg[syms[i].ID], deg[syms[j].ID]; di != dj {
			return di > dj
		}
		return syms[i].ID < syms[j].ID
	})
	if topM > 0 && len(syms) > topM {
		syms = syms[:topM]
	}
	return syms
}

// NeighborsOf returns every link touching id (as source or target).
func (g *Graph) NeighborsOf(id string) []Link {
	var out []Link
	for _, l := range g.Links {
		if l.Source == id || l.Target == id {
			out = append(out, l)
		}
	}
	return out
}

// communityName returns a human name for a community: the first non-empty
// community_name among members, else "community <id>".
func communityName(members []Node, id int) string {
	for _, n := range members {
		if n.CommunityName != "" {
			return n.CommunityName
		}
	}
	return fmt.Sprintf("community %d", id)
}
