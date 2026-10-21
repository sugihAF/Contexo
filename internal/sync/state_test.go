package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"testing"
)

// wp04BigState returns a State with n page_shas entries of 40-hex shas.
func wp04BigState(n int, salt string) *State {
	s := &State{LastPullSHA: salt + "-head", PageSHAs: map[string]string{}}
	for i := 0; i < n; i++ {
		s.PageSHAs[fmt.Sprintf("wiki/concepts/page-%04d-%s.md", i, salt)] = fmt.Sprintf("%040x", i*7919+len(salt))
	}
	return s
}

// wp04IsParseErr reports whether err comes from decoding a torn or invalid
// state.json rather than from reading it.
func wp04IsParseErr(err error) bool {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	return errors.As(err, &syn) || errors.As(err, &typ)
}

// wp04IsWindowsBusy reports ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION,
// the rename failures fsutil retries on Windows.
func wp04IsWindowsBusy(err error) bool {
	return errors.Is(err, syscall.Errno(5)) || errors.Is(err, syscall.Errno(32))
}

// wp04SyncDirNames lists <hub>/.sync.
func wp04SyncDirNames(t *testing.T, hub string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(hub, ".sync"))
	if err != nil {
		t.Fatalf("read .sync: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestSaveStateIsAtomic(t *testing.T) {
	hub := filepath.Join(t.TempDir(), ".contexo")
	states := [2]*State{wp04BigState(300, "a"), wp04BigState(700, "b")}
	if err := SaveState(hub, states[1]); err != nil {
		t.Fatalf("initial SaveState: %v", err)
	}

	const iterations = 200
	done := make(chan struct{})
	writeErr := make(chan error, 1)
	go func() {
		defer close(done)
		for i := 0; i < iterations; i++ {
			if err := SaveState(hub, states[i%2]); err != nil {
				// A Windows rename can lose to a reader after every retry;
				// that save is lost, never torn.
				if runtime.GOOS == "windows" && wp04IsWindowsBusy(err) {
					continue
				}
				writeErr <- err
				return
			}
		}
	}()

	reads, parseErrs, readErrs, mismatched := 0, 0, 0, 0
	var firstErr error
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
		}
		s, err := LoadState(hub)
		if err != nil {
			if wp04IsParseErr(err) {
				parseErrs++
				if firstErr == nil {
					firstErr = err
				}
			} else {
				readErrs++
			}
			continue
		}
		reads++
		if !reflect.DeepEqual(s, states[0]) && !reflect.DeepEqual(s, states[1]) {
			mismatched++
		}
	}
	select {
	case err := <-writeErr:
		t.Fatalf("SaveState: %v", err)
	default:
	}
	if parseErrs > 0 {
		t.Fatalf("%d LoadState calls hit a torn state.json (first: %v)", parseErrs, firstErr)
	}
	if mismatched > 0 {
		t.Fatalf("%d of %d reads returned neither saved state", mismatched, reads)
	}
	if reads == 0 {
		t.Fatal("reader never completed a LoadState")
	}
	if runtime.GOOS != "windows" && readErrs > 0 {
		t.Fatalf("%d LoadState calls failed to read state.json", readErrs)
	}
	if names := wp04SyncDirNames(t, hub); !reflect.DeepEqual(names, []string{".gitignore", "state.json"}) {
		t.Fatalf(".sync = %v, want [.gitignore state.json] (no temp files)", names)
	}
}

func TestSaveStateCreatesSyncGitignore(t *testing.T) {
	hub := filepath.Join(t.TempDir(), ".contexo")
	if err := SaveState(hub, &State{PageSHAs: map[string]string{}}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(hub, ".sync", ".gitignore"))
	if err != nil || string(got) != "*\n" {
		t.Fatalf(".sync/.gitignore = %q, %v; want %q", got, err, "*\n")
	}

	// A user-edited .gitignore is never rewritten by later saves.
	if err := os.WriteFile(filepath.Join(hub, ".sync", ".gitignore"), []byte("custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(hub, &State{LastPullSHA: "x"}); err != nil {
		t.Fatalf("second SaveState: %v", err)
	}
	got, err = os.ReadFile(filepath.Join(hub, ".sync", ".gitignore"))
	if err != nil || string(got) != "custom\n" {
		t.Fatalf(".sync/.gitignore = %q, %v; want it kept", got, err)
	}
}

func TestWP04SaveStateFormatUnchanged(t *testing.T) {
	hub := t.TempDir()
	s := &State{
		LastPullSHA: "abc123",
		PageSHAs: map[string]string{
			"wiki/entities/b.md": "s2",
			"wiki/concepts/a.md": "s1",
		},
	}
	if err := SaveState(hub, s); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := os.ReadFile(StatePath(hub))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n" +
		"  \"last_pull_sha\": \"abc123\",\n" +
		"  \"page_shas\": {\n" +
		"    \"wiki/concepts/a.md\": \"s1\",\n" +
		"    \"wiki/entities/b.md\": \"s2\"\n" +
		"  }\n" +
		"}"
	if string(got) != want {
		t.Fatalf("state.json =\n%s\nwant\n%s", got, want)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(StatePath(hub))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o644 {
			t.Fatalf("state.json perm = %v, want 0644", fi.Mode().Perm())
		}
	}
	back, err := LoadState(hub)
	if err != nil || !reflect.DeepEqual(back, s) {
		t.Fatalf("LoadState = %+v, %v; want %+v", back, err, s)
	}
}

func TestLoadStateOrEmptyCorrupt(t *testing.T) {
	writeState := func(t *testing.T, hub string, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(hub, ".sync"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(StatePath(hub), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wantEmpty := func(t *testing.T, s *State) {
		t.Helper()
		if s == nil || s.PageSHAs == nil || len(s.PageSHAs) != 0 || s.LastPullSHA != "" {
			t.Fatalf("state = %+v, want empty with a non-nil PageSHAs map", s)
		}
	}

	for _, c := range []struct{ name, data string }{
		{"garbage", "{not json"},
		{"empty_file", ""},
		{"truncated", `{"last_pull_sha":"abc","page_shas":{"wiki/concepts/x.md":"s`},
		{"json_array", `[]`},
		{"wrong_types", `{"last_pull_sha":"abc","page_shas":{"wiki/concepts/x.md":1}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			hub := t.TempDir()
			writeState(t, hub, c.data)
			s, corrupt, err := LoadStateOrEmpty(hub)
			if err != nil || !corrupt {
				t.Fatalf("corrupt=%v err=%v, want corrupt=true err=nil", corrupt, err)
			}
			wantEmpty(t, s)
			// LoadState keeps failing on the same file.
			if _, err := LoadState(hub); err == nil {
				t.Fatal("LoadState must still report the parse error")
			}
		})
	}

	t.Run("missing", func(t *testing.T) {
		hub := filepath.Join(t.TempDir(), ".contexo")
		s, corrupt, err := LoadStateOrEmpty(hub)
		if err != nil || corrupt {
			t.Fatalf("corrupt=%v err=%v, want false/nil", corrupt, err)
		}
		wantEmpty(t, s)
		if _, err := os.Stat(filepath.Join(hub, ".sync")); !os.IsNotExist(err) {
			t.Fatalf("LoadStateOrEmpty must not create .sync: %v", err)
		}
	})

	t.Run("valid", func(t *testing.T) {
		hub := t.TempDir()
		want := &State{LastPullSHA: "h1", PageSHAs: map[string]string{"wiki/concepts/x.md": "s1"}}
		if err := SaveState(hub, want); err != nil {
			t.Fatal(err)
		}
		s, corrupt, err := LoadStateOrEmpty(hub)
		if err != nil || corrupt || !reflect.DeepEqual(s, want) {
			t.Fatalf("got %+v corrupt=%v err=%v, want %+v", s, corrupt, err, want)
		}
	})

	t.Run("valid_without_page_shas", func(t *testing.T) {
		hub := t.TempDir()
		writeState(t, hub, `{"last_pull_sha":"h1"}`)
		s, corrupt, err := LoadStateOrEmpty(hub)
		if err != nil || corrupt {
			t.Fatalf("corrupt=%v err=%v", corrupt, err)
		}
		if s.LastPullSHA != "h1" || s.PageSHAs == nil || len(s.PageSHAs) != 0 {
			t.Fatalf("state = %+v, want h1 and an empty non-nil map", s)
		}
	})

	t.Run("json_null_matches_LoadState", func(t *testing.T) {
		hub := t.TempDir()
		writeState(t, hub, `null`)
		s, corrupt, err := LoadStateOrEmpty(hub)
		if err != nil || corrupt {
			t.Fatalf("corrupt=%v err=%v; LoadState accepts null, so it is not corrupt", corrupt, err)
		}
		wantEmpty(t, s)
	})

	t.Run("directory_at_state_path", func(t *testing.T) {
		hub := t.TempDir()
		if err := os.MkdirAll(StatePath(hub), 0o755); err != nil {
			t.Fatal(err)
		}
		s, corrupt, err := LoadStateOrEmpty(hub)
		if err == nil {
			t.Fatal("want an I/O error when state.json is a directory")
		}
		if s != nil || corrupt {
			t.Fatalf("state=%+v corrupt=%v, want nil/false with an error", s, corrupt)
		}
	})
}
