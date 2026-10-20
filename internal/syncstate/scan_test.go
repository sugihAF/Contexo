package syncstate

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// wp04cmpWrite writes data to hub/rel, creating parent directories.
func wp04cmpWrite(t *testing.T, hub, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(hub, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanLocalStates(t *testing.T) {
	hub := t.TempDir()
	good := wp04cmpPage("concept", "good", "# Good\n")
	bomPlain := wp04cmpPage("entity", "bom", "# Bom\n")
	bom := wp04cmpWithBOM(bomPlain)
	crlf := wp04cmpCRLF(string(wp04cmpPage("analysis", "crlf", "# CRLF\n")))
	source := wp04cmpPage("source", "s1", "session\n")
	broken := []byte("---\nslug: broken\nno closing delimiter\n")
	noAuthor := []byte("---\nschema: ctx.page.v1\nslug: noauthor\ntype: concept\n" +
		"created: 2026-01-02T00:00:00Z\n---\nbody\n")
	mis := wp04cmpPage("concept", "other", "# Mis\n")

	files := map[string][]byte{
		"wiki/concepts/good.md":     good,
		"wiki/concepts/broken.md":   broken,
		"wiki/concepts/noauthor.md": noAuthor,
		"wiki/concepts/mis.md":      mis,
		"wiki/entities/bom.md":      bom,
		"wiki/analyses/crlf.md":     crlf,
		"raw/sessions/s1.md":        source,
		"wiki/tags.md":              []byte("# Not the root tags.md\n"),
		// Excluded: root index/tags, dot-dirs, non-.md, temp names.
		"index.md":                             []byte("# Knowledge Index\n"),
		"tags.md":                              []byte("# Tags\n"),
		".sync/base/wiki/concepts/good.md":     good,
		".sync/conflicts/wiki/concepts/x.md":   wp04cmpPage("concept", "x", "theirs\n"),
		".sync/state.json":                     []byte("{}\n"),
		"wiki/concepts/notes.txt":              []byte("text\n"),
		"wiki/concepts/.good.md.tmp-123456":    good,
		".migrate-cache/wiki/concepts/good.md": good,
	}
	for rel, data := range files {
		wp04cmpWrite(t, hub, rel, data)
	}

	scan, err := ScanLocal(hub)
	if err != nil {
		t.Fatalf("ScanLocal: %v", err)
	}

	wantPaths := []string{
		"raw/sessions/s1.md",
		"wiki/analyses/crlf.md",
		"wiki/concepts/broken.md",
		"wiki/concepts/good.md",
		"wiki/concepts/mis.md",
		"wiki/concepts/noauthor.md",
		"wiki/entities/bom.md",
		"wiki/tags.md",
	}
	if !reflect.DeepEqual(scan.Paths, wantPaths) {
		t.Fatalf("Paths = %q\nwant  %q", scan.Paths, wantPaths)
	}
	if len(scan.Files) != len(wantPaths) {
		t.Fatalf("len(Files) = %d, want %d", len(scan.Files), len(wantPaths))
	}

	for _, rel := range wantPaths {
		f := scan.Files[rel]
		if f == nil {
			t.Fatalf("Files[%q] missing", rel)
		}
		if f.Path != rel {
			t.Errorf("Files[%q].Path = %q", rel, f.Path)
		}
		if !bytes.Equal(f.Data, files[rel]) {
			t.Errorf("%s: Data is not the verbatim file bytes", rel)
		}
	}

	for _, rel := range []string{"raw/sessions/s1.md", "wiki/analyses/crlf.md", "wiki/concepts/good.md", "wiki/entities/bom.md"} {
		f := scan.Files[rel]
		if f.Invalid != nil {
			t.Errorf("%s: want valid, got invalid %+v", rel, *f.Invalid)
			continue
		}
		if f.Page == nil {
			t.Errorf("%s: valid file has nil Page", rel)
		}
		if f.Hash != Hash(files[rel]) || !strings.HasPrefix(f.Hash, "sha256:") {
			t.Errorf("%s: Hash = %q, want %q", rel, f.Hash, Hash(files[rel]))
		}
	}
	if got, want := scan.Files["wiki/entities/bom.md"].Hash, Hash(bomPlain); got != want {
		t.Errorf("BOM page Hash = %q, want the hash without BOM %q", got, want)
	}
	if got, want := scan.Files["wiki/analyses/crlf.md"].Hash, Hash(wp04cmpPage("analysis", "crlf", "# CRLF\n")); got != want {
		t.Errorf("CRLF page Hash = %q, want the LF hash %q", got, want)
	}

	invalid := []struct {
		rel, stage, expected string
		page                 bool
	}{
		{"wiki/concepts/broken.md", StageParse, "", false},
		{"wiki/tags.md", StageParse, "", false},
		{"wiki/concepts/noauthor.md", StageValidate, "", true},
		{"wiki/concepts/mis.md", StagePathMismatch, "wiki/concepts/other.md", true},
	}
	for _, c := range invalid {
		f := scan.Files[c.rel]
		if f.Invalid == nil {
			t.Errorf("%s: want invalid stage %s, got valid", c.rel, c.stage)
			continue
		}
		if f.Invalid.Stage != c.stage {
			t.Errorf("%s: Stage = %q, want %q", c.rel, f.Invalid.Stage, c.stage)
		}
		if f.Invalid.Message == "" {
			t.Errorf("%s: empty Message", c.rel)
		}
		if f.Invalid.ExpectedPath != c.expected {
			t.Errorf("%s: ExpectedPath = %q, want %q", c.rel, f.Invalid.ExpectedPath, c.expected)
		}
		if f.Hash != "" {
			t.Errorf("%s: invalid file has Hash %q, want \"\"", c.rel, f.Hash)
		}
		if (f.Page != nil) != c.page {
			t.Errorf("%s: Page non-nil = %v, want %v", c.rel, f.Page != nil, c.page)
		}
	}
}

func TestWP04ScanLocalEmptyAndMissingHub(t *testing.T) {
	hub := t.TempDir()
	scan, err := ScanLocal(hub)
	if err != nil {
		t.Fatalf("ScanLocal(empty): %v", err)
	}
	if scan.Files == nil || len(scan.Files) != 0 || len(scan.Paths) != 0 {
		t.Errorf("empty hub: Files=%v Paths=%v, want empty non-nil Files", scan.Files, scan.Paths)
	}

	if _, err := ScanLocal(filepath.Join(hub, "missing")); err == nil {
		t.Error("ScanLocal(missing dir): want error, got nil")
	}
	file := filepath.Join(hub, "file.md")
	if err := os.WriteFile(file, wp04cmpPage("concept", "file", ""), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanLocal(file); err == nil {
		t.Error("ScanLocal(regular file): want error, got nil")
	}
}
