package gitstore

import (
	"errors"
	"fmt"
	"path/filepath"
	gosync "sync"
	"testing"
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
// (translatable) "nothing to commit" message.
func TestWriteNoopIsLocaleIndependent(t *testing.T) {
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LC_MESSAGES", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "de")
	s := wp02Store(t)
	c1 := wp02Write(t, s, "wiki/a.md", "alpha", "")
	wp02Write(t, s, "wiki/b.md", "beta", "")

	res, conflict, err := s.WriteWithOptions("r", "wiki/a.md", []byte("alpha"), wp02Opts(c1))
	if err != nil || conflict != nil {
		t.Fatalf("write: err=%v conflict=%+v", err, conflict)
	}
	if res.Changed || res.SHA != c1 {
		t.Errorf("got %+v, want {SHA:%s Changed:false}", res, c1)
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
