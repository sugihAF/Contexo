package syncstate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// wp04cmpWantPathError asserts err is a *PathError for path with the
// invalid_path contract code and an empty, non-nil advice list.
func wp04cmpWantPathError(t *testing.T, err error, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%q: want *PathError, got nil", path)
	}
	var pe *PathError
	if !errors.As(err, &pe) {
		t.Fatalf("%q: want *PathError, got %T (%v)", path, err, err)
	}
	if pe.ErrorCode() != "invalid_path" {
		t.Errorf("%q: ErrorCode = %q, want invalid_path", path, pe.ErrorCode())
	}
	if adv := pe.ErrorAdvice(); adv == nil || len(adv) != 0 {
		t.Errorf("%q: ErrorAdvice = %#v, want non-nil empty", path, adv)
	}
	if pe.Path != path {
		t.Errorf("PathError.Path = %q, want %q", pe.Path, path)
	}
	if pe.Reason == "" {
		t.Errorf("%q: PathError.Reason is empty", path)
	}
}

func TestWP04ValidateRelPathTable(t *testing.T) {
	accept := []string{
		"wiki/concepts/x.md",
		"raw/sessions/2026-x.md",
		"wiki/entities/a.b_c-d.md",
		"wiki/concepts/a b.md",
		"wiki/concepts/über.md",
		"index.md", // a valid path; callers exclude the root index.md/tags.md
	}
	for _, rel := range accept {
		if err := ValidateRelPath(rel); err != nil {
			t.Errorf("ValidateRelPath(%q) = %v, want nil", rel, err)
		}
	}

	reject := []string{
		"",
		".sync/base/x.md",
		".sync/state.json",
		"credentials.json",
		"config.json",
		"wiki/./x.md",
		"./wiki/x.md",
		"wiki/x.md/",
		"/wiki/x.md",
		"wiki/.x.md",
		".md",
		"wiki/.md",
		"WIKI/x.MD", // the .md suffix is case-sensitive
		"wiki/x.mdx",
		"wiki/x",
		"x.txt",
		"-x.md",
		"wiki/-x.md",
		"-wiki/x.md",
		"..",
		"../x.md",
		"wiki/../x.md",
		"wiki//x.md",
		`wiki\x.md`,
		"C:/x.md",
		"wiki/a:b.md",
		"wiki/a\x00b.md",
		"wiki/a\x01b.md",
		"wiki/a\nb.md",
		"wiki/a\x7fb.md",
		"wiki/a\xffb.md", // not UTF-8
	}
	for _, rel := range reject {
		wp04cmpWantPathError(t, ValidateRelPath(rel), rel)
	}
}

func TestNormalizeRelPathForms(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	const want = "wiki/concepts/x.md"
	forms := []string{
		"wiki/concepts/x.md",
		".contexo/wiki/concepts/x.md",
		filepath.Join(hub, "wiki", "concepts", "x.md"),
		filepath.Join(hub, "wiki", "..", "wiki", "concepts", "x.md"),
		`wiki\concepts\x.md`,
		`.contexo\wiki\concepts\x.md`,
		"./wiki/concepts/x.md",
		`.\wiki\concepts\x.md`,
		"./.contexo/wiki/concepts/x.md",
	}
	for _, arg := range forms {
		got, err := NormalizeRelPath(root, hub, arg)
		if err != nil {
			t.Errorf("NormalizeRelPath(%q) error: %v", arg, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeRelPath(%q) = %q, want %q", arg, got, want)
		}
	}

	got, err := NormalizeRelPath(root, hub, filepath.Join(hub, "raw", "sessions", "s.md"))
	if err != nil || got != "raw/sessions/s.md" {
		t.Errorf("absolute raw/sessions path = %q, %v; want raw/sessions/s.md", got, err)
	}
}

func TestNormalizeRelPathRejects(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	args := []string{
		"../x.md",
		`..\x.md`,
		filepath.Join(root, "outside.md"),
		filepath.Join(filepath.Dir(root), "x.md"),
		filepath.Join(hub, "..", "x.md"),
		hub,
		filepath.Join(hub, ".sync", "base", "wiki", "concepts", "x.md"),
		".sync/state.json",
		".contexo/.sync/state.json",
		"credentials.json",
		".contexo/credentials.json",
		"config.json",
		"x.txt",
		"-x.md",
		"wiki/a\x01b.md",
		"",
		"wiki//x.md",
		"wiki/../x.md",
		".contexo/../x.md",
		"/wiki/x.md",
		".contexo/",
		".contexo",
		"./",
	}
	for _, arg := range args {
		_, err := NormalizeRelPath(root, hub, arg)
		wp04cmpWantPathError(t, err, arg)
	}
}

// A hub reached through a symlinked directory still accepts the absolute
// path of a page given through the resolved (or the linked) directory, even
// when the page file does not exist.
func TestWP04NormalizeRelPathSymlinkedHub(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(realDir, ".contexo", "wiki"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	const want = "wiki/concepts/x.md"
	cases := []struct{ name, root, hub, arg string }{
		{"hub_linked", link, filepath.Join(link, ".contexo"), filepath.Join(realDir, ".contexo", "wiki", "concepts", "x.md")},
		{"arg_linked", realDir, filepath.Join(realDir, ".contexo"), filepath.Join(link, ".contexo", "wiki", "concepts", "x.md")},
	}
	for _, c := range cases {
		got, err := NormalizeRelPath(c.root, c.hub, c.arg)
		if err != nil || got != want {
			t.Errorf("%s: NormalizeRelPath = %q, %v; want %q", c.name, got, err, want)
		}
	}

	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	arg := filepath.Join(outside, "wiki", "concepts", "x.md")
	_, err := NormalizeRelPath(link, filepath.Join(link, ".contexo"), arg)
	wp04cmpWantPathError(t, err, arg)
}
