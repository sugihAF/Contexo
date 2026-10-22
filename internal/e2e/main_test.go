// Package e2e is the cross-command end-to-end suite of the ctx <-> extension
// contract (v1 plus the plan's Contract errata). It builds the real ctx
// binary once, drives it against in-process testkit servers with a fresh
// environment, an isolated HOME and --root, and asserts the stdout
// documents, the one-line stderr envelope and the exit codes. It also writes
// and checks the redacted golden fixtures under testdata/contract/.
//
// The package holds only _test.go files. Knobs:
//
//	CONTEXO_E2E_CTX=<abs path>  run against a prebuilt ctx instead of building one
//	CONTEXO_E2E_STRICT=1        known deviations fail instead of skipping
//	-update                     rewrite testdata/contract (TestGolden only)
package e2e

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	envPrebuiltCtx = "CONTEXO_E2E_CTX"
	envStrict      = "CONTEXO_E2E_STRICT"
	// envHelper switches the test binary into a helper process
	// (TestHarness_EnvIsolation). It is set only on that child.
	envHelper = "E2E_HELPER_PROCESS"
)

var (
	// ctxBin is the absolute path of the ctx binary under test.
	ctxBin string
	// ctxPrebuilt is true when ctxBin came from CONTEXO_E2E_CTX.
	ctxPrebuilt bool
	// ctxSkip, when set, is why tests that need ctx are skipped.
	ctxSkip string
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(envHelper); mode != "" {
		os.Exit(runHelper(mode))
	}
	flag.Parse()
	os.Exit(e2eMain(m))
}

func e2eMain(m *testing.M) int {
	if testing.Short() {
		ctxSkip = "internal/e2e builds and runs the real ctx binary; skipped under -short"
		fmt.Println(ctxSkip)
		return m.Run()
	}
	if p := os.Getenv(envPrebuiltCtx); p != "" {
		if !filepath.IsAbs(p) {
			fmt.Fprintf(os.Stderr, "e2e: %s=%q must be an absolute path\n", envPrebuiltCtx, p)
			return 2
		}
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			fmt.Fprintf(os.Stderr, "e2e: %s=%q is not a file: %v\n", envPrebuiltCtx, p, err)
			return 2
		}
		ctxBin, ctxPrebuilt = p, true
		return m.Run()
	}
	dir, err := os.MkdirTemp("", "contexo-e2e-ctx-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: temp dir: %v\n", err)
		return 2
	}
	defer os.RemoveAll(dir)
	bin, err := buildCtx(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: %v\n", err)
		return 2
	}
	ctxBin = bin
	return m.Run()
}

// buildCtx builds ./cmd/ctx into dir with CGO_ENABLED=0. The build inherits
// the developer environment (warm build cache); only the ctx runs get a
// scratch environment.
func buildCtx(dir string) (string, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("go toolchain not on PATH: %w", err)
	}
	bin := filepath.Join(dir, "ctx")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command(goBin, "build", "-o", bin, "github.com/sugihAF/contexo/cmd/ctx")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./cmd/ctx: %v\n%s", err, out)
	}
	return bin, nil
}

// requireCtx returns the ctx binary, skipping under -short.
func requireCtx(t testing.TB) string {
	t.Helper()
	if ctxSkip != "" {
		t.Skip(ctxSkip)
	}
	if ctxBin == "" {
		t.Fatal("e2e: no ctx binary (TestMain did not run?)")
	}
	return ctxBin
}

// runHelper is the helper-process body. "printenv" prints the environment,
// one KEY=VALUE per line, sorted.
func runHelper(mode string) int {
	switch mode {
	case "printenv":
		env := os.Environ()
		sortStrings(env)
		fmt.Print(strings.Join(env, "\n") + "\n")
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
	return 2
}
