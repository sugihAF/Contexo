package gitstore

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

func TestInitIdempotent(t *testing.T) {
	s := newStore(t)
	if err := s.Init("chompchat"); err != nil {
		t.Fatalf("init 1: %v", err)
	}
	if err := s.Init("chompchat"); err != nil {
		t.Fatalf("init 2 (should be idempotent): %v", err)
	}
	if !s.Exists("chompchat") {
		t.Errorf("Exists returned false after Init")
	}
}

func TestWriteAndRead(t *testing.T) {
	s := newStore(t)
	if err := s.Init("chompchat"); err != nil {
		t.Fatalf("init: %v", err)
	}

	sha1, conflict, err := s.Write("chompchat", "wiki/concepts/stripe-subscription.md",
		[]byte("hello world\n"),
		"sugihAF", "sugih@example.com",
		"add stripe page", "")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if conflict != nil {
		t.Fatalf("unexpected conflict: %+v", conflict)
	}
	if sha1 == "" {
		t.Fatalf("expected non-empty sha")
	}

	content, sha, err := s.Read("chompchat", "wiki/concepts/stripe-subscription.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != "hello world\n" {
		t.Errorf("content mismatch: %q", string(content))
	}
	if sha != sha1 {
		t.Errorf("sha mismatch: read=%s write=%s", sha, sha1)
	}
}

func TestParentSHAConflict(t *testing.T) {
	s := newStore(t)
	s.Init("repo")

	sha1, _, _ := s.Write("repo", "page.md", []byte("v1\n"), "A", "a@a", "v1", "")
	if sha1 == "" {
		t.Fatalf("write 1 failed")
	}

	// Second write with correct parent should succeed
	sha2, conflict, err := s.Write("repo", "page.md", []byte("v2\n"), "A", "a@a", "v2", sha1)
	if err != nil {
		t.Fatalf("write 2: %v", err)
	}
	if conflict != nil {
		t.Fatalf("unexpected conflict on correct parent: %+v", conflict)
	}
	if sha2 == sha1 {
		t.Errorf("sha2 should differ from sha1")
	}

	// Third write claiming sha1 as parent — but real parent is now sha2 — should conflict
	_, conflict, err = s.Write("repo", "page.md", []byte("v3\n"), "B", "b@b", "v3", sha1)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
	if conflict == nil || conflict.CurrentSHA != sha2 {
		t.Errorf("conflict.CurrentSHA: got %q, want %q", conflict.CurrentSHA, sha2)
	}
	if string(conflict.CurrentContent) != "v2\n" {
		t.Errorf("conflict.CurrentContent: got %q", string(conflict.CurrentContent))
	}
}

func TestLogAndLogPath(t *testing.T) {
	s := newStore(t)
	s.Init("repo")

	s.Write("repo", "a.md", []byte("a1\n"), "Alice", "a@a", "a v1", "")
	sha2, _, _ := s.Write("repo", "b.md", []byte("b1\n"), "Bob", "b@b", "b v1", "")
	s.Write("repo", "a.md", []byte("a2\n"), "Alice", "a@a", "a v2", "")

	log, err := s.Log("repo", 10)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(log) != 3 {
		t.Errorf("expected 3 commits, got %d", len(log))
	}
	if log[0].Message != "a v2" {
		t.Errorf("log[0] should be most recent ('a v2'), got %q", log[0].Message)
	}

	logA, err := s.LogPath("repo", "a.md", 10)
	if err != nil {
		t.Fatalf("logPath a.md: %v", err)
	}
	if len(logA) != 2 {
		t.Errorf("a.md should have 2 commits, got %d", len(logA))
	}

	logB, _ := s.LogPath("repo", "b.md", 10)
	if len(logB) != 1 || logB[0].SHA != sha2 {
		t.Errorf("b.md log: %+v", logB)
	}
}

func TestChangedSince(t *testing.T) {
	s := newStore(t)
	s.Init("repo")

	sha1, _, _ := s.Write("repo", "a.md", []byte("a1\n"), "A", "a@a", "msg", "")
	s.Write("repo", "b.md", []byte("b\n"), "A", "a@a", "msg", "")
	sha3, _, _ := s.Write("repo", "c.md", []byte("c\n"), "A", "a@a", "msg", "")

	// All files since empty
	files, head, err := s.ChangedSince("repo", "")
	if err != nil {
		t.Fatalf("changed empty: %v", err)
	}
	if len(files) != 3 || head != sha3 {
		t.Errorf("all: files=%v head=%s want sha3=%s", files, head, sha3)
	}

	// Since sha1: should include b.md and c.md but NOT a.md
	files, head, err = s.ChangedSince("repo", sha1)
	if err != nil {
		t.Fatalf("changed since sha1: %v", err)
	}
	if head != sha3 {
		t.Errorf("head mismatch: %s vs %s", head, sha3)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files since sha1, got %v", files)
	}

	// Since head: empty
	files, _, _ = s.ChangedSince("repo", sha3)
	if len(files) != 0 {
		t.Errorf("expected no changes since head, got %v", files)
	}
}

func TestReadNotFound(t *testing.T) {
	s := newStore(t)
	s.Init("repo")

	_, _, err := s.Read("repo", "missing.md")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

func TestUninitializedRepoRejects(t *testing.T) {
	s := newStore(t)
	if s.Exists("not-init") {
		t.Errorf("Exists true for non-init")
	}
	_, _, err := s.Write("not-init", "x.md", []byte("y"), "a", "a@a", "m", "")
	if !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("expected ErrRepoNotFound, got %v", err)
	}
}

func TestListRepos(t *testing.T) {
	s := newStore(t)
	// Empty
	if r, _ := s.ListRepos(); len(r) != 0 {
		t.Errorf("empty: got %v", r)
	}
	// Init three; ListRepos sorts
	for _, id := range []string{"zeta", "alpha", "mike"} {
		if err := s.Init(id); err != nil {
			t.Fatalf("init %s: %v", id, err)
		}
	}
	got, err := s.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	want := []string{"alpha", "mike", "zeta"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestListPages(t *testing.T) {
	s := newStore(t)
	s.Init("repo")

	if pages, _ := s.ListPages("repo"); len(pages) != 0 {
		t.Errorf("empty repo: %v", pages)
	}

	sha1, _, _ := s.Write("repo", "a.md", []byte("a1"), "Alice", "a@a", "msg-a-1", "")
	s.Write("repo", "b.md", []byte("b1"), "Bob", "b@b", "msg-b-1", "")
	sha3, _, _ := s.Write("repo", "a.md", []byte("a2"), "Alice", "a@a", "msg-a-2", sha1)

	pages, err := s.ListPages("repo")
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("want 2 pages, got %d: %v", len(pages), pages)
	}
	// Sorted by path: a.md, b.md
	if pages[0].Path != "a.md" || pages[1].Path != "b.md" {
		t.Errorf("path order: %v", pages)
	}
	// a.md's last-touch is the second commit (sha3, msg-a-2)
	if pages[0].SHA != sha3 || pages[0].Message != "msg-a-2" {
		t.Errorf("a.md last-touch wrong: %+v", pages[0])
	}
	// b.md author = Bob
	if pages[1].Author != "Bob" {
		t.Errorf("b.md author: %+v", pages[1])
	}
}

func TestListReposWithMeta(t *testing.T) {
	s := newStore(t)
	s.Init("empty-repo")
	s.Init("active-repo")
	s.Write("active-repo", "x.md", []byte("hi"), "Alice", "a@a", "first", "")
	s.Write("active-repo", "y.md", []byte("yo"), "Alice", "a@a", "second", "")

	summaries, err := s.ListReposWithMeta()
	if err != nil {
		t.Fatalf("ListReposWithMeta: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}
	for _, sum := range summaries {
		if sum.ID == "empty-repo" {
			if sum.PageCount != 0 || sum.LastCommit != nil {
				t.Errorf("empty-repo summary wrong: %+v", sum)
			}
		}
		if sum.ID == "active-repo" {
			if sum.PageCount != 2 {
				t.Errorf("active-repo page count: %d", sum.PageCount)
			}
			if sum.LastCommit == nil || sum.LastCommit.Message != "second" {
				t.Errorf("active-repo last commit: %+v", sum.LastCommit)
			}
		}
	}
}

func TestReadAtSha(t *testing.T) {
	s := newStore(t)
	s.Init("repo")

	sha1, _, _ := s.Write("repo", "page.md", []byte("v1\n"), "A", "a@a", "v1", "")
	sha2, _, _ := s.Write("repo", "page.md", []byte("v2\n"), "A", "a@a", "v2", sha1)

	content, err := s.ReadAtSha("repo", "page.md", sha1)
	if err != nil {
		t.Fatalf("ReadAtSha sha1: %v", err)
	}
	if string(content) != "v1\n" {
		t.Errorf("sha1 content: %q", string(content))
	}

	content, err = s.ReadAtSha("repo", "page.md", sha2)
	if err != nil {
		t.Fatalf("ReadAtSha sha2: %v", err)
	}
	if string(content) != "v2\n" {
		t.Errorf("sha2 content: %q", string(content))
	}

	_, err = s.ReadAtSha("repo", "page.md", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if !errors.Is(err, ErrUnknownSHA) {
		t.Errorf("expected ErrUnknownSHA, got %v", err)
	}

	_, err = s.ReadAtSha("repo", "missing.md", sha1)
	if !errors.Is(err, ErrPathNotAtSHA) {
		t.Errorf("expected ErrPathNotAtSHA, got %v", err)
	}
}

func TestResolveParentSHAForPath(t *testing.T) {
	s := newStore(t)
	s.Init("repo")
	sha1, _, _ := s.Write("repo", "page.md", []byte("v1\n"), "A", "a@a", "v1", "")
	sha2, _, _ := s.Write("repo", "page.md", []byte("v2\n"), "A", "a@a", "v2", sha1)

	got, err := s.ResolveParentSHAForPath("repo", "page.md", sha2)
	if err != nil {
		t.Fatalf("ResolveParentSHAForPath sha2: %v", err)
	}
	if got != sha1 {
		t.Errorf("parent of sha2: got %q, want sha1=%q", got, sha1)
	}

	got, err = s.ResolveParentSHAForPath("repo", "page.md", sha1)
	if err != nil {
		t.Fatalf("ResolveParentSHAForPath sha1: %v", err)
	}
	if got != "" {
		t.Errorf("parent of sha1 (first version): got %q, want empty", got)
	}
}

func TestHeadSHAForPath(t *testing.T) {
	s := newStore(t)
	s.Init("repo")
	if got, _ := s.HeadSHAForPath("repo", "missing.md"); got != "" {
		t.Errorf("HeadSHAForPath missing: %q", got)
	}
	sha1, _, _ := s.Write("repo", "page.md", []byte("v1\n"), "A", "a@a", "v1", "")
	got, err := s.HeadSHAForPath("repo", "page.md")
	if err != nil {
		t.Fatalf("HeadSHAForPath: %v", err)
	}
	if got != sha1 {
		t.Errorf("HeadSHAForPath got %q, want %q", got, sha1)
	}
}

func TestSanitizeRepoID(t *testing.T) {
	cases := map[string]string{
		"chompchat":            "chompchat",
		"my-project_v2":        "my-project_v2",
		"foo/bar":              "foobar",
		"../../etc/passwd":     "etcpasswd",
		"":                     "",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
		_ = strings.TrimSpace
	}
}
