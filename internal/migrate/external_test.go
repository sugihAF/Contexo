package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsGitURL(t *testing.T) {
	yes := []string{"https://github.com/a/b", "https://github.com/a/b.git", "git@github.com:a/b.git"}
	no := []string{"/home/me/wiki", "./docs", "C:\\proj\\kb", "relative/path"}
	for _, u := range yes {
		if !IsGitURL(u) {
			t.Errorf("IsGitURL(%q) = false, want true", u)
		}
	}
	for _, p := range no {
		if IsGitURL(p) {
			t.Errorf("IsGitURL(%q) = true, want false", p)
		}
	}
}

func TestResolveExternalLocalPath(t *testing.T) {
	dir := t.TempDir()
	scan, cache, err := ResolveExternal(dir, t.TempDir(), nil)
	if err != nil || scan != dir || cache != "" {
		t.Fatalf("local: %q %q %v", scan, cache, err)
	}
	if _, _, err := ResolveExternal(filepath.Join(dir, "nope"), t.TempDir(), nil); err == nil {
		t.Errorf("missing local path should error")
	}
}

func TestResolveExternalURLUsesClonerAndCache(t *testing.T) {
	hub := t.TempDir()
	var gotURL, gotDest string
	stub := func(url, dest string) error {
		gotURL, gotDest = url, dest
		return os.MkdirAll(dest, 0o755)
	}
	scan, cache, err := ResolveExternal("https://github.com/a/b.git", hub, stub)
	if err != nil {
		t.Fatal(err)
	}
	if gotURL != "https://github.com/a/b.git" {
		t.Errorf("cloner url = %q", gotURL)
	}
	if scan != cache || gotDest != scan {
		t.Errorf("scan/cache/dest mismatch: %q %q %q", scan, cache, gotDest)
	}
	if !strings.HasPrefix(cache, filepath.Join(hub, ".migrate-cache")) {
		t.Errorf("cache not under .migrate-cache: %q", cache)
	}
}
