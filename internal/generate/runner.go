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

// lookPath is the PATH-probe seam (default exec.LookPath); tests override it to
// simulate which binaries are present.
var lookPath = exec.LookPath

// GraphifyInstalled reports whether the `graphify` CLI is on PATH.
func GraphifyInstalled() bool {
	_, err := lookPath("graphify")
	return err == nil
}

// graphifyInstallers is the detection order. Each entry's bin is probed with
// lookPath; the first present one installs the fixed `graphifyy` package.
var graphifyInstallers = []struct {
	bin  string
	args []string
}{
	{"uv", []string{"tool", "install", "graphifyy"}},
	{"pipx", []string{"install", "graphifyy"}},
	{"pip", []string{"install", "graphifyy"}},
	{"pip3", []string{"install", "graphifyy"}},
	{"python", []string{"-m", "pip", "install", "graphifyy"}},
	{"python3", []string{"-m", "pip", "install", "graphifyy"}},
	{"py", []string{"-m", "pip", "install", "graphifyy"}},
}

// installRun is the install seam: default streams the installer's output to the
// user; tests override it to capture the argv without really installing.
// Both streams go to stderr on purpose — the MCP stdio transport speaks
// JSON-RPC over stdout, so installer noise there would corrupt the protocol;
// stderr still reaches the user's terminal on the CLI path (and keeps CLI
// stdout clean for piping).
var installRun = func(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

// InstallGraphify installs the `graphifyy` package with the first available
// installer (uv → pipx → pip → …) and returns which one it used. The package
// name is fixed and passed as an arg slice (no shell, no injection). It returns
// an error listing the manual commands when no installer is on PATH.
func InstallGraphify() (method string, err error) {
	for _, inst := range graphifyInstallers {
		if _, e := lookPath(inst.bin); e != nil {
			continue
		}
		if e := installRun(inst.bin, inst.args...); e != nil {
			return "", fmt.Errorf("`%s %s` failed: %w", inst.bin, strings.Join(inst.args, " "), e)
		}
		return inst.bin, nil
	}
	return "", fmt.Errorf("no Python installer found (need uv, pipx, or pip) — install Graphify yourself with `uv tool install graphifyy` (or `pipx install graphifyy`, or `pip install graphifyy`)")
}

// DefaultRunner shells out to the user-installed `graphify` CLI (keyless code
// extraction), then relocates <projectRoot>/graphify-out into the cache so the
// repo root is left clean.
func DefaultRunner(projectRoot, cacheDir, backend string) (string, error) {
	if _, err := lookPath("graphify"); err != nil {
		return "", fmt.Errorf("ctx generate needs Graphify — install it with `uv tool install graphifyy` (or pipx/pip install graphifyy)")
	}
	// --code-only keeps extraction keyless (local AST): a corpus with docs/images
	// otherwise makes `graphify extract` demand an LLM API key and fail. generate
	// only needs the code graph — docs are ctx migrate's job, not generate's.
	if out, err := runGraphify([]string{"extract", projectRoot, "--code-only"}); err != nil {
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

// runGraphify invokes the graphify CLI (seam: tests override it to assert argv).
var runGraphify = func(args []string) (string, error) {
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
