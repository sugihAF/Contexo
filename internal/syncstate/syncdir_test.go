package syncstate

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// wp04viewGitignore asserts hub/.sync/.gitignore holds exactly "*\n".
func wp04viewGitignore(t *testing.T, hub string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(SyncDir(hub), ".gitignore"))
	if err != nil {
		t.Fatalf(".sync/.gitignore: %v", err)
	}
	if string(got) != "*\n" {
		t.Errorf(".sync/.gitignore = %q, want %q", got, "*\n")
	}
}

// wp04viewGit runs git in dir with the user's and the system's git config
// out of the way (HOME is isolated by the caller) and any GIT_* variable of
// an enclosing hook removed. It returns git's exit code and output.
func wp04viewGit(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	base := []string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}
	cmd := exec.Command("git", append(base, args...)...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out)
	}
	t.Fatalf("git %q: %v\n%s", args, err, out)
	return -1, ""
}

// TestSyncDirSelfIgnoring checks E41: the first write under .sync, by any of
// the stores or the lock, creates .sync/.gitignore = "*\n", so a team that
// commits .contexo/ never commits base, conflict or backup copies.
func TestSyncDirSelfIgnoring(t *testing.T) {
	const rel = "wiki/concepts/x.md"
	page := wp04viewPage("x", "# X\n")
	sidecar := ConflictRecord{Path: rel, ServerSHA: wp04viewSR, BaseSHA: wp04viewSB,
		Kind: KindBothModified, Source: SourcePull}
	firstWrites := []struct {
		name  string
		write func(t *testing.T, hub string)
	}{
		{"Bases.Put", func(t *testing.T, hub string) {
			b, err := OpenBases(hub)
			if err == nil {
				err = b.Put(rel, page, wp04viewSB, time.Time{})
			}
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"Bases.Save", func(t *testing.T, hub string) {
			b, err := OpenBases(hub)
			if err == nil {
				err = b.Save()
			}
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"Conflicts.Put", func(t *testing.T, hub string) {
			c, err := OpenConflicts(hub)
			if err == nil {
				err = c.Put(sidecar, page)
			}
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"Conflicts.Put/no_content", func(t *testing.T, hub string) {
			c, err := OpenConflicts(hub)
			if err == nil {
				err = c.Put(sidecar, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"Conflicts.Save", func(t *testing.T, hub string) {
			c, err := OpenConflicts(hub)
			if err == nil {
				err = c.Save()
			}
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"Lock", func(t *testing.T, hub string) {
			unlock, err := Lock(hub, "push", 0)
			if err != nil {
				t.Fatal(err)
			}
			wp04viewGitignore(t, hub) // already there while the lock is held
			unlock()
		}},
		{"TryLock", func(t *testing.T, hub string) {
			unlock, ok, err := TryLock(hub, "status")
			if err != nil || !ok {
				t.Fatalf("TryLock = %v, %v", ok, err)
			}
			unlock()
		}},
		{"EnsureSyncDir", func(t *testing.T, hub string) {
			if err := EnsureSyncDir(hub); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, fw := range firstWrites {
		t.Run(fw.name, func(t *testing.T) {
			hub := wp04viewHub(t)
			if _, err := os.Lstat(SyncDir(hub)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("precondition: %s exists (err %v)", SyncDir(hub), err)
			}
			fw.write(t, hub)
			wp04viewGitignore(t, hub)
		})
	}

	t.Run("git_check_ignore", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not on PATH")
		}
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

		root := t.TempDir()
		hub := filepath.Join(root, ".contexo")
		wp04viewWrite(t, hub, rel, page)
		wp04viewWrite(t, hub, "index.md", []byte("# Knowledge Index\n"))
		for _, args := range [][]string{
			{"init", "-q"},
			{"add", ".contexo"},
			{"commit", "-q", "-m", "commit .contexo"},
		} {
			if code, out := wp04viewGit(t, root, args...); code != 0 {
				t.Fatalf("git %q: exit %d\n%s", args, code, out)
			}
		}

		// Every kind of .sync content: a base, a sidecar, the indexes and a
		// lock (left held while git looks).
		b, err := OpenBases(hub)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Put(rel, page, wp04viewSB, time.Time{}); err != nil {
			t.Fatal(err)
		}
		if err := b.Save(); err != nil {
			t.Fatal(err)
		}
		c, err := OpenConflicts(hub)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Put(sidecar, page); err != nil {
			t.Fatal(err)
		}
		if err := c.Save(); err != nil {
			t.Fatal(err)
		}
		unlock, err := Lock(hub, "push", 0)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()

		for _, p := range []string{
			".contexo/.sync/base/x.md",
			".contexo/.sync/base/" + rel,
			".contexo/.sync/conflicts/" + rel,
			".contexo/.sync/bases.json",
			".contexo/.sync/lock",
			".contexo/.sync/backup/" + rel + ".20261103T091530Z",
			".contexo/.sync/.gitignore",
		} {
			if code, out := wp04viewGit(t, root, "check-ignore", "-q", p); code != 0 {
				t.Errorf("git check-ignore %s: exit %d, want 0 (ignored)\n%s", p, code, out)
			}
		}
		// Control: the working page is not ignored, so the check above is
		// not passing because everything is ignored.
		if code, out := wp04viewGit(t, root, "check-ignore", "-q", ".contexo/"+rel); code != 1 {
			t.Errorf("git check-ignore .contexo/%s: exit %d, want 1 (not ignored)\n%s", rel, code, out)
		}
		code, out := wp04viewGit(t, root, "status", "--porcelain", "--untracked-files=all")
		if code != 0 || strings.TrimSpace(out) != "" {
			t.Errorf("git status after writes under .sync: exit %d, output %q; want clean", code, out)
		}
	})
}
