package generate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setLookPath stubs the package's lookPath seam so only the named binaries are
// "on PATH". Restored via t.Cleanup.
func setLookPath(t *testing.T, present ...string) {
	t.Helper()
	prev := lookPath
	set := map[string]bool{}
	for _, p := range present {
		set[p] = true
	}
	lookPath = func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPath = prev })
}

func TestGraphifyInstalled(t *testing.T) {
	setLookPath(t, "graphify")
	if !GraphifyInstalled() {
		t.Fatal("expected installed when graphify on PATH")
	}
	setLookPath(t) // none present
	if GraphifyInstalled() {
		t.Fatal("expected not installed when graphify absent")
	}
}

func TestInstallGraphifyPrefersUv(t *testing.T) {
	setLookPath(t, "uv", "pip", "python") // uv should win the ordering
	var gotBin string
	var gotArgs []string
	prev := installRun
	installRun = func(bin string, args ...string) error {
		gotBin, gotArgs = bin, args
		return nil
	}
	t.Cleanup(func() { installRun = prev })

	method, err := InstallGraphify()
	if err != nil {
		t.Fatal(err)
	}
	if method != "uv" {
		t.Fatalf("method = %q, want uv", method)
	}
	if gotBin != "uv" || strings.Join(gotArgs, " ") != "tool install graphifyy" {
		t.Fatalf("ran %q %v, want uv tool install graphifyy", gotBin, gotArgs)
	}
}

func TestInstallGraphifySkipsMissingInstaller(t *testing.T) {
	setLookPath(t, "pipx") // uv absent, pipx present
	var gotBin string
	var gotArgs []string
	prev := installRun
	installRun = func(bin string, args ...string) error {
		gotBin, gotArgs = bin, args
		return nil
	}
	t.Cleanup(func() { installRun = prev })

	method, err := InstallGraphify()
	if err != nil {
		t.Fatal(err)
	}
	if method != "pipx" {
		t.Fatalf("method = %q, want pipx", method)
	}
	if gotBin != "pipx" || strings.Join(gotArgs, " ") != "install graphifyy" {
		t.Fatalf("ran %q %v, want pipx install graphifyy", gotBin, gotArgs)
	}
}

func TestInstallGraphifyNoInstaller(t *testing.T) {
	setLookPath(t) // nothing present
	called := false
	prev := installRun
	installRun = func(bin string, args ...string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { installRun = prev })

	if _, err := InstallGraphify(); err == nil {
		t.Fatal("expected error when no installer present")
	}
	if called {
		t.Fatal("installRun must not run when no installer found")
	}
}

// A stub Runner drops a graphify-out with our fixture graph.json + a report.
func stubRunner(t *testing.T) Runner {
	fixture, err := os.ReadFile(filepath.Join("testdata", "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	return func(projectRoot, cacheDir, backend string) (string, error) {
		out := filepath.Join(cacheDir, "graphify-out")
		if err := os.MkdirAll(out, 0o755); err != nil {
			return "", err
		}
		os.WriteFile(filepath.Join(out, "graph.json"), fixture, 0o644)
		os.WriteFile(filepath.Join(out, "GRAPH_REPORT.md"), []byte("# overview"), 0o644)
		return out, nil
	}
}

func TestDiscover(t *testing.T) {
	contexo := t.TempDir()
	cands, cacheDir, err := Discover("/proj", contexo, "", stubRunner(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) == 0 {
		t.Fatal("no candidates from Discover")
	}
	if cacheDir != filepath.Join(contexo, ".generate-cache") {
		t.Errorf("cacheDir = %q", cacheDir)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "slices")); err != nil {
		t.Errorf("slices dir not created: %v", err)
	}
}

func TestDiscoverDegradesOnBadGraph(t *testing.T) {
	contexo := t.TempDir()
	bad := func(projectRoot, cacheDir, backend string) (string, error) {
		out := filepath.Join(cacheDir, "graphify-out")
		os.MkdirAll(out, 0o755)
		os.WriteFile(filepath.Join(out, "graph.json"), []byte("{bad"), 0o644)
		os.WriteFile(filepath.Join(out, "GRAPH_REPORT.md"), []byte("# overview"), 0o644)
		return out, nil
	}
	cands, _, err := Discover("/proj", contexo, "", bad)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("bad graph should degrade to 1 report candidate, got %d", len(cands))
	}
}
