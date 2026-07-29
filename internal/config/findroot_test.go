package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindProjectRoot(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	sub := filepath.Join(proj, "a", "b")
	if err := os.MkdirAll(filepath.Join(proj, ContexoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// At the project root: returns it unchanged.
	if got := FindProjectRoot(proj); got != proj {
		t.Errorf("at project root: got %q want %q", got, proj)
	}
	// From a nested subdirectory: walks up to the project root.
	if got := FindProjectRoot(sub); got != proj {
		t.Errorf("from subdir: got %q want %q", got, proj)
	}
	// No .contexo anywhere above: returns start unchanged.
	noProj := t.TempDir()
	if got := FindProjectRoot(noProj); got != noProj {
		t.Errorf("no project: got %q want %q", got, noProj)
	}
}
