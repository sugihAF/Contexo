package fsutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// wp04JSONPayload returns a valid JSON document of at least n bytes whose
// string values are filled with fill.
func wp04JSONPayload(t *testing.T, n int, fill string) []byte {
	t.Helper()
	doc := map[string]string{
		"schema": "ctx.test.v1",
		"filler": strings.Repeat(fill, n),
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// wp04DirNames lists the entry names of dir.
func wp04DirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestWriteFileAtomic_NoTornReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	payloads := [2][]byte{
		wp04JSONPayload(t, 4096, "a"),
		wp04JSONPayload(t, 9000, "b"),
	}
	if len(payloads[0]) == len(payloads[1]) {
		t.Fatal("payloads must differ in length")
	}
	if err := WriteFileAtomic(path, payloads[1], 0o644); err != nil {
		t.Fatalf("initial write: %v", err)
	}

	const rewrites = 500
	done := make(chan struct{})
	writeErr := make(chan error, 1)
	go func() {
		defer close(done)
		for i := 0; i < rewrites; i++ {
			err := WriteFileAtomic(path, payloads[i%2], 0o644)
			// On Windows a rename over a file the reader holds open can
			// still fail after every retry; that write is lost, not torn.
			if err != nil && !(runtime.GOOS == "windows" && isRetryableRename(err)) {
				writeErr <- err
				return
			}
		}
	}()

	reads, readErrs, torn := 0, 0, 0
	var firstTorn []byte
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
		}
		data, err := os.ReadFile(path)
		if err != nil {
			readErrs++
			continue
		}
		reads++
		if !json.Valid(data) || (!bytes.Equal(data, payloads[0]) && !bytes.Equal(data, payloads[1])) {
			if torn == 0 {
				firstTorn = data
			}
			torn++
		}
	}
	select {
	case err := <-writeErr:
		t.Fatalf("writer: %v", err)
	default:
	}
	if torn > 0 {
		t.Fatalf("%d of %d reads saw a torn file; first had %d bytes", torn, reads, len(firstTorn))
	}
	if reads == 0 {
		t.Fatal("reader never completed a read")
	}
	if runtime.GOOS != "windows" {
		if readErrs > 0 {
			t.Fatalf("%d reads failed; the file must always exist", readErrs)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, payloads[(rewrites-1)%2]) {
			t.Fatalf("final content: err=%v len=%d", err, len(got))
		}
	}
	if names := wp04DirNames(t, filepath.Dir(path)); len(names) != 1 {
		t.Fatalf("leftover files: %v", names)
	}
}

func TestWriteFileAtomicCreatesParentAndPerm(t *testing.T) {
	for _, perm := range []os.FileMode{0o600, 0o644, 0o666} {
		t.Run(perm.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "a", "b", "c", "x.json")
			data := []byte(`{"k":"v"}`)
			if err := WriteFileAtomic(path, data, perm); err != nil {
				t.Fatalf("WriteFileAtomic: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("content = %q, %v; want %q", got, err, data)
			}
			if runtime.GOOS == "windows" {
				return
			}
			fi, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			// Chmod is not subject to the umask, so 0o666 survives a 022 umask.
			if fi.Mode().Perm() != perm {
				t.Fatalf("perm = %v, want %v", fi.Mode().Perm(), perm)
			}
		})
	}

	t.Run("replace_existing_resets_perm", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "x.json")
		if err := os.WriteFile(path, []byte("old content that is longer"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := WriteFileAtomic(path, []byte("new"), 0o644); err != nil {
			t.Fatalf("WriteFileAtomic: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != "new" {
			t.Fatalf("content = %q, %v", got, err)
		}
		if runtime.GOOS == "windows" {
			return
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o644 {
			t.Fatalf("perm = %v, want 0644", fi.Mode().Perm())
		}
	})
}

func TestWriteFileAtomicTempNameNeverMD(t *testing.T) {
	tempName := regexp.MustCompile(`^\.x\.md\.tmp-`)
	data := []byte("---\ntitle: x\n---\n# x\n")

	t.Run("rename_sees_complete_temp", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "x.md")
		var seen []string
		rename := func(oldpath, newpath string) error {
			seen = append(seen, oldpath)
			if newpath != path {
				t.Errorf("rename target = %q, want %q", newpath, path)
			}
			if filepath.Dir(oldpath) != dir {
				t.Errorf("temp %q is not in the target dir %q", oldpath, dir)
			}
			base := filepath.Base(oldpath)
			if !tempName.MatchString(base) || strings.HasSuffix(base, ".md") {
				t.Errorf("temp name %q must match %s and never end in .md", base, tempName)
			}
			got, err := os.ReadFile(oldpath)
			if err != nil || !bytes.Equal(got, data) {
				t.Errorf("temp content before rename = %q, %v", got, err)
			}
			if runtime.GOOS != "windows" {
				if fi, err := os.Stat(oldpath); err != nil || fi.Mode().Perm() != 0o644 {
					t.Errorf("temp not chmod-ed before rename: %v %v", fi, err)
				}
			}
			return os.Rename(oldpath, newpath)
		}
		if err := writeFileAtomic(path, data, 0o644, rename); err != nil {
			t.Fatalf("writeFileAtomic: %v", err)
		}
		if len(seen) != 1 {
			t.Fatalf("rename called %d times, want 1", len(seen))
		}
		if names := wp04DirNames(t, dir); len(names) != 1 || names[0] != "x.md" {
			t.Fatalf("dir = %v, want [x.md]", names)
		}
	})

	t.Run("failed_rename_removes_temp", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "x.md")
		boom := errors.New("boom")
		rename := func(oldpath, newpath string) error {
			if !tempName.MatchString(filepath.Base(oldpath)) {
				t.Errorf("temp name %q", filepath.Base(oldpath))
			}
			return boom
		}
		if err := writeFileAtomic(path, data, 0o644, rename); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
		if names := wp04DirNames(t, dir); len(names) != 0 {
			t.Fatalf("dir = %v, want empty", names)
		}
	})
}

func TestRenameRetry(t *testing.T) {
	// wp04AccessDenied stands in for ERROR_ACCESS_DENIED from MoveFileEx.
	wp04AccessDenied := &os.LinkError{Op: "rename", Old: "old", New: "new", Err: syscall.Errno(5)}
	retryable := func(err error) bool { return errors.Is(err, syscall.Errno(5)) }

	type result struct {
		calls  int
		sleeps []time.Duration
		err    error
	}
	run := func(failures int, failErr error) result {
		var r result
		rename := func(oldpath, newpath string) error {
			r.calls++
			if oldpath != "old" || newpath != "new" {
				t.Errorf("rename(%q, %q)", oldpath, newpath)
			}
			if r.calls <= failures {
				return failErr
			}
			return nil
		}
		sleep := func(d time.Duration) { r.sleeps = append(r.sleeps, d) }
		r.err = renameRetry(rename, retryable, sleep, "old", "new")
		return r
	}
	wantSleeps := func(t *testing.T, got []time.Duration, n int) {
		t.Helper()
		if len(got) != n {
			t.Fatalf("slept %d times (%v), want %d", len(got), got, n)
		}
		for _, d := range got {
			if d != 50*time.Millisecond {
				t.Fatalf("slept %v, want 50ms", d)
			}
		}
	}

	t.Run("four_failures_then_success", func(t *testing.T) {
		r := run(4, wp04AccessDenied)
		if r.err != nil || r.calls != 5 {
			t.Fatalf("err=%v calls=%d, want nil after 5 calls", r.err, r.calls)
		}
		wantSleeps(t, r.sleeps, 4)
	})
	t.Run("five_failures_return_error", func(t *testing.T) {
		r := run(100, wp04AccessDenied)
		if r.err != wp04AccessDenied || r.calls != 5 {
			t.Fatalf("err=%v calls=%d, want access-denied after 5 calls", r.err, r.calls)
		}
		wantSleeps(t, r.sleeps, 4)
	})
	t.Run("non_retryable_fails_once", func(t *testing.T) {
		other := errors.New("not retryable")
		r := run(100, other)
		if r.err != other || r.calls != 1 {
			t.Fatalf("err=%v calls=%d, want the error after 1 call", r.err, r.calls)
		}
		wantSleeps(t, r.sleeps, 0)
	})
	t.Run("success_first_try", func(t *testing.T) {
		r := run(0, nil)
		if r.err != nil || r.calls != 1 {
			t.Fatalf("err=%v calls=%d", r.err, r.calls)
		}
		wantSleeps(t, r.sleeps, 0)
	})
}

func TestWP04IsRetryableRename(t *testing.T) {
	wrap := func(errno syscall.Errno) error {
		return &os.LinkError{Op: "rename", Old: "a", New: "b", Err: errno}
	}
	cases := []struct {
		name        string
		err         error
		wantWindows bool
	}{
		{"access_denied", wrap(5), true},
		{"sharing_violation", wrap(32), true},
		{"bare_access_denied", syscall.Errno(5), true},
		{"file_not_found", wrap(2), false},
		{"other", errors.New("x"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := c.wantWindows && runtime.GOOS == "windows"
			if got := isRetryableRename(c.err); got != want {
				t.Fatalf("isRetryableRename(%v) = %v, want %v on %s", c.err, got, want, runtime.GOOS)
			}
		})
	}
}

func TestEnsureSelfIgnoringDir(t *testing.T) {
	t.Run("fresh_dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "hub", ".sync")
		if err := EnsureSelfIgnoringDir(dir); err != nil {
			t.Fatalf("EnsureSelfIgnoringDir: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
		if err != nil || string(got) != "*\n" {
			t.Fatalf(".gitignore = %q, %v; want %q", got, err, "*\n")
		}
		if names := wp04DirNames(t, dir); len(names) != 1 {
			t.Fatalf("dir = %v, want only .gitignore", names)
		}
		if err := EnsureSelfIgnoringDir(dir); err != nil {
			t.Fatalf("second call: %v", err)
		}
	})

	t.Run("existing_dir_without_gitignore", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := EnsureSelfIgnoringDir(dir); err != nil {
			t.Fatalf("EnsureSelfIgnoringDir: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
		if err != nil || string(got) != "*\n" {
			t.Fatalf(".gitignore = %q, %v", got, err)
		}
	})

	t.Run("existing_gitignore_untouched", func(t *testing.T) {
		dir := t.TempDir()
		gi := filepath.Join(dir, ".gitignore")
		if err := os.WriteFile(gi, []byte("keep\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
		if err := os.Chtimes(gi, old, old); err != nil {
			t.Fatal(err)
		}
		if err := EnsureSelfIgnoringDir(dir); err != nil {
			t.Fatalf("EnsureSelfIgnoringDir: %v", err)
		}
		got, err := os.ReadFile(gi)
		if err != nil || string(got) != "keep\n" {
			t.Fatalf(".gitignore = %q, %v; want it unchanged", got, err)
		}
		fi, err := os.Stat(gi)
		if err != nil {
			t.Fatal(err)
		}
		if !fi.ModTime().Equal(old) {
			t.Fatalf("mtime = %v, want %v", fi.ModTime(), old)
		}
		if names := wp04DirNames(t, dir); len(names) != 1 {
			t.Fatalf("dir = %v, want only .gitignore", names)
		}
	})

	t.Run("dir_is_a_file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(dir, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := EnsureSelfIgnoringDir(dir); err == nil {
			t.Fatal("want an error when dir is a regular file")
		}
	})
}

func TestWP04WriteFileAtomicCleansTempOnFailure(t *testing.T) {
	t.Run("target_is_directory", func(t *testing.T) {
		parent := t.TempDir()
		target := filepath.Join(parent, "target")
		if err := os.MkdirAll(filepath.Join(target, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := WriteFileAtomic(target, []byte("data"), 0o644); err == nil {
			t.Fatal("want an error when the target is a directory")
		}
		names := wp04DirNames(t, parent)
		if len(names) != 1 || names[0] != "target" {
			t.Fatalf("parent = %v, want only target (no .target.tmp-*)", names)
		}
		if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
			t.Fatalf("target directory disturbed: %v %v", fi, err)
		}
	})

	t.Run("parent_is_a_file", func(t *testing.T) {
		parent := t.TempDir()
		blocker := filepath.Join(parent, "blocker")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := WriteFileAtomic(filepath.Join(blocker, "x.json"), []byte("{}"), 0o644); err == nil {
			t.Fatal("want an error when the parent is a regular file")
		}
		if names := wp04DirNames(t, parent); len(names) != 1 {
			t.Fatalf("parent = %v, want only blocker", names)
		}
	})
}
