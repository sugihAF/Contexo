package gitstore

import (
	"path/filepath"
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
}
