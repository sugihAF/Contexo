package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sugihAF/contexo/internal/migrate"
	"github.com/sugihAF/contexo/internal/schema"
)

// Caps bound how many communities / god nodes become candidates, so a large
// codebase doesn't explode into hundreds of pages.
type Caps struct{ Communities, GodNodes int }

func DefaultCaps() Caps { return Caps{Communities: 15, GodNodes: 15} }

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// BuildCandidates maps a parsed graph into migrate Candidates, materializing a
// markdown slice file per community/god-node under <cacheDir>/slices/. Order:
// overview, then communities (largest first), then god nodes. If g is nil it
// degrades to just the GRAPH_REPORT.md overview candidate.
func BuildCandidates(g *Graph, projectRoot, graphifyOutDir, cacheDir string, caps Caps) ([]migrate.Candidate, error) {
	var cands []migrate.Candidate

	if reportPath := filepath.Join(graphifyOutDir, "GRAPH_REPORT.md"); fileExists(reportPath) {
		cands = append(cands, migrate.Candidate{
			Detector: "graphify", SourceRoot: graphifyOutDir, AbsPath: reportPath,
			RelPath: "GRAPH_REPORT.md", Title: "Codebase overview",
			SuggestedType: schema.TypeAnalysis, SuggestedSlug: "codebase-overview",
			Preview: "Graphify overview: god nodes, subsystems, key connections",
		})
	}

	if g == nil {
		return cands, nil // degradation: report only
	}

	slicesDir := filepath.Join(cacheDir, "slices")
	if err := os.MkdirAll(slicesDir, 0o755); err != nil {
		return nil, fmt.Errorf("generate: mkdir slices: %w", err)
	}

	// Communities, largest first, capped.
	comms := g.Communities()
	ids := make([]int, 0, len(comms))
	for id := range comms {
		ids = append(ids, id)
	}
	sort.SliceStable(ids, func(i, j int) bool {
		if li, lj := len(comms[ids[i]]), len(comms[ids[j]]); li != lj {
			return li > lj
		}
		return ids[i] < ids[j]
	})
	if caps.Communities > 0 && len(ids) > caps.Communities {
		ids = ids[:caps.Communities]
	}
	for _, id := range ids {
		members := comms[id]
		name := communityName(members, id)
		slug := migrate.Slugify(name)
		content := renderCommunitySlice(g, id, members, projectRoot)
		rel := filepath.Join("slices", fmt.Sprintf("community-%s-%d.md", slug, id))
		if err := os.WriteFile(filepath.Join(cacheDir, rel), []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("generate: write slice: %w", err)
		}
		cands = append(cands, migrate.Candidate{
			Detector: "graphify", SourceRoot: cacheDir, AbsPath: filepath.Join(cacheDir, rel),
			RelPath: filepath.ToSlash(rel), Title: name,
			SuggestedType: schema.TypeConcept, SuggestedSlug: slug,
			EstTokens: (len(content) + 3) / 4,
			Preview:   fmt.Sprintf("Subsystem with %d nodes (hub: %s)", len(members), name),
		})
	}

	// God nodes → entity.
	for _, n := range g.GodNodes(caps.GodNodes) {
		slug := migrate.Slugify(n.Label)
		content := renderGodNodeSlice(g, n, projectRoot)
		rel := filepath.Join("slices", "component-"+migrate.Slugify(n.ID)+".md")
		if err := os.WriteFile(filepath.Join(cacheDir, rel), []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("generate: write slice: %w", err)
		}
		cands = append(cands, migrate.Candidate{
			Detector: "graphify", SourceRoot: cacheDir, AbsPath: filepath.Join(cacheDir, rel),
			RelPath: filepath.ToSlash(rel), Title: n.Label,
			SuggestedType: schema.TypeEntity, SuggestedSlug: slug,
			EstTokens: (len(content) + 3) / 4,
			Preview:   sourceRef(projectRoot, n.SourceFile, n.SourceLoc),
		})
	}
	return cands, nil
}
