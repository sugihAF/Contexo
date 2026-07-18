// Package generate builds a seed Contexo knowledge base from a codebase by
// running the external Graphify CLI, parsing its graph.json, and mapping the
// code graph into migrate Candidates the agent then distills into pages.
package generate

import (
	"encoding/json"
	"fmt"
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
