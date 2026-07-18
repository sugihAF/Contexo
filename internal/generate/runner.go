package generate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sugihAF/contexo/internal/migrate"
)

// Runner runs Graphify for projectRoot and returns the directory containing the
// produced graph.json / GRAPH_REPORT.md. Seam for testing.
type Runner func(projectRoot, cacheDir, backend string) (graphifyOutDir string, err error)

// CacheDir is where generate keeps Graphify artifacts + slices (a dot-dir, so
// migrate's Store.Walk exclusion keeps it out of pushes).
func CacheDir(contexoDir string) string {
	return filepath.Join(contexoDir, ".generate-cache")
}

// DefaultRunner shells out to the user-installed `graphify` CLI (keyless code
// extraction), then relocates <projectRoot>/graphify-out into the cache so the
// repo root is left clean.
func DefaultRunner(projectRoot, cacheDir, backend string) (string, error) {
	if _, err := exec.LookPath("graphify"); err != nil {
		return "", fmt.Errorf("ctx generate needs Graphify — install it with `uv tool install graphifyy` (or pipx/pip install graphifyy)")
	}
	if out, err := runGraphify([]string{"extract", projectRoot}); err != nil {
		return "", fmt.Errorf("graphify extract failed: %v: %s", err, strings.TrimSpace(out))
	}
	// cluster-only adds community_name (hub heuristic, keyless) + GRAPH_REPORT.md;
	// best-effort — extraction already gave us a usable graph.json.
	clusterArgs := []string{"cluster-only", projectRoot, "--no-viz"}
	if backend != "" {
		clusterArgs = append(clusterArgs, "--backend="+backend)
	}
	_, _ = runGraphify(clusterArgs)

	srcOut := filepath.Join(projectRoot, "graphify-out")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("generate: mkdir cache: %w", err)
	}
	dstOut := filepath.Join(cacheDir, "graphify-out")
	_ = os.RemoveAll(dstOut)
	if err := os.Rename(srcOut, dstOut); err != nil {
		// cross-volume/busy: fall back to reading graphify-out in place.
		return srcOut, nil
	}
	return dstOut, nil
}

func runGraphify(args []string) (string, error) {
	out, err := exec.Command("graphify", args...).CombinedOutput()
	return string(out), err
}

// Discover runs Graphify (via runner) and maps its output into candidates.
// A missing/unparseable graph.json degrades to the report-only candidate.
func Discover(projectRoot, contexoDir, backend string, runner Runner) ([]migrate.Candidate, string, error) {
	cacheDir := CacheDir(contexoDir)
	outDir, err := runner(projectRoot, cacheDir, backend)
	if err != nil {
		return nil, "", err
	}
	var g *Graph
	if data, e := os.ReadFile(filepath.Join(outDir, "graph.json")); e == nil {
		g, _ = ParseGraph(data) // nil on parse failure → degradation
	}
	cands, err := BuildCandidates(g, projectRoot, outDir, cacheDir, DefaultCaps())
	if err != nil {
		return nil, "", err
	}
	return cands, cacheDir, nil
}
