package gitstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestValidatePagePathRejectsTraversal locks in the path-traversal / git-arg
// guard. Each "bad" input is a vector confirmed during the security assessment
// (cross-tenant overwrite, .git tamper, contexo.db destruction, flag injection).
func TestValidatePagePathRejectsTraversal(t *testing.T) {
	bad := []string{
		"",
		"../escape.md",
		"../../x.md",
		"a/../../b.md",
		"wiki/../../../x.md",
		"/etc/passwd",
		`..\..\x.md`,
		`wiki\..\..\x.md`,
		"-rf",
		"--output=y",
		".git/hooks/pre-commit",
		"../repoB/.git/config",
		"../contexo.db",
		"wiki//foo.md",
		"./foo.md",
		"wiki/./foo.md",
		"wiki/x\x00.md", // NUL byte
		"wiki/x\tfoo.md", // control byte (tab)
	}
	for _, p := range bad {
		if err := validatePagePath(p); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("validatePagePath(%q) = %v, want ErrUnsafePath", p, err)
		}
	}
	good := []string{
		"wiki/concepts/foo.md",
		"raw/sessions/2026-06-13-topic.md",
		"wiki/entities/contexo.md",
		"index.md",
		"a/b/c/d.md",
	}
	for _, p := range good {
		if err := validatePagePath(p); err != nil {
			t.Errorf("validatePagePath(%q) = %v, want nil", p, err)
		}
	}
}

// TestWriteRejectsTraversalEndToEnd proves the F1 fix at the gitstore boundary:
// a push whose file path escapes the repo dir must be rejected AND must not
// touch the would-be victim file on disk.
func TestWriteRejectsTraversalEndToEnd(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init("repoA"); err != nil {
		t.Fatal(err)
	}
	// Sentinel "victim" one level above repoA (sibling), mimicking another
	// tenant's data or contexo.db.
	victim := filepath.Join(root, "victim.txt")
	if err := os.WriteFile(victim, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.Write("repoA", "../victim.txt", []byte("PWNED"), "a", "a@x", "m", ""); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Write traversal err = %v, want ErrUnsafePath", err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "ORIGINAL" {
		t.Fatalf("victim file was modified to %q — traversal NOT blocked", got)
	}

	// A legitimate page still writes and commits.
	if _, _, err := s.Write("repoA", "wiki/concepts/ok.md", []byte("hello"), "a", "a@x", "m", ""); err != nil {
		t.Fatalf("legit Write failed: %v", err)
	}
}

// TestArgInjectionBlocked proves the F2 fix: a non-hex since/from/to ref (e.g.
// --output=PATH) is rejected before reaching git, so no file is written.
func TestArgInjectionBlocked(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root)
	if err := s.Init("repoA"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Write("repoA", "wiki/concepts/ok.md", []byte("hi"), "a", "a@x", "m", ""); err != nil {
		t.Fatal(err)
	}

	evil := filepath.Join(root, "evil.txt")
	if _, _, err := s.ChangedSince("repoA", "--output="+evil); !errors.Is(err, ErrUnknownSHA) {
		t.Fatalf("ChangedSince arg-injection err = %v, want ErrUnknownSHA", err)
	}
	if _, statErr := os.Stat(evil); statErr == nil {
		t.Fatal("evil.txt was created — git arg injection NOT blocked")
	}

	if _, err := s.ResolveParentSHAForPath("repoA", "wiki/concepts/ok.md", "--all"); !errors.Is(err, ErrUnknownSHA) {
		t.Fatalf("ResolveParentSHAForPath flag err = %v, want ErrUnknownSHA", err)
	}
}
