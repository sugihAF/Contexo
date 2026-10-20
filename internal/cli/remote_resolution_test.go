package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/ui"
)

// wp03Token is the bearer every resolution test stores in the credentials.
const wp03Token = "ctxp_wp03_trap_token"

// wp03IsolateHome points HOME and USERPROFILE (os.UserHomeDir on Windows) at
// a temp dir, so no test can read the developer's real global credentials
// and send them anywhere. t.Setenv forbids t.Parallel in the caller.
func wp03IsolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// wp03Hit is one request the trap server A received.
type wp03Hit struct {
	Method, Path, Auth string
}

// wp03Trap is a pair of servers: A answers every route generically, so the
// wave-1 rewrites that call new routes keep passing; B fails the test on any
// request. The credentials point at A and the workspace config at B.
type wp03Trap struct {
	A, B *httptest.Server

	mu   gosync.Mutex
	hits []wp03Hit
}

func wp03TrapServers(t *testing.T) *wp03Trap {
	t.Helper()
	tr := &wp03Trap{}
	tr.A = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr.mu.Lock()
		tr.hits = append(tr.hits, wp03Hit{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization")})
		tr.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/pages"):
			_, _ = w.Write([]byte(`{"pages":[]}`))
		case r.Method == http.MethodGet && strings.Contains(p, "/pages/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"page not found","code":"page_not_found"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/sync/push"):
			_, _ = w.Write([]byte(`{"pushed":[],"conflicts":[]}`))
		case r.Method == http.MethodGet && strings.HasSuffix(p, "/sync/pull"):
			_, _ = w.Write([]byte(`{"new_head":"","files":[]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	tr.B = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("workspace server B received %s %s (Authorization %q); requests must go to the credentials' server",
			r.Method, r.URL.Path, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(tr.A.Close)
	t.Cleanup(tr.B.Close)
	return tr
}

// Hits returns a copy of the requests A received so far.
func (tr *wp03Trap) Hits() []wp03Hit {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]wp03Hit(nil), tr.hits...)
}

// Reset forgets A's recorded hits, so one test can check several commands.
func (tr *wp03Trap) Reset() {
	tr.mu.Lock()
	tr.hits = nil
	tr.mu.Unlock()
}

// AssertOnlyA asserts A received at least one request carrying
// 'Bearer <token>'. B fails the test by itself on any request.
func (tr *wp03Trap) AssertOnlyA(t *testing.T, label, token string) {
	t.Helper()
	for _, h := range tr.Hits() {
		if h.Auth == "Bearer "+token {
			return
		}
	}
	t.Errorf("%s: server A received no request with 'Bearer %s'; hits: %+v", label, token, tr.Hits())
}

// wp03Project builds a project whose GLOBAL credentials belong to A while its
// workspace config points at B, with one concept page "foo" so slug commands
// resolve. HOME is isolated. It returns the project root.
func wp03Project(t *testing.T, tr *wp03Trap) string {
	t.Helper()
	wp03IsolateHome(t)
	root := t.TempDir()
	if err := config.Save(root, &config.Config{Version: 1, ServerURL: tr.B.URL, RepoID: "wp03-repo"}); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveGlobalCredentials(&config.Credentials{
		Token: wp03Token, ServerURL: tr.A.URL, UserName: "wp03", UserEmail: "wp03@example.test",
	}); err != nil {
		t.Fatal(err)
	}
	wp03WritePage(t, root, "foo")
	return root
}

// wp03WritePage writes a valid concept page into root's .contexo/.
func wp03WritePage(t *testing.T, root, slug string) {
	t.Helper()
	store, err := pagestore.Open(config.ContexoDirPath(root))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	page := &schema.Page{
		Frontmatter: schema.PageFrontmatter{
			Schema: schema.PageSchemaV1, Slug: slug, Type: schema.TypeConcept,
			Author: "wp03", Created: now, Updated: now, Tags: []string{"wp03"},
		},
		Body: "## Decision\nTrap.\n",
	}
	if err := store.Write(page); err != nil {
		t.Fatal(err)
	}
}

// wp03Run executes ctx with args (prefixed with --root root when root is
// non-empty) and returns its stdout, stderr and error. It restores the
// package-level rootDir that the --root flag writes.
func wp03Run(t *testing.T, root string, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("CONTEXO_NO_UPDATE_CHECK", "1")
	prev := rootDir
	t.Cleanup(func() { rootDir = prev })
	if root != "" {
		args = append([]string{"--root", root}, args...)
	}
	var out, errBuf bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errBuf.String(), err
}

// wp03RemoteCode returns the RemoteError code in err's chain, or "".
func wp03RemoteCode(err error) string {
	var re *config.RemoteError
	if errors.As(err, &re) {
		return re.ErrorCode()
	}
	return ""
}

func TestOpenRemoteErrorsKeepCode(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T, root string)
		needRepo bool
		code     string
		advice   []string
	}{
		{
			name:  "no credentials",
			setup: func(t *testing.T, root string) {},
			code:  "not_authenticated", advice: []string{"ctx auth login"},
		},
		{
			name: "global credentials without server_url",
			setup: func(t *testing.T, root string) {
				if err := config.Save(root, &config.Config{Version: 1, ServerURL: "https://b.example.test", RepoID: "r"}); err != nil {
					t.Fatal(err)
				}
				if err := config.SaveGlobalCredentials(&config.Credentials{Token: wp03Token}); err != nil {
					t.Fatal(err)
				}
			},
			needRepo: true,
			code:     "server_unresolved", advice: []string{"ctx auth login --server <url>"},
		},
		{
			name: "no repo",
			setup: func(t *testing.T, root string) {
				if err := config.SaveGlobalCredentials(&config.Credentials{Token: wp03Token, ServerURL: "https://a.example.test"}); err != nil {
					t.Fatal(err)
				}
			},
			needRepo: true,
			code:     "repo_not_configured", advice: []string{"ctx remote set-repo <id>"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wp03IsolateHome(t)
			root := t.TempDir()
			c.setup(t, root)

			rr, client, err := openRemote(root, "members", c.needRepo)
			if err == nil {
				t.Fatalf("openRemote: want %s, got nil error", c.code)
			}
			if client != nil {
				t.Error("openRemote returned a client together with an error")
			}
			if rr == nil {
				t.Error("openRemote must return the resolved remote with a RequireAuth/RequireRepo error")
			}
			var re *config.RemoteError
			if !errors.As(err, &re) {
				t.Fatalf("errors.As found no *config.RemoteError in %v (%T)", err, err)
			}
			if re.ErrorCode() != c.code {
				t.Errorf("code = %q, want %q", re.ErrorCode(), c.code)
			}
			var ce *ui.CLIError
			if !errors.As(err, &ce) {
				t.Fatalf("error is not wrapped in ui.Suggest: %T", err)
			}
			if !reflect.DeepEqual(ce.Advice, c.advice) {
				t.Errorf("advice = %q, want %q", ce.Advice, c.advice)
			}
			if !strings.HasPrefix(err.Error(), "members: ") {
				t.Errorf("message %q is not prefixed with the command name", err.Error())
			}
		})
	}

	t.Run("no repo is fine when not needed", func(t *testing.T) {
		wp03IsolateHome(t)
		root := t.TempDir()
		if err := config.SaveGlobalCredentials(&config.Credentials{Token: wp03Token, ServerURL: "https://a.example.test"}); err != nil {
			t.Fatal(err)
		}
		rr, client, err := openRemote(root, "join", false)
		if err != nil || client == nil || rr == nil {
			t.Fatalf("openRemote(needRepo=false) = %v, %v, %v", rr, client, err)
		}
		if rr.ServerURL != "https://a.example.test" || rr.Token != wp03Token {
			t.Errorf("resolved %q/%q", rr.ServerURL, rr.Token)
		}
	})

	t.Run("corrupt config is an error without a remote", func(t *testing.T) {
		wp03IsolateHome(t)
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".contexo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.ContexoConfigPath(root), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		rr, client, err := openRemote(root, "members", true)
		if err == nil || rr != nil || client != nil {
			t.Fatalf("openRemote = %v, %v, %v; want only an error", rr, client, err)
		}
		if !strings.HasPrefix(err.Error(), "members: ") {
			t.Errorf("message %q is not prefixed with the command name", err.Error())
		}
	})
}

// wp03UnresolvedProject builds a project whose GLOBAL credentials carry a
// token but no server_url while its workspace config points at B. Such
// credentials are unresolved (contract §5, D5): nothing may be sent, least of
// all to B. HOME is isolated. It returns the project root.
func wp03UnresolvedProject(t *testing.T, tr *wp03Trap) string {
	t.Helper()
	wp03IsolateHome(t)
	root := t.TempDir()
	if err := config.Save(root, &config.Config{Version: 1, ServerURL: tr.B.URL, RepoID: "wp03-repo"}); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveGlobalCredentials(&config.Credentials{Token: wp03Token}); err != nil {
		t.Fatal(err)
	}
	wp03WritePage(t, root, "foo")
	return root
}

// wp03AssertNothingSent asserts A received no request. B fails the test by
// itself on any request.
func wp03AssertNothingSent(t *testing.T, tr *wp03Trap, label string) {
	t.Helper()
	if hits := tr.Hits(); len(hits) != 0 {
		t.Errorf("%s: unresolved credentials still reached server A: %+v", label, hits)
	}
}

func TestLogHistoryDiffEvolutionUseResolvedServer(t *testing.T) {
	tr := wp03TrapServers(t)
	root := wp03Project(t, tr)
	for _, args := range [][]string{{"log"}, {"history", "foo"}, {"diff", "foo"}, {"evolution", "foo"}} {
		tr.Reset()
		// A's generic bodies may not satisfy the command; only the host matters.
		_, _, _ = wp03Run(t, root, args...)
		tr.AssertOnlyA(t, strings.Join(args, " "), wp03Token)
	}
}

func TestMembersActivityInviteJoinUseResolvedServer(t *testing.T) {
	tr := wp03TrapServers(t)
	root := wp03Project(t, tr)
	// join runs last: it rewrites the config's repo_id from A's answer.
	for _, args := range [][]string{{"members"}, {"activity"}, {"invite", "list"}, {"join", "ctxi_wp03key"}} {
		tr.Reset()
		_, _, _ = wp03Run(t, root, args...)
		tr.AssertOnlyA(t, strings.Join(args, " "), wp03Token)
	}
}

func TestRemoteSetRepoPickerUsesResolvedServer(t *testing.T) {
	tr := wp03TrapServers(t)
	root := wp03Project(t, tr)

	var buf bytes.Buffer
	_, err := pickRepo(root, "set-repo", strings.NewReader(""), &buf)
	// A lists no repos, so the picker has nothing to offer.
	if err == nil || !strings.Contains(err.Error(), "no repos available") {
		t.Errorf("pickRepo error = %v, want 'no repos available' from A's empty list", err)
	}
	tr.AssertOnlyA(t, "set-repo picker", wp03Token)
}

func TestGlobalCredsWithoutServerNeverReachWorkspaceURL(t *testing.T) {
	tr := wp03TrapServers(t)
	root := wp03UnresolvedProject(t, tr)

	for _, args := range [][]string{
		{"log"}, {"history", "foo"}, {"diff", "foo"}, {"evolution", "foo"},
		{"members"}, {"activity"}, {"invite", "list"}, {"join", "ctxi_k"},
	} {
		label := strings.Join(args, " ")
		_, _, err := wp03Run(t, root, args...)
		if code := wp03RemoteCode(err); code != "server_unresolved" {
			t.Errorf("%s: error code = %q (err: %v), want server_unresolved", label, code, err)
		}
	}

	var buf bytes.Buffer
	_, err := pickRepo(root, "set-repo", strings.NewReader(""), &buf)
	if code := wp03RemoteCode(err); code != "server_unresolved" {
		t.Errorf("set-repo picker: error code = %q (err: %v), want server_unresolved", code, err)
	}
	wp03AssertNothingSent(t, tr, "global credentials without server_url")
}

func TestRemoteSetNotesCredentialsServer(t *testing.T) {
	const target = "https://c.example.test"
	advice := "ctx auth login --server " + target

	t.Run("credentials belong to another server", func(t *testing.T) {
		tr := wp03TrapServers(t)
		root := wp03Project(t, tr)
		stdout, stderr, err := wp03Run(t, root, "remote", "set", target)
		if err != nil {
			t.Fatalf("remote set: %v", err)
		}
		out := stdout + stderr
		if !strings.Contains(out, tr.A.URL) || strings.Count(out, advice) != 1 {
			t.Errorf("remote set must name the credentials' server %s and advise %q once:\n%s", tr.A.URL, advice, out)
		}
	})

	t.Run("global credentials without server_url", func(t *testing.T) {
		tr := wp03TrapServers(t)
		root := wp03UnresolvedProject(t, tr)
		stdout, stderr, err := wp03Run(t, root, "remote", "set", target)
		if err != nil {
			t.Fatalf("remote set: %v", err)
		}
		out := stdout + stderr
		if !strings.Contains(out, "no server URL") || strings.Count(out, advice) != 1 {
			t.Errorf("remote set must say the credentials have no server URL and advise %q once:\n%s", advice, out)
		}
	})

	t.Run("same server prints no note", func(t *testing.T) {
		tr := wp03TrapServers(t)
		root := wp03Project(t, tr)
		stdout, stderr, err := wp03Run(t, root, "remote", "set", tr.A.URL)
		if err != nil {
			t.Fatalf("remote set: %v", err)
		}
		if out := stdout + stderr; strings.Contains(out, "ctx auth login") {
			t.Errorf("remote set to the credentials' own server printed a note:\n%s", out)
		}
	})
}
