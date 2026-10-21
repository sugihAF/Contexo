package gitstore

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	gosync "sync"
	"testing"
	"time"
)

// wp02Store returns a Store with one initialized repo "r".
func wp02Store(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "repos"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Init("r"); err != nil {
		t.Fatalf("init: %v", err)
	}
	return s
}

// wp02Write writes through the legacy Write API and fails the test on any
// error or conflict.
func wp02Write(t *testing.T, s *Store, path, body, parent string) string {
	t.Helper()
	sha, conflict, err := s.Write("r", path, []byte(body), "Tester", "t@example.com", "msg", parent)
	if err != nil || conflict != nil {
		t.Fatalf("write %s: err=%v conflict=%+v", path, err, conflict)
	}
	return sha
}

// TestWriteNoopReturnsPathLastCommit pins the gitstore.go:130-132 bug: an
// unchanged write must return the commit that last touched the path, not repo
// HEAD. Returning HEAD makes the client record a sha that no longer matches
// the path's own history, so its next push 409s (false drift).
func TestWriteNoopReturnsPathLastCommit(t *testing.T) {
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/a.md", "alpha", "")
	c2 := wp02Write(t, s, "wiki/b.md", "beta", "")
	if c1 == c2 {
		t.Fatalf("expected distinct commits, got %s twice", c1)
	}

	got := wp02Write(t, s, "wiki/a.md", "alpha", c1)
	head, err := s.HeadSHA("r")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if got != c1 {
		t.Errorf("no-op write sha = %s, want a's last commit %s (HEAD is %s)", got, c1, head)
	}
	if got == head {
		t.Errorf("no-op write returned repo HEAD %s", head)
	}
	if head != c2 {
		t.Errorf("no-op write moved HEAD: got %s, want %s", head, c2)
	}

	res, conflict, err := s.WriteWithOptions("r", "wiki/a.md", []byte("alpha"), wp02Opts(""))
	if err != nil || conflict != nil {
		t.Fatalf("WriteWithOptions: err=%v conflict=%+v", err, conflict)
	}
	if res.Changed {
		t.Errorf("unchanged write reported Changed=true")
	}
	if res.SHA != c1 {
		t.Errorf("WriteWithOptions no-op sha = %s, want %s", res.SHA, c1)
	}
}

func wp02Opts(parent string) WriteOptions {
	return WriteOptions{AuthorName: "Tester", AuthorEmail: "t@example.com", Message: "msg", ParentSHA: parent}
}

// TestWriteNoopIsLocaleIndependent: no-op detection must not depend on git's
// (translatable) "nothing to commit" message. gitCmd forces LC_ALL=C, so a
// German host locale alone cannot catch a regression to text matching; the
// call log pins the mechanism instead: an unchanged write is decided by
// `diff --cached --quiet`'s exit status and never runs `git commit`, the step
// whose message the old code matched.
func TestWriteNoopIsLocaleIndependent(t *testing.T) {
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LC_MESSAGES", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "de")
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/a.md", "alpha", "")
	wp02Write(t, s, "wiki/b.md", "beta", "")

	calls := ""
	if runtime.GOOS != "windows" {
		realGit, err := exec.LookPath("git")
		if err != nil {
			t.Fatalf("look up git: %v", err)
		}
		bin := t.TempDir()
		calls = filepath.Join(bin, "calls.log")
		wrapper := "#!/bin/sh\necho \"$*\" >> \"$WP02_GIT_CALLS\"\nexec \"$WP02_REAL_GIT\" \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
			t.Fatalf("write git wrapper: %v", err)
		}
		t.Setenv("WP02_GIT_CALLS", calls)
		t.Setenv("WP02_REAL_GIT", realGit)
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	res, conflict, err := s.WriteWithOptions("r", "wiki/a.md", []byte("alpha"), wp02Opts(c1))
	if err != nil || conflict != nil {
		t.Fatalf("write: err=%v conflict=%+v", err, conflict)
	}
	if res.Changed || res.SHA != c1 {
		t.Errorf("got %+v, want {SHA:%s Changed:false}", res, c1)
	}
	if calls != "" {
		log, err := os.ReadFile(calls)
		if err != nil {
			t.Fatalf("read git call log: %v", err)
		}
		for _, line := range strings.Split(string(log), "\n") {
			if f := strings.Fields(line); len(f) > 2 && f[2] == "commit" {
				t.Errorf("an unchanged write ran %q; no-op detection must not go through git commit", line)
			}
		}
		if !strings.Contains(string(log), "diff --cached --quiet") {
			t.Errorf("an unchanged write did not use diff --cached --quiet:\n%s", log)
		}
	}
}

func TestWriteWithOptionsReportsChanged(t *testing.T) {
	s := wp02Store(t)

	res, conflict, err := s.WriteWithOptions("r", "wiki/a.md", []byte("v1"), wp02Opts(""))
	if err != nil || conflict != nil {
		t.Fatalf("create: err=%v conflict=%+v", err, conflict)
	}
	head, _ := s.HeadSHA("r")
	pathSHA, _ := s.HeadSHAForPath("r", "wiki/a.md")
	if !res.Changed || res.SHA != head || res.SHA != pathSHA {
		t.Errorf("create: got %+v, want Changed=true SHA=%s (HEAD) = %s (path)", res, head, pathSHA)
	}

	res2, conflict, err := s.WriteWithOptions("r", "wiki/a.md", []byte("v2"), wp02Opts(res.SHA))
	if err != nil || conflict != nil {
		t.Fatalf("edit: err=%v conflict=%+v", err, conflict)
	}
	if !res2.Changed || res2.SHA == res.SHA {
		t.Errorf("edit: got %+v, want Changed=true and a new sha (old %s)", res2, res.SHA)
	}
	content, sha, err := s.Read("r", "wiki/a.md")
	if err != nil || string(content) != "v2" || sha != res2.SHA {
		t.Errorf("read after edit: content=%q sha=%s err=%v, want v2 at %s", content, sha, err, res2.SHA)
	}
}

func TestWriteExpectAbsentConflicts(t *testing.T) {
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/a.md", "teammate", "")

	for _, parent := range []string{"", c1} {
		o := wp02Opts(parent)
		o.ExpectAbsent = true
		res, conflict, err := s.WriteWithOptions("r", "wiki/a.md", []byte("mine"), o)
		if !errors.Is(err, ErrConflict) || conflict == nil {
			t.Fatalf("parent=%q: want ErrConflict with a *Conflict, got res=%+v conflict=%+v err=%v", parent, res, conflict, err)
		}
		if conflict.Path != "wiki/a.md" || conflict.CurrentSHA != c1 ||
			string(conflict.CurrentContent) != "teammate" || conflict.ExpectedParentSHA != "" {
			t.Errorf("parent=%q: conflict = %+v, want path/current_sha %s/content teammate/expected_parent_sha \"\"", parent, conflict, c1)
		}
	}
	content, sha, err := s.Read("r", "wiki/a.md")
	if err != nil || string(content) != "teammate" || sha != c1 {
		t.Errorf("ExpectAbsent conflict must not write: content=%q sha=%s err=%v", content, sha, err)
	}

	o := wp02Opts("")
	o.ExpectAbsent = true
	res, conflict, err := s.WriteWithOptions("r", "wiki/new.md", []byte("fresh"), o)
	if err != nil || conflict != nil || !res.Changed {
		t.Fatalf("absent path: res=%+v conflict=%+v err=%v, want a write", res, conflict, err)
	}
	if content, _, _ := s.Read("r", "wiki/new.md"); string(content) != "fresh" {
		t.Errorf("absent path content = %q, want fresh", content)
	}
}

// TestWriteConcurrentSameRepo: concurrent writers (two teammates, or an agent
// and the editor extension) must each get their own commit. Unserialized,
// git add/commit race on index.lock and a plain `git commit` sweeps another
// writer's staged file into the wrong commit.
func TestWriteConcurrentSameRepo(t *testing.T) {
	s := wp02Store(t)
	wp02Write(t, s, "wiki/seed.md", "seed", "")

	const n = 8
	type result struct {
		path string
		res  WriteResult
		err  error
	}
	results := make(chan result, n)
	var wg gosync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := fmt.Sprintf("wiki/page-%d.md", i)
			res, conflict, err := s.WriteWithOptions("r", p, []byte(p), wp02Opts(""))
			if err == nil && conflict != nil {
				err = fmt.Errorf("unexpected conflict %+v", conflict)
			}
			results <- result{p, res, err}
		}(i)
	}
	wg.Wait()
	close(results)

	for r := range results {
		if r.err != nil {
			t.Errorf("%s: %v", r.path, r.err)
			continue
		}
		pathSHA, _ := s.HeadSHAForPath("r", r.path)
		if !r.res.Changed || r.res.SHA != pathSHA {
			t.Errorf("%s: got %+v, want Changed=true SHA=%s", r.path, r.res, pathSHA)
		}
	}
	commits, err := s.Log("r", 100)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if len(commits) != n+1 {
		t.Errorf("want %d commits (one per write), got %d", n+1, len(commits))
	}
	for _, c := range commits {
		out, err := s.git(s.repoDir("r"), "show", "--name-only", "--format=", c.SHA)
		if err != nil {
			t.Fatalf("show %s: %v %s", c.SHA, err, out)
		}
		if files := splitLines(out); len(files) != 1 {
			t.Errorf("commit %s touches %v, want exactly one path", c.SHA, files)
		}
	}
}

// TestReadPageAtPathSHAAtOrBefore: ReadPageAt returns the page as of the
// requested commit together with the path's own last commit at or before it,
// which is the sha a client records as the page's parent.
func TestReadPageAtPathSHAAtOrBefore(t *testing.T) {
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/a.md", "alpha v1", "")
	c2 := wp02Write(t, s, "wiki/b.md", "beta", "")
	c3 := wp02Write(t, s, "wiki/a.md", "alpha v2", c1)

	cases := []struct{ at, want, wantSHA string }{
		{c1, "alpha v1", c1},
		{c2, "alpha v1", c1},
		{c2[:7], "alpha v1", c1},
		{strings.ToUpper(c2[:7]), "alpha v1", c1},
		{c3, "alpha v2", c3},
	}
	for _, tc := range cases {
		content, pathSHA, err := s.ReadPageAt("r", "wiki/a.md", tc.at)
		if err != nil {
			t.Errorf("at %s: %v", tc.at, err)
			continue
		}
		if string(content) != tc.want || pathSHA != tc.wantSHA {
			t.Errorf("at %s: got %q@%s, want %q@%s", tc.at, content, pathSHA, tc.want, tc.wantSHA)
		}
	}
}

func TestValidSHA(t *testing.T) {
	hex40 := strings.Repeat("0123456789", 4)
	hex64 := strings.Repeat("abcdef0123456789", 4)
	for _, s := range []string{"abcd", "abcdef1", "ABCDEF1", "AbCd", hex40, strings.ToUpper(hex64), hex64} {
		if !ValidSHA(s) {
			t.Errorf("ValidSHA(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "abc", "zz", "--all", "-G.", "HEAD", "main", "abcd^", "abcd~1", "abcd ", " abcd", "abcd:x", hex64 + "a"} {
		if ValidSHA(s) {
			t.Errorf("ValidSHA(%q) = true, want false", s)
		}
	}
}

// TestReadPageAtErrors pins the documented error order: repo, then sha
// syntax (before any git call), then path safety, then git resolution.
func TestReadPageAtErrors(t *testing.T) {
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/a.md", "alpha", "")
	wp02Write(t, s, "wiki/b.md", "beta", "")

	cases := []struct {
		name, repo, path, at string
		want                 error
	}{
		{"missing repo", "ghost", "wiki/a.md", c1, ErrRepoNotFound},
		{"missing repo wins", "ghost", "wiki/-x.md", "zz", ErrRepoNotFound},
		{"empty sha", "r", "wiki/a.md", "", ErrUnknownSHA},
		{"non-hex sha", "r", "wiki/a.md", "zz", ErrUnknownSHA},
		{"flag sha", "r", "wiki/a.md", "--all", ErrUnknownSHA},
		{"grep flag sha", "r", "wiki/a.md", "-G.", ErrUnknownSHA},
		{"ref name", "r", "wiki/a.md", "HEAD", ErrUnknownSHA},
		{"too short", "r", "wiki/a.md", "abc", ErrUnknownSHA},
		{"too long", "r", "wiki/a.md", strings.Repeat("a", 65), ErrUnknownSHA},
		{"sha checked before path", "r", "wiki/-x.md", "zz", ErrUnknownSHA},
		{"flag path", "r", "wiki/-x.md", c1, ErrUnsafePath},
		{"traversal", "r", "../x.md", c1, ErrUnsafePath},
		{"unknown commit", "r", "wiki/a.md", strings.Repeat("deadbeef", 5), ErrUnknownSHA},
		{"path added later", "r", "wiki/b.md", c1, ErrPathNotAtSHA},
		{"never written", "r", "wiki/none.md", c1, ErrPathNotAtSHA},
	}
	for _, tc := range cases {
		content, pathSHA, err := s.ReadPageAt(tc.repo, tc.path, tc.at)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %q@%q err=%v, want %v", tc.name, content, pathSHA, err, tc.want)
		}
	}
}

// TestReadPageAtDirectoryIsAbsent: a tree path such as "wiki" is not a page,
// so it must not come back as a tree listing.
func TestReadPageAtDirectoryIsAbsent(t *testing.T) {
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/a.md", "alpha", "")

	if content, pathSHA, err := s.ReadPageAt("r", "wiki", c1); !errors.Is(err, ErrPathNotAtSHA) {
		t.Errorf("ReadPageAt(wiki) = %q@%q err=%v, want ErrPathNotAtSHA", content, pathSHA, err)
	}
	if content, err := s.ReadAtSha("r", "wiki", c1); !errors.Is(err, ErrPathNotAtSHA) {
		t.Errorf("ReadAtSha(wiki) = %q err=%v, want ErrPathNotAtSHA", content, err)
	}
}

// TestReadEmptyPageIsPresent: an empty page is a page. Whether exec hands
// back a nil or an empty slice for empty stdout is an implementation detail,
// so absence is decided by blobAt's ok and empty content is never nil.
func TestReadEmptyPageIsPresent(t *testing.T) {
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/empty.md", "", "")

	content, sha, err := s.Read("r", "wiki/empty.md")
	if err != nil || content == nil || len(content) != 0 || sha != c1 {
		t.Errorf("Read = %q (nil=%v) @%s err=%v, want empty non-nil content @%s", content, content == nil, sha, err, c1)
	}
	if content, err := s.ReadAtSha("r", "wiki/empty.md", c1); err != nil || content == nil || len(content) != 0 {
		t.Errorf("ReadAtSha = %q (nil=%v) err=%v, want empty non-nil content", content, content == nil, err)
	}
	content, pathSHA, err := s.ReadPageAt("r", "wiki/empty.md", c1)
	if err != nil || content == nil || len(content) != 0 || pathSHA != c1 {
		t.Errorf("ReadPageAt = %q (nil=%v) @%s err=%v, want empty non-nil content @%s", content, content == nil, pathSHA, err, c1)
	}
}

// TestReadDeletedOrDirectoryIsNotExist: Read reports os.ErrNotExist for a
// path that is not a file at HEAD (deleted, a directory, never written, or an
// empty repo) instead of a 200 with an empty body or a tree listing.
func TestReadDeletedOrDirectoryIsNotExist(t *testing.T) {
	s := wp02Store(t)
	if content, sha, err := s.Read("r", "wiki/a.md"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("empty repo: Read = %q@%q err=%v, want os.ErrNotExist", content, sha, err)
	}

	c1 := wp02Write(t, s, "wiki/a.md", "alpha", "")
	c2 := wp02Write(t, s, "wiki/b.md", "beta", "")
	dir := s.repoDir("r")
	if out, err := s.git(dir, "rm", "-q", "--", "wiki/a.md"); err != nil {
		t.Fatalf("git rm: %v %s", err, out)
	}
	if out, err := s.git(dir, "commit", "-q", "-m", "delete a"); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	del, err := s.HeadSHA("r")
	if err != nil || del == c2 {
		t.Fatalf("deletion commit: %s err=%v", del, err)
	}

	for _, p := range []string{"wiki/a.md", "wiki", "wiki/never.md"} {
		if content, sha, err := s.Read("r", p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Read(%q) = %q@%q err=%v, want os.ErrNotExist", p, content, sha, err)
		}
	}
	if content, sha, err := s.Read("r", "wiki/b.md"); err != nil || string(content) != "beta" || sha != c2 {
		t.Errorf("Read(b) = %q@%s err=%v, want beta@%s", content, sha, err, c2)
	}

	if _, _, err := s.ReadPageAt("r", "wiki/a.md", del); !errors.Is(err, ErrPathNotAtSHA) {
		t.Errorf("ReadPageAt(a, deletion) err=%v, want ErrPathNotAtSHA", err)
	}
	if _, err := s.ReadAtSha("r", "wiki/a.md", del); !errors.Is(err, ErrPathNotAtSHA) {
		t.Errorf("ReadAtSha(a, deletion) err=%v, want ErrPathNotAtSHA", err)
	}
	if content, sha, err := s.ReadPageAt("r", "wiki/a.md", c2); err != nil || string(content) != "alpha" || sha != c1 {
		t.Errorf("ReadPageAt(a, c2) = %q@%s err=%v, want alpha@%s", content, sha, err, c1)
	}
}

// wp02CommitFiles returns the paths a commit touches, verbatim (-z output is
// never C-quoted).
func wp02CommitFiles(t *testing.T, s *Store, sha string) []string {
	t.Helper()
	out, err := s.git(s.repoDir("r"), "diff-tree", "-r", "--root", "--no-commit-id", "--name-only", "-z", sha)
	if err != nil {
		t.Fatalf("diff-tree %s: %v %s", sha, err, out)
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

// wp02Status returns `git status --porcelain` for repo "r" (untracked files
// included), or "" when the working tree and index match HEAD.
func wp02Status(t *testing.T, s *Store) string {
	t.Helper()
	out, err := s.git(s.repoDir("r"), "status", "--porcelain")
	if err != nil {
		t.Fatalf("status: %v %s", err, out)
	}
	return strings.TrimSpace(out)
}

// TestWriteMagicPathIsLiteral: page paths reach git as pathspecs, where a
// leading ':' is pathspec magic (":memo.md" means "memo.md"). Unless pathspecs
// are literal, a write of ":memo.md" stages and checks memo.md instead,
// reports an unchanged no-op and leaves the new file untracked.
func TestWriteMagicPathIsLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("':' is not valid in a Windows file name")
	}
	s := wp02Store(t)
	c := wp02Write(t, s, "memo.md", "memo", "")

	o := wp02Opts("")
	o.ExpectAbsent = true
	res, conflict, err := s.WriteWithOptions("r", ":memo.md", []byte("magic"), o)
	if err != nil || conflict != nil {
		t.Fatalf("write :memo.md: res=%+v conflict=%+v err=%v", res, conflict, err)
	}
	if !res.Changed || res.SHA == c {
		t.Fatalf("write :memo.md: got %+v, want Changed=true and a new commit (memo.md is at %s)", res, c)
	}
	if files := wp02CommitFiles(t, s, res.SHA); len(files) != 1 || files[0] != ":memo.md" {
		t.Errorf("commit %s touches %q, want only \":memo.md\"", res.SHA, files)
	}
	if content, sha, err := s.Read("r", ":memo.md"); err != nil || string(content) != "magic" || sha != res.SHA {
		t.Errorf("Read(:memo.md) = %q@%s err=%v, want magic@%s", content, sha, err, res.SHA)
	}
	if content, sha, err := s.Read("r", "memo.md"); err != nil || string(content) != "memo" || sha != c {
		t.Errorf("Read(memo.md) = %q@%s err=%v, want memo@%s (unchanged)", content, sha, err, c)
	}
	if st := wp02Status(t, s); st != "" {
		t.Errorf("git status --porcelain = %q, want clean", st)
	}
}

// TestWriteGlobPathIsLiteral: '?', '*' and '[' in a page path are glob
// characters to git unless pathspecs are literal, so "wiki/a?.md" would also
// match wiki/ab.md: its "last commit" would be ab's, and a write of
// "wiki/a*.md" would sweep a dirty wiki/ab.md into its commit.
func TestWriteGlobPathIsLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("'?' and '*' are not valid in a Windows file name")
	}
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/a?.md", "glob", "")
	c2 := wp02Write(t, s, "wiki/ab.md", "ab", "")

	res, conflict, err := s.WriteWithOptions("r", "wiki/a?.md", []byte("glob"), wp02Opts(c1))
	if err != nil || conflict != nil {
		t.Errorf("unchanged rewrite of wiki/a?.md with parent %s: res=%+v conflict=%+v err=%v", c1, res, conflict, err)
	} else if res.Changed || res.SHA != c1 {
		t.Errorf("unchanged rewrite of wiki/a?.md: got %+v, want {SHA:%s Changed:false}", res, c1)
	}
	if content, sha, err := s.Read("r", "wiki/a?.md"); err != nil || string(content) != "glob" || sha != c1 {
		t.Errorf("Read(wiki/a?.md) = %q@%s err=%v, want glob@%s", content, sha, err, c1)
	}
	if content, pathSHA, err := s.ReadPageAt("r", "wiki/a?.md", c2); err != nil || string(content) != "glob" || pathSHA != c1 {
		t.Errorf("ReadPageAt(wiki/a?.md, c2) = %q@%s err=%v, want glob@%s", content, pathSHA, err, c1)
	}

	dirty := filepath.Join(s.repoDir("r"), "wiki", "ab.md")
	if err := os.WriteFile(dirty, []byte("dirty"), 0o644); err != nil {
		t.Fatalf("dirty wiki/ab.md: %v", err)
	}
	res, conflict, err = s.WriteWithOptions("r", "wiki/a*.md", []byte("star"), wp02Opts(""))
	if err != nil || conflict != nil || !res.Changed {
		t.Fatalf("write wiki/a*.md: res=%+v conflict=%+v err=%v, want a new commit", res, conflict, err)
	}
	if files := wp02CommitFiles(t, s, res.SHA); len(files) != 1 || files[0] != "wiki/a*.md" {
		t.Errorf("commit %s touches %q, want only \"wiki/a*.md\"", res.SHA, files)
	}
	if content, sha, err := s.Read("r", "wiki/ab.md"); err != nil || string(content) != "ab" || sha != c2 {
		t.Errorf("Read(wiki/ab.md) = %q@%s err=%v, want ab@%s (the dirty file must not be committed)", content, sha, err, c2)
	}
}

// wp02PageSHAs maps each listed page path to its last-touch sha.
func wp02PageSHAs(t *testing.T, s *Store) map[string]string {
	t.Helper()
	pages, err := s.ListPages("r")
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	m := map[string]string{}
	for _, p := range pages {
		m[p.Path] = p.SHA
	}
	return m
}

// wp02SameSet reports whether got and want hold the same paths.
func wp02SameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	set := map[string]bool{}
	for _, p := range got {
		set[p] = true
	}
	for _, p := range want {
		if !set[p] {
			return false
		}
	}
	return true
}

// TestListPagesNonASCIIPath: git C-quotes non-ASCII names in its plain-text
// output under core.quotePath=true ("wiki/caf\303\251.md"), so the listing
// must come back verbatim, not as git's escaped form.
func TestListPagesNonASCIIPath(t *testing.T) {
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/café.md", "accent", "")
	c2 := wp02Write(t, s, "wiki/a.md", "plain", "")

	got := wp02PageSHAs(t, s)
	want := map[string]string{"wiki/café.md": c1, "wiki/a.md": c2}
	if len(got) != len(want) || got["wiki/café.md"] != c1 || got["wiki/a.md"] != c2 {
		t.Errorf("ListPages = %q, want %q", got, want)
	}
}

// TestChangedSinceNonASCIIPath: ChangedSince feeds pull, which reads each
// returned path back, so a C-quoted name would be a page the client can never
// fetch.
func TestChangedSinceNonASCIIPath(t *testing.T) {
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/a.md", "plain", "")
	c2 := wp02Write(t, s, "wiki/café.md", "accent", "")

	files, head, err := s.ChangedSince("r", "")
	if err != nil || head != c2 || !wp02SameSet(files, []string{"wiki/a.md", "wiki/café.md"}) {
		t.Errorf("ChangedSince(\"\") = %q head=%s err=%v, want [wiki/a.md wiki/café.md] head=%s", files, head, err, c2)
	}
	files, head, err = s.ChangedSince("r", c1)
	if err != nil || head != c2 || !wp02SameSet(files, []string{"wiki/café.md"}) {
		t.Errorf("ChangedSince(c1) = %q head=%s err=%v, want [wiki/café.md] head=%s", files, head, err, c2)
	}
}

// TestListPagesQuotedPath: git quotes a name containing '"' or '\' even with
// core.quotePath=false ("wiki/quo\"te.md"), keeping any non-ASCII bytes raw
// inside the quotes. Such names must still be listed verbatim.
func TestListPagesQuotedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("'\"' is not valid, and '\\' is a separator, in a Windows file name")
	}
	paths := []string{`wiki/quo"te.md`, `wiki/back\slash.md`, `wiki/"café".md`}
	for _, p := range paths {
		if err := validatePagePath(p); err != nil {
			t.Skipf("validatePagePath(%q) = %v: such paths never reach git", p, err)
		}
	}
	s := wp02Store(t)
	c0 := wp02Write(t, s, "wiki/plain.md", "plain", "")
	shas := map[string]string{}
	for _, p := range paths {
		shas[p] = wp02Write(t, s, p, p, "")
	}

	got := wp02PageSHAs(t, s)
	for _, p := range paths {
		if got[p] != shas[p] {
			t.Errorf("ListPages[%q] = %q, want %s (listing: %q)", p, got[p], shas[p], got)
		}
	}
	if len(got) != len(paths)+1 {
		t.Errorf("ListPages = %q, want %d pages", got, len(paths)+1)
	}
	files, _, err := s.ChangedSince("r", c0)
	if err != nil || !wp02SameSet(files, paths) {
		t.Errorf("ChangedSince(c0) = %q err=%v, want %q", files, err, paths)
	}
	files, _, err = s.ChangedSince("r", "")
	if err != nil || !wp02SameSet(files, append([]string{"wiki/plain.md"}, paths...)) {
		t.Errorf("ChangedSince(\"\") = %q err=%v, want wiki/plain.md and %q", files, err, paths)
	}
}

func TestUnquoteGitPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"wiki/a.md", "wiki/a.md"},
		{"wiki/café.md", "wiki/café.md"},
		{`"wiki/caf\303\251.md"`, "wiki/café.md"},
		{`"wiki/quo\"te.md"`, `wiki/quo"te.md`},
		{`"wiki/back\\slash.md"`, `wiki/back\slash.md`},
		{`"wiki/"café".md"`, `"wiki/"café".md"`},
		{`"unterminated`, `"unterminated`},
	}
	for _, tc := range cases {
		if got := unquoteGitPath(tc.in); got != tc.want {
			t.Errorf("unquoteGitPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// wp02NoAmbientGitIdentity leaves the repo's own user.name/user.email (set by
// Init) as the only committer identity git can use, so a commit that runs
// before Init has configured the repo fails instead of borrowing the host's
// global identity.
func wp02NoAmbientGitIdentity(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "EMAIL"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Setenv(k, v) // restores the value after the test
			os.Unsetenv(k)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.useConfigOnly")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
}

// TestInitConcurrentThenWrite: concurrent first pushes to a new repo each run
// Init then a write. Unserialized, they race `git init` and `git config`
// (config.lock), and a write can commit before user.name/user.email are set.
func TestInitConcurrentThenWrite(t *testing.T) {
	wp02NoAmbientGitIdentity(t)
	s, err := Open(filepath.Join(t.TempDir(), "repos"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	const n = 8
	errs := make([]error, n)
	start := make(chan struct{})
	var wg gosync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if err := s.Init("fresh"); err != nil {
				errs[i] = fmt.Errorf("init: %w", err)
				return
			}
			p := fmt.Sprintf("wiki/page-%d.md", i)
			res, conflict, err := s.WriteWithOptions("fresh", p, []byte(p), wp02Opts(""))
			if err != nil || conflict != nil || !res.Changed {
				errs[i] = fmt.Errorf("write %s: res=%+v conflict=%+v err=%v", p, res, conflict, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
	if commits, err := s.Log("fresh", 100); err != nil || len(commits) != n {
		t.Errorf("Log = %d commits err=%v, want %d (one per write)", len(commits), err, n)
	}
}

// TestWriteSamePathConcurrentCAS pins the repo lock's lost-update guarantee:
// of several writers that all hold the same parent, exactly one commits and
// every other one gets a conflict naming the winner's commit.
func TestWriteSamePathConcurrentCAS(t *testing.T) {
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/a.md", "v0", "")

	const n = 8
	type result struct {
		res      WriteResult
		conflict *Conflict
		err      error
	}
	results := make([]result, n)
	start := make(chan struct{})
	var wg gosync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, conflict, err := s.WriteWithOptions("r", "wiki/a.md", []byte(fmt.Sprintf("v%d", i+1)), wp02Opts(c1))
			results[i] = result{res, conflict, err}
		}(i)
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, r := range results {
		if r.err == nil && r.conflict == nil && r.res.Changed {
			if winner >= 0 {
				t.Fatalf("writers %d and %d both committed with parent %s: %+v", winner, i, c1, results)
			}
			winner = i
		}
	}
	if winner < 0 {
		t.Fatalf("no writer committed: %+v", results)
	}
	won := results[winner].res.SHA
	wonContent := fmt.Sprintf("v%d", winner+1)
	for i, r := range results {
		if i == winner {
			continue
		}
		if !errors.Is(r.err, ErrConflict) || r.conflict == nil {
			t.Errorf("writer %d: res=%+v conflict=%+v err=%v, want ErrConflict", i, r.res, r.conflict, r.err)
			continue
		}
		if r.conflict.CurrentSHA != won || string(r.conflict.CurrentContent) != wonContent || r.conflict.ExpectedParentSHA != c1 {
			t.Errorf("writer %d: conflict = %+v, want current %s (%q), expected parent %s", i, r.conflict, won, wonContent, c1)
		}
	}
	if content, sha, err := s.Read("r", "wiki/a.md"); err != nil || string(content) != wonContent || sha != won {
		t.Errorf("Read = %q@%s err=%v, want %q@%s", content, sha, err, wonContent, won)
	}
	if commits, err := s.Log("r", 100); err != nil || len(commits) != 2 {
		t.Errorf("Log = %d commits err=%v, want 2", len(commits), err)
	}
}

// wp02CaseInsensitiveFS reports whether dir's filesystem folds case.
func wp02CaseInsensitiveFS(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "probe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer os.Remove(probe)
	_, err := os.Stat(filepath.Join(dir, "PROBE"))
	return err == nil
}

// TestWriteCaseVariantOnCaseInsensitiveFS: on a case-insensitive filesystem
// (git sets core.ignorecase), writing "Wiki/A.md" while wiki/a.md is tracked
// opens and overwrites wiki/a.md on disk, but the index never holds
// "Wiki/A.md". The write must be refused as an unsafe path and leave the repo
// as it was, not be reported as an unchanged no-op.
func TestWriteCaseVariantOnCaseInsensitiveFS(t *testing.T) {
	s := wp02Store(t)
	if !wp02CaseInsensitiveFS(t, s.Root) {
		t.Skip("filesystem is case-sensitive")
	}
	c1 := wp02Write(t, s, "wiki/a.md", "lower", "")
	c2 := wp02Write(t, s, "wiki/b.md", "b", "")
	onDisk := filepath.Join(s.repoDir("r"), "wiki", "a.md")

	for _, expectAbsent := range []bool{false, true} {
		o := wp02Opts("")
		o.ExpectAbsent = expectAbsent
		res, conflict, err := s.WriteWithOptions("r", "Wiki/A.md", []byte("UPPER"), o)
		if !errors.Is(err, ErrUnsafePath) {
			t.Errorf("ExpectAbsent=%v: got res=%+v conflict=%+v err=%v, want ErrUnsafePath", expectAbsent, res, conflict, err)
		}
		if content, sha, err := s.Read("r", "wiki/a.md"); err != nil || string(content) != "lower" || sha != c1 {
			t.Errorf("ExpectAbsent=%v: Read(wiki/a.md) = %q@%s err=%v, want lower@%s", expectAbsent, content, sha, err, c1)
		}
		if b, err := os.ReadFile(onDisk); err != nil || string(b) != "lower" {
			t.Errorf("ExpectAbsent=%v: wiki/a.md on disk = %q err=%v, want lower", expectAbsent, b, err)
		}
		if head, err := s.HeadSHA("r"); err != nil || head != c2 {
			t.Errorf("ExpectAbsent=%v: HEAD = %s err=%v, want %s", expectAbsent, head, err, c2)
		}
		if st := wp02Status(t, s); st != "" {
			t.Errorf("ExpectAbsent=%v: git status --porcelain = %q, want clean", expectAbsent, st)
		}
	}

	// A new page under a case variant of a tracked directory lands in that
	// directory on disk and git stages it under the on-disk name
	// (wiki/c.md), so the refusal must also unstage and delete it.
	for _, p := range []string{"Wiki/c.md", "WIKI/new/x.md"} {
		if res, conflict, err := s.WriteWithOptions("r", p, []byte("new"), wp02Opts("")); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s: got res=%+v conflict=%+v err=%v, want ErrUnsafePath", p, res, conflict, err)
		}
		if head, err := s.HeadSHA("r"); err != nil || head != c2 {
			t.Errorf("%s: HEAD = %s err=%v, want %s", p, head, err, c2)
		}
		if st := wp02Status(t, s); st != "" {
			t.Errorf("%s: git status --porcelain = %q, want clean", p, st)
		}
	}
}

// TestCreateReportsCreatedOnce: of several callers that create the same new
// repo at once, only the one that actually ran git init is told it created
// it, so a handler can grant ownership to exactly one of them.
func TestCreateReportsCreatedOnce(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "repos"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	const n = 8
	created := make([]bool, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg gosync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			created[i], errs[i] = s.Create("new")
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i := range created {
		if errs[i] != nil {
			t.Errorf("Create %d: %v", i, errs[i])
		}
		if created[i] {
			winners++
		}
	}
	if winners != 1 {
		t.Errorf("created=true for %d callers, want exactly 1: %v", winners, created)
	}
	if !s.Exists("new") {
		t.Errorf("repo new does not exist after Create")
	}
	if again, err := s.Create("new"); err != nil || again {
		t.Errorf("later Create = %v err=%v, want false, nil", again, err)
	}
	if err := s.Init("new"); err != nil {
		t.Errorf("Init of an existing repo: %v", err)
	}
	if created, err := s.Create("..."); err == nil || created {
		t.Errorf("Create(\"...\") = %v err=%v, want an invalid repoID error", created, err)
	}
}

// TestValidatePagePathExported: the exported validator lets a caller check
// every path of a batch before any write, so it must agree with the guard
// every gitstore entry point applies.
func TestValidatePagePathExported(t *testing.T) {
	safe := []string{"wiki/a.md", "a.md", "wiki/café.md", "wiki/a b.md"}
	unsafe := []string{"", "/etc/passwd", "../x.md", "wiki/../x.md", "wiki//a.md",
		"wiki/./a.md", "wiki/-x.md", "--all", ".git/config", "wiki/.GIT/hooks",
		"wiki/a\x00.md", "wiki/a\n.md", `..\x.md`}
	// OS-dependent (Windows refuses ':'): only checked for agreement.
	osDependent := []string{":memo.md", "notes/todo.md "}
	for _, p := range safe {
		if err := ValidatePagePath(p); err != nil {
			t.Errorf("ValidatePagePath(%q) = %v, want nil", p, err)
		}
	}
	for _, p := range unsafe {
		if err := ValidatePagePath(p); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("ValidatePagePath(%q) = %v, want ErrUnsafePath", p, err)
		}
	}
	all := append(append(append([]string{}, safe...), unsafe...), osDependent...)
	for _, p := range all {
		if got, want := ValidatePagePath(p), validatePagePath(p); got != want {
			t.Errorf("ValidatePagePath(%q) = %v, validatePagePath = %v", p, got, want)
		}
	}
}

// TestWriteDecomposedUnicodePath: with core.precomposeunicode (git's default
// on macOS) git indexes a decomposed (NFD) name such as "wiki/café.md"
// under its precomposed (NFC) bytes. That is the page the client named, not a
// case variant, so the write must succeed and an unchanged rewrite must be a
// no-op at the same commit.
func TestWriteDecomposedUnicodePath(t *testing.T) {
	s := wp02Store(t)
	out, _ := s.git(s.repoDir("r"), "config", "--type=bool", "core.precomposeunicode")
	if strings.TrimSpace(out) != "true" {
		t.Skipf("core.precomposeunicode is %q, not true", strings.TrimSpace(out))
	}
	const nfd = "wiki/café.md"

	res, conflict, err := s.WriteWithOptions("r", nfd, []byte("accent"), wp02Opts(""))
	if err != nil || conflict != nil || !res.Changed {
		t.Fatalf("write %q: res=%+v conflict=%+v err=%v, want a new commit", nfd, res, conflict, err)
	}
	again, conflict, err := s.WriteWithOptions("r", nfd, []byte("accent"), wp02Opts(res.SHA))
	if err != nil || conflict != nil || again.Changed || again.SHA != res.SHA {
		t.Errorf("unchanged rewrite of %q: res=%+v conflict=%+v err=%v, want {SHA:%s Changed:false}", nfd, again, conflict, err, res.SHA)
	}
	if st := wp02Status(t, s); st != "" {
		t.Errorf("git status --porcelain = %q, want clean", st)
	}
	if content, sha, err := s.Read("r", nfd); err != nil || string(content) != "accent" || sha != res.SHA {
		t.Errorf("Read(%q) = %q@%s err=%v, want accent@%s", nfd, content, sha, err, res.SHA)
	}
	o := wp02Opts("")
	o.ExpectAbsent = true
	if _, conflict, err := s.WriteWithOptions("r", nfd, []byte("mine"), o); !errors.Is(err, ErrConflict) ||
		conflict == nil || conflict.CurrentSHA != res.SHA || string(conflict.CurrentContent) != "accent" {
		t.Errorf("ExpectAbsent rewrite of %q: conflict=%+v err=%v, want ErrConflict at %s with content accent", nfd, conflict, err, res.SHA)
	}
}

// TestLockRepoKeyIsCaseInsensitive: on a case-insensitive filesystem "Repo"
// and "repo" are one git repo, so they must share one lock.
func TestLockRepoKeyIsCaseInsensitive(t *testing.T) {
	base := t.TempDir()
	unlock := lockRepo(filepath.Join(base, "Repo"))
	acquired := make(chan struct{})
	go func() {
		release := lockRepo(filepath.Join(base, "repo"))
		close(acquired)
		release()
	}()

	select {
	case <-acquired:
		unlock()
		t.Fatal("lockRepo(repo) was acquired while lockRepo(Repo) was held")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case <-acquired:
	case <-time.After(10 * time.Second):
		t.Fatal("lockRepo(repo) still blocked after lockRepo(Repo) was released")
	}
}

// TestChangedSinceKeepsSpacesInNames: validatePagePath accepts leading and
// trailing spaces, so ChangedSince must return such names verbatim; a trimmed
// name is a page pull can never read back.
func TestChangedSinceKeepsSpacesInNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows drops trailing spaces from file names")
	}
	s := wp02Store(t)
	c0 := wp02Write(t, s, "wiki/a.md", "plain", "")
	spaced := []string{"notes/todo.md ", " lead.md"}
	for _, p := range spaced {
		wp02Write(t, s, p, "spaced "+p, "")
	}

	files, _, err := s.ChangedSince("r", "")
	if err != nil || !wp02SameSet(files, append([]string{"wiki/a.md"}, spaced...)) {
		t.Errorf("ChangedSince(\"\") = %q err=%v, want wiki/a.md and %q", files, err, spaced)
	}
	files, _, err = s.ChangedSince("r", c0)
	if err != nil || !wp02SameSet(files, spaced) {
		t.Errorf("ChangedSince(c0) = %q err=%v, want %q", files, err, spaced)
	}
	for _, p := range files {
		if content, _, err := s.Read("r", p); err != nil || string(content) != "spaced "+p {
			t.Errorf("Read(%q) = %q err=%v, want %q", p, content, err, "spaced "+p)
		}
	}
}

// TestBlobAtTypes pins blobAt per object kind: only a blob is present, its
// bytes come back exactly (newlines, NUL, header-like text), and an empty
// blob is empty non-nil content.
func TestBlobAtTypes(t *testing.T) {
	s := wp02Store(t)
	const tricky = "alpha\n0123 blob 5\nwith\x00nul\n\n"
	c1 := wp02Write(t, s, "wiki/a.md", tricky, "")
	wp02Write(t, s, "wiki/empty.md", "", "")
	wp02Write(t, s, "wiki/x missing", "not missing", "")
	dir := s.repoDir("r")

	cases := []struct {
		name, rev, path string
		ok              bool
		want            string
	}{
		{"blob", "HEAD", "wiki/a.md", true, tricky},
		{"blob at full commit", c1, "wiki/a.md", true, tricky},
		{"blob at abbreviated commit", c1[:7], "wiki/a.md", true, tricky},
		{"empty blob", "HEAD", "wiki/empty.md", true, ""},
		{"name ending in missing", "HEAD", "wiki/x missing", true, "not missing"},
		{"tree", "HEAD", "wiki", false, ""},
		{"added later", c1, "wiki/empty.md", false, ""},
		{"never written", "HEAD", "wiki/none.md", false, ""},
		{"never written, name ending in missing", "HEAD", "wiki/y missing", false, ""},
		{"unknown commit", strings.Repeat("deadbeef", 5), "wiki/a.md", false, ""},
	}
	for _, tc := range cases {
		content, ok, err := s.blobAt(dir, tc.rev, tc.path)
		if err != nil || ok != tc.ok || string(content) != tc.want {
			t.Errorf("%s: blobAt(%s, %q) = %q ok=%v err=%v, want %q ok=%v", tc.name, tc.rev, tc.path, content, ok, err, tc.want, tc.ok)
		}
		if ok && content == nil {
			t.Errorf("%s: present blob returned nil content", tc.name)
		}
	}
}

// TestBlobAtIsOneGitProcess: Read calls blobAt for every page pull serves,
// so a lookup must cost one git process, whatever the object turns out to be.
func TestBlobAtIsOneGitProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("counts git processes through a /bin/sh wrapper")
	}
	s := wp02Store(t)
	wp02Write(t, s, "wiki/a.md", "alpha", "")
	wp02Write(t, s, "wiki/café.md", "accent", "")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("look up git: %v", err)
	}
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls.log")
	wrapper := "#!/bin/sh\necho \"$*\" >> \"$WP02_GIT_CALLS\"\nexec \"$WP02_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write git wrapper: %v", err)
	}
	t.Setenv("WP02_GIT_CALLS", calls)
	t.Setenv("WP02_REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, p := range []string{"wiki/a.md", "wiki/café.md", "wiki", "wiki/none.md"} {
		_ = os.Remove(calls)
		if _, _, err := s.blobAt(s.repoDir("r"), "HEAD", p); err != nil {
			t.Fatalf("blobAt(HEAD, %q): %v", p, err)
		}
		log, err := os.ReadFile(calls)
		if err != nil {
			t.Fatalf("read git call log: %v", err)
		}
		if n := strings.Count(string(log), "\n"); n != 1 {
			t.Errorf("blobAt(HEAD, %q) ran %d git processes, want 1:\n%s", p, n, log)
		}
	}
}

// TestReadPageAtResolvesShaOnce: ?at=<abbrev> is resolved to a full commit id
// exactly once; every later git call uses the full id. Passing the
// abbreviation again would let a commit pushed in between make it ambiguous
// (a 404 for a page that was just read).
func TestReadPageAtResolvesShaOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("logs git calls through a /bin/sh wrapper")
	}
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/a.md", "alpha", "")
	wp02Write(t, s, "wiki/b.md", "beta", "")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("look up git: %v", err)
	}
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls.log")
	wrapper := "#!/bin/sh\necho \"$*\" >> \"$WP02_GIT_CALLS\"\nexec \"$WP02_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write git wrapper: %v", err)
	}
	t.Setenv("WP02_GIT_CALLS", calls)
	t.Setenv("WP02_REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	abbrev := c1[:7]
	content, pathSHA, err := s.ReadPageAt("r", "wiki/a.md", abbrev)
	if err != nil || string(content) != "alpha" || pathSHA != c1 {
		t.Fatalf("ReadPageAt(%s) = %q, %s, %v; want alpha, %s", abbrev, content, pathSHA, err, c1)
	}
	log, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("read git call log: %v", err)
	}
	var withAbbrev []string
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		if strings.Contains(line, abbrev) && !strings.Contains(line, c1) {
			withAbbrev = append(withAbbrev, line)
		}
	}
	if len(withAbbrev) != 1 {
		t.Errorf("git calls passing the abbreviation %s: %q, want exactly one (the rev-parse)\nall calls:\n%s", abbrev, withAbbrev, log)
	}
}

// TestWriteToleratesOddCommitMetadata: the message and author come from the
// client. git refuses a commit whose message is only whitespace ("Aborting
// commit due to empty commit message") or whose author name is only
// punctuation ("empty ident name"), and '<' '>' or a newline break the
// --author "Name <email>" form; none of that may turn a valid page write into
// a 500.
func TestWriteToleratesOddCommitMetadata(t *testing.T) {
	s := wp02Store(t)
	cases := []WriteOptions{
		{AuthorName: "Tester", AuthorEmail: "t@example.com", Message: "  \n\t "},
		{AuthorName: ".", AuthorEmail: "t@example.com", Message: "m"},
		{AuthorName: "", AuthorEmail: "", Message: ""},
		{AuthorName: "Eve <evil>", AuthorEmail: "e@x>\n<y", Message: "m"},
		{AuthorName: "\u00A0", AuthorEmail: "\u3000", Message: "m"},
		{AuthorName: ". \u00A0", AuthorEmail: "t@example.com", Message: "m"},
		{AuthorName: "Tester", AuthorEmail: "t@example.com", Message: "fix\x00x"},
	}
	for i, o := range cases {
		p := fmt.Sprintf("wiki/odd-%d.md", i)
		res, conflict, err := s.WriteWithOptions("r", p, []byte(p), o)
		if err != nil || conflict != nil || !res.Changed {
			t.Errorf("case %d %+v: res=%+v conflict=%+v err=%v, want a commit", i, o, res, conflict, err)
		}
	}
}

// TestWritePushedGitignoreCannotBlockPages: a member can push a page named
// .gitignore (validatePagePath allows it). Its rules must not apply to the
// store's own `add`: with "*" in it, every later new page would otherwise
// fail with "paths are ignored" (a 500 for every push in the repo).
func TestWritePushedGitignoreCannotBlockPages(t *testing.T) {
	s := wp02Store(t)
	// Pushing one is refused now (TestGitControlFilesAreNotPages); a repo can
	// still hold one committed before that rule.
	dir := s.repoDir("r")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*\n"), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}
	if out, err := s.git(dir, "add", "-f", "--", ".gitignore"); err != nil {
		t.Fatalf("add .gitignore: %v %s", err, out)
	}
	if out, err := s.git(dir, "commit", "-m", "old ignore file", "--", ".gitignore"); err != nil {
		t.Fatalf("commit .gitignore: %v %s", err, out)
	}
	res, conflict, err := s.WriteWithOptions("r", "wiki/after.md", []byte("after"), wp02Opts(""))
	if err != nil || conflict != nil || !res.Changed {
		t.Fatalf("new page after a pushed .gitignore: res=%+v conflict=%+v err=%v, want a commit", res, conflict, err)
	}
	if content, _, err := s.Read("r", "wiki/after.md"); err != nil || string(content) != "after" {
		t.Errorf("read back: %q, %v", content, err)
	}
}

// TestGitControlFilesAreNotPages: .gitattributes, .gitignore and .gitmodules
// change how git treats every other path (eol/encoding/ident conversion of
// stored bytes, ignored adds), so a member must not be able to write one.
func TestGitControlFilesAreNotPages(t *testing.T) {
	for _, p := range []string{".gitattributes", ".gitignore", ".gitmodules", "wiki/.gitattributes",
		"wiki/sub/.GitIgnore", ".GITMODULES"} {
		if err := ValidatePagePath(p); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("ValidatePagePath(%q) = %v, want ErrUnsafePath", p, err)
		}
	}
	for _, p := range []string{"wiki/gitattributes.md", "wiki/.gitattributes.md", "wiki/notes.gitignore"} {
		if err := ValidatePagePath(p); err != nil {
			t.Errorf("ValidatePagePath(%q) = %v, want nil", p, err)
		}
	}
	s := wp02Store(t)
	if _, _, err := s.WriteWithOptions("r", ".gitattributes", []byte("* text eol=crlf\n"), wp02Opts("")); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("write .gitattributes: %v, want ErrUnsafePath", err)
	}
}

// TestCreateRollsBackHalfInitializedRepo: if git init succeeds but setting
// the committer identity fails, Create must not leave a .git behind; a later
// Create would report created=false for a repo that can never commit.
func TestCreateRollsBackHalfInitializedRepo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fails git config through a /bin/sh wrapper")
	}
	s, err := Open(filepath.Join(t.TempDir(), "repos"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("look up git: %v", err)
	}
	bin := t.TempDir()
	wrapper := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = config ] && exit 1; done\nexec \"$WP02_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write git wrapper: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("WP02_REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+origPath)

	if created, err := s.Create("half"); err == nil || created {
		t.Fatalf("Create with a failing git config: created=%v err=%v, want an error", created, err)
	}
	if s.Exists("half") {
		t.Errorf("a half-initialized .git was left behind")
	}
	t.Setenv("PATH", origPath)
	if created, err := s.Create("half"); err != nil || !created {
		t.Errorf("retry Create: created=%v err=%v, want a fresh repo", created, err)
	}
}

// TestListPagesSkipsUnreadableNames: a path ReadPage would refuse (a git
// control file committed before those were refused) is not listed, so a
// client never follows the listing into a 400.
func TestListPagesSkipsUnreadableNames(t *testing.T) {
	s := wp02Store(t)
	wp02Write(t, s, "wiki/a.md", "a", "")
	dir := s.repoDir("r")
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.md text\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if out, err := s.git(dir, "add", "-f", "--", ".gitattributes"); err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	if out, err := s.git(dir, "commit", "-m", "old attributes", "--", ".gitattributes"); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	pages, err := s.ListPages("r")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pages) != 1 || pages[0].Path != "wiki/a.md" {
		t.Errorf("listed %+v, want only wiki/a.md", pages)
	}
}

// TestWriteExpectAbsentFailsClosed: when the existence check itself fails
// (cat-file cannot run), a create-only write must fail, not treat the page as
// absent and overwrite it, which would silently break expect_absent's
// guarantee against the same-path create race.
func TestWriteExpectAbsentFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fails cat-file through a /bin/sh wrapper")
	}
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/a.md", "teammate", "")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("look up git: %v", err)
	}
	bin := t.TempDir()
	wrapper := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = --batch ] && exit 128; done\nexec \"$WP02_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write git wrapper: %v", err)
	}
	t.Setenv("WP02_REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	o := wp02Opts("")
	o.ExpectAbsent = true
	res, conflict, err := s.WriteWithOptions("r", "wiki/a.md", []byte("mine"), o)
	if err == nil {
		t.Fatalf("create-only write with a failing existence check: res=%+v conflict=%+v, want an error", res, conflict)
	}
	if errors.Is(err, ErrConflict) {
		t.Logf("got a conflict (acceptable fail-closed outcome): %v", err)
	}
	t.Setenv("PATH", strings.TrimPrefix(os.Getenv("PATH"), bin+string(os.PathListSeparator)))
	content, sha, rerr := s.Read("r", "wiki/a.md")
	if rerr != nil || string(content) != "teammate" || sha != c1 {
		t.Errorf("page after the failed create-only write: %q@%s (err %v), want teammate@%s", content, sha, rerr, c1)
	}
}
