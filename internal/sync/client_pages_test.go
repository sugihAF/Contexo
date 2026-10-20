package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	stdsync "sync"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

const (
	wp01SHA1 = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
	wp01SHA2 = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c"
)

// wp01ClosedURL returns the URL of a TCP port that was just closed, so a
// request to it fails at dial time.
func wp01ClosedURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return "http://" + addr
}

// wp01UnsetClientEnv makes CONTEXO_CLIENT absent for the test.
func wp01UnsetClientEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CONTEXO_CLIENT", "")
	if err := os.Unsetenv("CONTEXO_CLIENT"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
}

// wp01Respond builds a server that answers every request with status, body
// and the given headers.
func wp01Respond(t *testing.T, status int, body string, hdr map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// wp01AsHTTPError asserts err carries an *HTTPError and returns it.
func wp01AsHTTPError(t *testing.T, err error) *HTTPError {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("error %T (%v) is not an *HTTPError", err, err)
	}
	return he
}

// wp01Method describes one Client method for the every-method tables.
type wp01Method struct {
	name  string
	op    string // NetworkError.Op, the legacy "sync: <op>: %w" label
	call  func(c *Client) error
	fail  string // legacy non-2xx Error() format (%d status, %s body)
	parse string // legacy garbled-2xx Error() prefix; "" when no body is parsed
}

func wp01AllMethods() []wp01Method {
	return []wp01Method{
		{"CreateRepo", "create repo", func(c *Client) error {
			return c.CreateRepo("team-repo")
		}, "sync: create repo (%d): %s", ""},
		{"PushPages", "push", func(c *Client) error {
			_, err := c.PushPages("team-repo", &PushRequest{Files: []PushFile{{Path: "wiki/concepts/a.md", Content: "x"}}})
			return err
		}, "sync: push failed (%d): %s", "sync: parse push response: "},
		{"PullPages", "pull", func(c *Client) error {
			_, err := c.PullPages("team-repo", "")
			return err
		}, "sync: pull failed (%d): %s", "sync: parse pull response: "},
		{"JoinRepo", "join", func(c *Client) error {
			_, _, err := c.JoinRepo("ctxi_example")
			return err
		}, "sync: join failed (%d): %s", "sync: parse join response: "},
		{"ListRepos", "list repos", func(c *Client) error {
			_, err := c.ListRepos()
			return err
		}, "sync: list repos (%d): %s", "sync: parse list repos: "},
		{"Timeline", "timeline", func(c *Client) error {
			_, err := c.Timeline("team-repo", 5)
			return err
		}, "sync: timeline (%d): %s", "sync: parse timeline: "},
		{"MintInviteKey", "mint invite key", func(c *Client) error {
			_, _, err := c.MintInviteKey("team-repo", "label")
			return err
		}, "sync: mint invite key failed (%d): %s", "sync: parse mint response: "},
		{"ListInviteKeys", "list invite keys", func(c *Client) error {
			_, err := c.ListInviteKeys("team-repo")
			return err
		}, "sync: list invite keys (%d): %s", "sync: parse list invite keys: "},
		{"ReadPage", "read page", func(c *Client) error {
			_, _, err := c.ReadPage("team-repo", "wiki/concepts/a.md")
			return err
		}, "sync: read page (%d): %s", ""},
		{"PageHistory", "page history", func(c *Client) error {
			_, err := c.PageHistory("team-repo", "wiki/concepts/a.md", 3)
			return err
		}, "sync: page history (%d): %s", "sync: parse history: "},
		{"PageEvolution", "page evolution", func(c *Client) error {
			_, err := c.PageEvolution("team-repo", "wiki/concepts/a.md", 3, true)
			return err
		}, "sync: page evolution (%d): %s", "sync: parse evolution: "},
		{"PageDiff", "page diff", func(c *Client) error {
			_, err := c.PageDiff("team-repo", "wiki/concepts/a.md", wp01SHA1, wp01SHA2, false)
			return err
		}, "sync: page diff (%d): %s", "sync: parse diff: "},
		{"DeleteInviteKey", "delete invite key", func(c *Client) error {
			return c.DeleteInviteKey("team-repo", "k1")
		}, "sync: delete invite key (%d): %s", ""},
		{"ListMembers", "list members", func(c *Client) error {
			_, err := c.ListMembers("team-repo")
			return err
		}, "sync: list members (%d): %s", "sync: parse list members: "},
		{"RemoveMember", "remove member", func(c *Client) error {
			return c.RemoveMember("team-repo", "u2")
		}, "sync: remove member (%d): %s", ""},
		{"ListActivity", "list activity", func(c *Client) error {
			_, _, err := c.ListActivity("team-repo", 10, 0)
			return err
		}, "sync: list activity (%d): %s", "sync: parse list activity: "},
		{"ServerDistill", "distill", func(c *Client) error {
			_, err := c.ServerDistill("team-repo", &DistillRequest{SessionID: "sess"})
			return err
		}, "sync: distill failed (%d): %s", "sync: parse distill response: "},
		{"ListPages", "list pages", func(c *Client) error {
			_, err := c.ListPages("team-repo")
			return err
		}, "sync: list pages (%d): %s", "sync: parse list pages: "},
		{"ReadPageAt", "read page at", func(c *Client) error {
			_, _, err := c.ReadPageAt("team-repo", "wiki/concepts/a.md", wp01SHA1)
			return err
		}, "sync: read page at (%d): %s", ""},
	}
}

func TestListPagesParsesWrapper(t *testing.T) {
	const listing = `{"pages":[
		{"path":"wiki/concepts/auth-flow.md","sha":"` + wp01SHA1 + `","author":"Alice","email":"alice@acme.dev","time":"2026-10-01T09:30:00Z","message":"auth flow"},
		{"path":"wiki/entities/billing.md","sha":"` + wp01SHA2 + `","author":"Bob","email":"bob@acme.dev","time":"2026-10-02T10:00:05Z","message":"billing"}
	]}`
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_, _ = w.Write([]byte(listing))
	}))
	defer srv.Close()

	pages, err := NewClient(srv.URL, "tok").ListPages("team-repo")
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/v1/repos/team-repo/pages" {
		t.Errorf("request = %s %s, want GET /v1/repos/team-repo/pages", gotMethod, gotPath)
	}
	want := []PageMeta{
		{Path: "wiki/concepts/auth-flow.md", SHA: wp01SHA1, Author: "Alice", Email: "alice@acme.dev",
			Time: time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC), Message: "auth flow"},
		{Path: "wiki/entities/billing.md", SHA: wp01SHA2, Author: "Bob", Email: "bob@acme.dev",
			Time: time.Date(2026, 10, 2, 10, 0, 5, 0, time.UTC), Message: "billing"},
	}
	if len(pages) != len(want) {
		t.Fatalf("got %d pages, want %d", len(pages), len(want))
	}
	for i := range want {
		if pages[i].Path != want[i].Path || pages[i].SHA != want[i].SHA || pages[i].Author != want[i].Author ||
			pages[i].Email != want[i].Email || !pages[i].Time.Equal(want[i].Time) || pages[i].Message != want[i].Message {
			t.Errorf("page[%d] = %+v, want %+v", i, pages[i], want[i])
		}
	}

	for _, body := range []string{`{"pages":null}`, `{"pages":[]}`, `{}`} {
		t.Run(body, func(t *testing.T) {
			srv := wp01Respond(t, 200, body, nil)
			pages, err := NewClient(srv.URL, "tok").ListPages("team-repo")
			if err != nil {
				t.Fatalf("ListPages: %v", err)
			}
			if pages == nil || len(pages) != 0 {
				t.Errorf("pages = %#v, want a non-nil empty slice", pages)
			}
			out, _ := json.Marshal(pages)
			if string(out) != "[]" {
				t.Errorf("marshalled pages = %s, want []", out)
			}
		})
	}
}

func TestListPagesRepoNotFound(t *testing.T) {
	for _, body := range []string{`{"error":"repo not found"}`, `{"error":"repo not found","code":"repo_not_found"}`} {
		t.Run(body, func(t *testing.T) {
			srv := wp01Respond(t, 404, body, map[string]string{"Content-Type": "application/json"})
			pages, err := NewClient(srv.URL, "tok").ListPages("team-repo")
			if pages != nil {
				t.Errorf("pages = %#v on error, want nil", pages)
			}
			he := wp01AsHTTPError(t, err)
			if he.Code != contract.CodeRepoNotFound || he.ErrorCode() != "repo_not_found" {
				t.Errorf("Code = %q, want repo_not_found", he.Code)
			}
			if !errors.Is(err, ErrRepoNotFound) {
				t.Error("errors.Is(ErrRepoNotFound) = false")
			}
			if errors.Is(err, ErrPageNotFound) || errors.Is(err, ErrServerUnsupported) {
				t.Error("repo_not_found must not match page/server sentinels")
			}
			if he.HTTPStatus() != 404 {
				t.Errorf("HTTPStatus() = %d, want 404", he.HTTPStatus())
			}
			if want := "sync: list pages (404): " + body; err.Error() != want {
				t.Errorf("Error() = %q, want %q", err.Error(), want)
			}
		})
	}
}

func TestListPagesNonJSON404IsServerUnsupported(t *testing.T) {
	srv := wp01Respond(t, 404, "404 page not found", map[string]string{"Content-Type": "text/plain"})
	_, err := NewClient(srv.URL, "tok").ListPages("team-repo")
	he := wp01AsHTTPError(t, err)
	if he.Code != contract.CodeServerUnsupported {
		t.Errorf("Code = %q, want server_unsupported", he.Code)
	}
	if !errors.Is(err, ErrServerUnsupported) {
		t.Error("errors.Is(ErrServerUnsupported) = false")
	}
	if errors.Is(err, ErrRepoNotFound) || errors.Is(err, ErrPageNotFound) {
		t.Error("a missing route must not look like a missing repo or page")
	}
	if he.Details != nil {
		t.Errorf("Details = %#v, want nil for a non-?at request", he.Details)
	}
	if want := "sync: list pages (404): 404 page not found"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

// wp01AtUnsupported asserts the old-server ?at error shape.
func wp01AtUnsupported(t *testing.T, err error, sha string) {
	t.Helper()
	he := wp01AsHTTPError(t, err)
	if !errors.Is(err, ErrServerUnsupported) {
		t.Error("errors.Is(ErrServerUnsupported) = false")
	}
	if he.ErrorCode() != "server_unsupported" {
		t.Errorf("ErrorCode() = %q, want server_unsupported", he.ErrorCode())
	}
	want := contract.ServerUnsupportedDetails{Missing: []contract.ServerCapability{contract.SrvPagesAt}}
	if !reflect.DeepEqual(he.Details, want) || !reflect.DeepEqual(he.ErrorDetails(), want) {
		t.Errorf("Details = %#v, want %#v", he.Details, want)
	}
	if he.HTTPStatus() != 200 {
		t.Errorf("HTTPStatus() = %d, want 200", he.HTTPStatus())
	}
	if wantMsg := "sync: read page at " + sha + ": server did not echo X-Contexo-At (no ?at support)"; err.Error() != wantMsg {
		t.Errorf("Error() = %q, want %q", err.Error(), wantMsg)
	}
}

func TestReadPageAtRequiresAtHeader(t *testing.T) {
	var gotPath, gotAt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAt = r.URL.Path, r.URL.Query().Get("at")
		// An old server ignores ?at and serves HEAD with no echo.
		w.Header().Set("X-Page-SHA", wp01SHA2)
		_, _ = w.Write([]byte("# HEAD content\n"))
	}))
	defer srv.Close()

	data, pageSHA, err := NewClient(srv.URL, "tok").ReadPageAt("team-repo", "wiki/concepts/rate limits.md", wp01SHA1)
	if gotPath != "/v1/repos/team-repo/pages/wiki/concepts/rate limits.md" || gotAt != wp01SHA1 {
		t.Errorf("request path=%q at=%q", gotPath, gotAt)
	}
	if data != nil || pageSHA != "" {
		t.Errorf("returned (%q, %q) with the error, want (nil, \"\")", data, pageSHA)
	}
	wp01AtUnsupported(t, err, wp01SHA1)

	// "Missing" means absent, not merely unequal: an empty sha must not let
	// an old server's HEAD content through because "" == Get(absent).
	data, pageSHA, err = NewClient(srv.URL, "tok").ReadPageAt("team-repo", "wiki/concepts/a.md", "")
	if data != nil || pageSHA != "" {
		t.Errorf("empty sha: returned (%q, %q) with the error", data, pageSHA)
	}
	wp01AtUnsupported(t, err, "")
}

func TestReadPageAtHeaderMismatch(t *testing.T) {
	srv := wp01Respond(t, 200, "# other content\n", map[string]string{
		"X-Contexo-At": wp01SHA2,
		"X-Page-SHA":   wp01SHA2,
	})
	data, pageSHA, err := NewClient(srv.URL, "tok").ReadPageAt("team-repo", "wiki/concepts/a.md", wp01SHA1)
	if data != nil || pageSHA != "" {
		t.Errorf("returned (%q, %q) with the error, want (nil, \"\")", data, pageSHA)
	}
	wp01AtUnsupported(t, err, wp01SHA1)

	// Case or whitespace differences are a mismatch too: the echo is exact.
	srv2 := wp01Respond(t, 200, "x", map[string]string{"X-Contexo-At": strings.ToUpper(wp01SHA1)})
	_, _, err = NewClient(srv2.URL, "tok").ReadPageAt("team-repo", "wiki/concepts/a.md", wp01SHA1)
	wp01AtUnsupported(t, err, wp01SHA1)
}

func TestReadPageAtShaNotFound(t *testing.T) {
	cases := []struct{ name, body, serverCode string }{
		{"body code", `{"error":"sha not found","code":"sha_not_found"}`, "sha_not_found"},
		{"codeless JSON", `{"error":"path not found at sha"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := wp01Respond(t, 404, tc.body, map[string]string{"Content-Type": "application/json"})
			data, pageSHA, err := NewClient(srv.URL, "tok").ReadPageAt("team-repo", "wiki/concepts/a.md", wp01SHA1)
			if data != nil || pageSHA != "" {
				t.Errorf("returned (%q, %q) with the error", data, pageSHA)
			}
			he := wp01AsHTTPError(t, err)
			if he.Code != contract.CodeSHANotFound {
				t.Errorf("Code = %q, want sha_not_found", he.Code)
			}
			if he.ServerCode != tc.serverCode {
				t.Errorf("ServerCode = %q, want %q", he.ServerCode, tc.serverCode)
			}
			if !errors.Is(err, ErrSHANotFound) {
				t.Error("errors.Is(ErrSHANotFound) = false")
			}
			if errors.Is(err, ErrPageNotFound) || errors.Is(err, ErrRepoNotFound) {
				t.Error("sha_not_found must not match page/repo sentinels")
			}
			if want := "sync: read page at (404): " + tc.body; err.Error() != want {
				t.Errorf("Error() = %q, want %q", err.Error(), want)
			}
		})
	}

	t.Run("repo not found wins over at", func(t *testing.T) {
		srv := wp01Respond(t, 404, `{"error":"repo not found"}`, nil)
		_, _, err := NewClient(srv.URL, "tok").ReadPageAt("team-repo", "wiki/concepts/a.md", wp01SHA1)
		if he := wp01AsHTTPError(t, err); he.Code != contract.CodeRepoNotFound {
			t.Errorf("Code = %q, want repo_not_found", he.Code)
		}
	})

	t.Run("non-JSON 404 is server_unsupported pages.at", func(t *testing.T) {
		srv := wp01Respond(t, 404, "404 page not found", nil)
		_, _, err := NewClient(srv.URL, "tok").ReadPageAt("team-repo", "wiki/concepts/a.md", wp01SHA1)
		he := wp01AsHTTPError(t, err)
		if he.Code != contract.CodeServerUnsupported || !errors.Is(err, ErrServerUnsupported) {
			t.Errorf("Code = %q, want server_unsupported", he.Code)
		}
		want := contract.ServerUnsupportedDetails{Missing: []contract.ServerCapability{contract.SrvPagesAt}}
		if !reflect.DeepEqual(he.Details, want) {
			t.Errorf("Details = %#v, want %#v", he.Details, want)
		}
	})
}

func TestReadPageAtReturnsPageSHA(t *testing.T) {
	var gotRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		w.Header().Set("X-Contexo-At", r.URL.Query().Get("at"))
		w.Header().Set("X-Page-SHA", wp01SHA2)
		_, _ = w.Write([]byte("# Auth flow\n\nas of the requested sha\n"))
	}))
	defer srv.Close()

	data, pageSHA, err := NewClient(srv.URL, "tok").ReadPageAt("team-repo", "wiki/concepts/auth-flow.md", wp01SHA1)
	if err != nil {
		t.Fatalf("ReadPageAt: %v", err)
	}
	if string(data) != "# Auth flow\n\nas of the requested sha\n" {
		t.Errorf("data = %q", data)
	}
	if pageSHA != wp01SHA2 {
		t.Errorf("X-Page-SHA = %q, want %q", pageSHA, wp01SHA2)
	}
	if gotRawQuery != "at="+wp01SHA1 {
		t.Errorf("raw query = %q, want at=%s", gotRawQuery, wp01SHA1)
	}
}

func TestServerInfoFromHeaders(t *testing.T) {
	hdr := map[string]string{
		"X-Contexo-Server-Version": "v0.7.0",
		"X-Contexo-Capabilities":   " pages.at, push.expect_absent,,write.path_sha , future.cap ",
	}
	wantCaps := []contract.ServerCapability{contract.SrvPagesAt, contract.SrvExpectAbsent, contract.SrvWritePathSHA, "future.cap"}

	t.Run("success response", func(t *testing.T) {
		srv := wp01Respond(t, 200, `{"pages":[]}`, hdr)
		c := NewClient(srv.URL, "tok")
		if _, err := c.ListPages("team-repo"); err != nil {
			t.Fatalf("ListPages: %v", err)
		}
		v, caps, known := c.ServerInfo()
		if v != "v0.7.0" || !known || !reflect.DeepEqual(caps, wantCaps) {
			t.Fatalf("ServerInfo() = (%q, %v, %v), want (v0.7.0, %v, true)", v, caps, known, wantCaps)
		}
		// The result is a copy: mutating it never changes the client.
		caps[0] = "mutated"
		if _, caps2, _ := c.ServerInfo(); !reflect.DeepEqual(caps2, wantCaps) {
			t.Errorf("ServerInfo caps changed after caller mutation: %v", caps2)
		}
	})

	t.Run("error responses carry the headers too", func(t *testing.T) {
		for _, status := range []int{401, 404, 429, 500} {
			srv := wp01Respond(t, status, `{"error":"x"}`, hdr)
			c := NewClient(srv.URL, "tok")
			if _, err := c.ListPages("team-repo"); err == nil {
				t.Fatalf("status %d: expected an error", status)
			}
			v, caps, known := c.ServerInfo()
			if v != "v0.7.0" || !known || !reflect.DeepEqual(caps, wantCaps) {
				t.Errorf("status %d: ServerInfo() = (%q, %v, %v)", status, v, caps, known)
			}
		}
	})

	t.Run("any method captures", func(t *testing.T) {
		for _, m := range wp01AllMethods() {
			srv := wp01Respond(t, 200, `{}`, hdr)
			c := NewClient(srv.URL, "tok")
			_ = m.call(c)
			if _, caps, known := c.ServerInfo(); !known || !reflect.DeepEqual(caps, wantCaps) {
				t.Errorf("%s: ServerInfo() caps=%v known=%v", m.name, caps, known)
			}
		}
	})
}

func TestServerInfoOldServerKnownNoCaps(t *testing.T) {
	srv := wp01Respond(t, 200, `{"pages":[]}`, nil)
	c := NewClient(srv.URL, "tok")
	if _, err := c.ListPages("team-repo"); err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	v, caps, known := c.ServerInfo()
	if v != "" || !known || caps == nil || len(caps) != 0 {
		t.Fatalf("ServerInfo() = (%q, %#v, %v), want (\"\", [] non-nil, true)", v, caps, known)
	}

	// The most recent response wins: a new server followed by an old one
	// (e.g. a load balancer mid-rollout) reports the old one.
	var n int
	var mu stdsync.Mutex
	mixed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		if first {
			w.Header().Set("X-Contexo-Server-Version", "v0.7.0")
			w.Header().Set("X-Contexo-Capabilities", "pages.at")
		}
		_, _ = w.Write([]byte(`{"pages":[]}`))
	}))
	defer mixed.Close()
	c2 := NewClient(mixed.URL, "tok")
	_, _ = c2.ListPages("team-repo")
	if _, caps, _ := c2.ServerInfo(); len(caps) != 1 {
		t.Fatalf("after new server: caps = %v", caps)
	}
	_, _ = c2.ListPages("team-repo")
	if v, caps, known := c2.ServerInfo(); v != "" || caps == nil || len(caps) != 0 || !known {
		t.Errorf("after old server: ServerInfo() = (%q, %#v, %v)", v, caps, known)
	}
}

func TestServerInfoBeforeAnyRequest(t *testing.T) {
	c := NewClient("https://example.test", "tok")
	v, caps, known := c.ServerInfo()
	if v != "" || known || caps == nil || len(caps) != 0 {
		t.Fatalf("ServerInfo() = (%q, %#v, %v), want (\"\", [] non-nil, false)", v, caps, known)
	}

	// A transport failure is not a server answer.
	c2 := NewClient(wp01ClosedURL(t), "tok")
	if _, err := c2.ListPages("team-repo"); err == nil {
		t.Fatal("expected a network error")
	}
	if v, caps, known := c2.ServerInfo(); v != "" || known || caps == nil || len(caps) != 0 {
		t.Errorf("after a network error: ServerInfo() = (%q, %#v, %v)", v, caps, known)
	}
}

// TestServerInfoConcurrent: status drift calls the client from up to 8
// goroutines; header capture and the client name must be race-free (-race).
func TestServerInfoConcurrent(t *testing.T) {
	srv := wp01Respond(t, 200, `{"pages":[]}`, map[string]string{
		"X-Contexo-Server-Version": "v0.7.0",
		"X-Contexo-Capabilities":   "pages.at,push.expect_absent,write.path_sha",
	})
	c := NewClient(srv.URL, "tok")
	var wg stdsync.WaitGroup
	errs := make(chan error, 8*10)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if g%2 == 0 {
					c.SetClientName(fmt.Sprintf("agent-%d", g))
				}
				if _, err := c.ListPages("team-repo"); err != nil {
					errs <- err
					return
				}
				_, caps, known := c.ServerInfo()
				if known && len(caps) > 0 {
					caps[0] = "scribble" // must not corrupt the client's copy
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("ListPages: %v", err)
	}
	v, caps, known := c.ServerInfo()
	want := []contract.ServerCapability{contract.SrvPagesAt, contract.SrvExpectAbsent, contract.SrvWritePathSHA}
	if v != "v0.7.0" || !known || !reflect.DeepEqual(caps, want) {
		t.Errorf("ServerInfo() = (%q, %v, %v)", v, caps, known)
	}
}

func TestHTTPErrorCodes(t *testing.T) {
	const quotaBody = `{"error":"Free tier is limited to 5 repos.","code":"quota_repos","limit":5,"upgrade_url":"https://contexo.live/#pricing"}`
	readPage := func(c *Client) error { _, _, err := c.ReadPage("team-repo", "wiki/concepts/a.md"); return err }
	readAt := func(c *Client) error {
		_, _, err := c.ReadPageAt("team-repo", "wiki/concepts/a.md", wp01SHA1)
		return err
	}
	listPages := func(c *Client) error { _, err := c.ListPages("team-repo"); return err }
	push := func(c *Client) error {
		_, err := c.PushPages("team-repo", &PushRequest{Files: []PushFile{{Path: "wiki/concepts/a.md", Content: "x"}}})
		return err
	}
	cases := []struct {
		name    string
		status  int
		body    string
		call    func(c *Client) error
		code    contract.ErrorCode
		is      []error
		details any
	}{
		{"400 invalid_path", 400, `{"error":"invalid path","code":"invalid_path"}`, readPage, contract.CodeInvalidPath, nil, nil},
		{"400 invalid_sha is usage", 400, `{"error":"invalid sha","code":"invalid_sha"}`, readAt, contract.CodeUsage, nil, nil},
		{"401", 401, `{"error":"bad token"}`, listPages, contract.CodeAuthRejected, nil, nil},
		{"402 quota", 402, quotaBody, push, contract.CodeQuotaExceeded, nil,
			contract.QuotaDetails{QuotaCode: "quota_repos", Limit: 5, UpgradeURL: "https://contexo.live/#pricing"}},
		{"403", 403, `{"error":"not a member of this repo"}`, listPages, contract.CodeForbidden, nil, nil},
		{"404 body page_not_found", 404, `{"error":"page not found","code":"page_not_found"}`, readPage, contract.CodePageNotFound, []error{ErrPageNotFound}, nil},
		{"404 body repo_not_found", 404, `{"error":"repo not found","code":"repo_not_found"}`, readAt, contract.CodeRepoNotFound, []error{ErrRepoNotFound}, nil},
		{"404 body sha_not_found", 404, `{"error":"sha not found","code":"sha_not_found"}`, readAt, contract.CodeSHANotFound, []error{ErrSHANotFound}, nil},
		{"404 codeless repo", 404, `{"error":"repo not found"}`, listPages, contract.CodeRepoNotFound, []error{ErrRepoNotFound}, nil},
		{"404 codeless page", 404, `{"error":"page not found"}`, readPage, contract.CodePageNotFound, []error{ErrPageNotFound}, nil},
		{"404 codeless at", 404, `{"error":"not found"}`, readAt, contract.CodeSHANotFound, []error{ErrSHANotFound}, nil},
		{"404 non-JSON", 404, `404 page not found`, listPages, contract.CodeServerUnsupported, []error{ErrServerUnsupported}, nil},
		{"409 unexpected is internal", 409, `{"error":"conflict"}`, listPages, contract.CodeInternal, nil, nil},
		{"429", 429, `{"error":"rate limited"}`, listPages, contract.CodeRateLimited, nil, nil},
		{"500", 500, `boom`, listPages, contract.CodeServerError, nil, nil},
		{"502", 502, `<html>bad gateway</html>`, readPage, contract.CodeServerError, nil, nil},
		{"garbled 200", 200, `garbage{`, listPages, contract.CodeServerError, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := wp01Respond(t, tc.status, tc.body, nil)
			err := tc.call(NewClient(srv.URL, "tok"))
			he := wp01AsHTTPError(t, err)
			if he.Code != tc.code || he.ErrorCode() != string(tc.code) {
				t.Errorf("Code = %q, want %q", he.Code, tc.code)
			}
			if he.Status != tc.status || he.HTTPStatus() != tc.status {
				t.Errorf("Status = %d, want %d", he.Status, tc.status)
			}
			if he.Body != tc.body {
				t.Errorf("Body = %q, want %q", he.Body, tc.body)
			}
			for _, s := range tc.is {
				if !errors.Is(err, s) {
					t.Errorf("errors.Is(%v) = false", s)
				}
			}
			if !reflect.DeepEqual(he.Details, tc.details) {
				t.Errorf("Details = %#v, want %#v", he.Details, tc.details)
			}
		})
	}

	t.Run("closed port is a NetworkError", func(t *testing.T) {
		err := listPages(NewClient(wp01ClosedURL(t), "tok"))
		var ne *NetworkError
		if !errors.As(err, &ne) {
			t.Fatalf("error %T (%v) is not a *NetworkError", err, err)
		}
		if ne.ErrorCode() != "network" || ne.Op != "list pages" {
			t.Errorf("ErrorCode() = %q, Op = %q", ne.ErrorCode(), ne.Op)
		}
		var ue *url.Error
		if !errors.As(err, &ue) {
			t.Error("NetworkError lost the *url.Error")
		}
		var he *HTTPError
		if errors.As(err, &he) {
			t.Error("a transport failure must not be an *HTTPError")
		}
		if !strings.HasPrefix(err.Error(), "sync: list pages: ") {
			t.Errorf("Error() = %q", err.Error())
		}
	})
}

// TestLegacyErrorTextsByteStable pins every method's legacy Error() text for
// a non-2xx status, a garbled 2xx body and a transport failure.
func TestLegacyErrorTextsByteStable(t *testing.T) {
	closed := wp01ClosedURL(t)
	for _, m := range wp01AllMethods() {
		t.Run(m.name, func(t *testing.T) {
			srv := wp01Respond(t, 500, "boom", nil)
			err := m.call(NewClient(srv.URL, "tok"))
			he := wp01AsHTTPError(t, err)
			if want := fmt.Sprintf(m.fail, 500, "boom"); err.Error() != want {
				t.Errorf("500: Error() = %q, want %q", err.Error(), want)
			}
			if he.Code != contract.CodeServerError || he.Status != 500 {
				t.Errorf("500: Code = %q Status = %d", he.Code, he.Status)
			}

			srv418 := wp01Respond(t, 418, "teapot", nil)
			err = m.call(NewClient(srv418.URL, "tok"))
			he = wp01AsHTTPError(t, err)
			if want := fmt.Sprintf(m.fail, 418, "teapot"); err.Error() != want {
				t.Errorf("418: Error() = %q, want %q", err.Error(), want)
			}
			if he.Code != contract.CodeInternal {
				t.Errorf("418: Code = %q, want internal", he.Code)
			}

			if m.parse != "" {
				const garbled = "garbage{"
				var v any
				jerr := json.Unmarshal([]byte(garbled), &v)
				srvG := wp01Respond(t, 200, garbled, nil)
				err = m.call(NewClient(srvG.URL, "tok"))
				he = wp01AsHTTPError(t, err)
				if want := m.parse + jerr.Error(); err.Error() != want {
					t.Errorf("garbled: Error() = %q, want %q", err.Error(), want)
				}
				if he.Code != contract.CodeServerError || he.Status != 200 {
					t.Errorf("garbled: Code = %q Status = %d", he.Code, he.Status)
				}
				var se *json.SyntaxError
				if !errors.As(err, &se) {
					t.Error("garbled: the *json.SyntaxError left the chain")
				}
			}

			err = m.call(NewClient(closed, "tok"))
			var ne *NetworkError
			if !errors.As(err, &ne) {
				t.Fatalf("closed port: error %T (%v) is not a *NetworkError", err, err)
			}
			if ne.Op != m.op || !strings.HasPrefix(err.Error(), "sync: "+m.op+": ") {
				t.Errorf("closed port: Op = %q, Error() = %q, want op %q", ne.Op, err.Error(), m.op)
			}
		})
	}
}

// TestLegacySentinelsStillMatch: callers in diff.go, push_preview.go and
// members.go keep their errors.Is checks and their exact texts.
func TestLegacySentinelsStillMatch(t *testing.T) {
	for _, body := range []string{`{"error":"page not found"}`, `404 page not found`, ``} {
		t.Run("ReadPage 404 "+body, func(t *testing.T) {
			srv := wp01Respond(t, 404, body, nil)
			data, sha, err := NewClient(srv.URL, "tok").ReadPage("team-repo", "wiki/concepts/a.md")
			if data != nil || sha != "" {
				t.Errorf("returned (%q, %q) with the error", data, sha)
			}
			if !errors.Is(err, ErrPageNotFound) {
				t.Errorf("errors.Is(ErrPageNotFound) = false for %v", err)
			}
			if err.Error() != ErrPageNotFound.Error() {
				t.Errorf("Error() = %q, want %q", err.Error(), ErrPageNotFound.Error())
			}
			if he := wp01AsHTTPError(t, err); he.Status != 404 {
				t.Errorf("Status = %d", he.Status)
			}
		})
	}

	cases := []struct {
		status   int
		body     string
		sentinel error
		code     contract.ErrorCode
	}{
		{403, `{"error":"owner role required"}`, ErrNotOwner, contract.CodeForbidden},
		{404, `{"error":"not a member of this repo"}`, ErrMemberNotFound, contract.CodePageNotFound},
		{409, `{"error":"cannot remove the last owner"}`, ErrLastOwner, contract.CodeInternal},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("RemoveMember %d", tc.status), func(t *testing.T) {
			srv := wp01Respond(t, tc.status, tc.body, nil)
			err := NewClient(srv.URL, "tok").RemoveMember("team-repo", "u2")
			if !errors.Is(err, tc.sentinel) {
				t.Errorf("errors.Is(%v) = false for %v", tc.sentinel, err)
			}
			if err.Error() != tc.sentinel.Error() {
				t.Errorf("Error() = %q, want %q", err.Error(), tc.sentinel.Error())
			}
			he := wp01AsHTTPError(t, err)
			if he.Code != tc.code || he.Status != tc.status {
				t.Errorf("Code = %q Status = %d, want %q %d", he.Code, he.Status, tc.code, tc.status)
			}
		})
	}
}

// wp01HeaderServer records the X-Contexo-Client and Authorization header of
// every request and answers 200 with a parseable body (echoing ?at).
type wp01HeaderServer struct {
	*httptest.Server
	mu      stdsync.Mutex
	clients []string
	auths   []string
}

func wp01NewHeaderServer(t *testing.T) *wp01HeaderServer {
	t.Helper()
	hs := &wp01HeaderServer{}
	hs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		hs.mu.Lock()
		hs.clients = append(hs.clients, r.Header.Get("X-Contexo-Client"))
		hs.auths = append(hs.auths, r.Header.Get("Authorization"))
		hs.mu.Unlock()
		if at := r.URL.Query().Get("at"); at != "" {
			w.Header().Set("X-Contexo-At", at)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(hs.Close)
	return hs
}

func (hs *wp01HeaderServer) last() (client, auth string, n int) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	n = len(hs.clients)
	if n == 0 {
		return "", "", 0
	}
	return hs.clients[n-1], hs.auths[n-1], n
}

func TestClientNameHeader(t *testing.T) {
	cases := []struct {
		name    string
		env     *string // nil = unset
		setName *string // nil = never called
		want    string
	}{
		{"default", nil, nil, DefaultClientName},
		{"SetClientName beats default", nil, wp01Ptr("claude-code"), "claude-code"},
		{"env beats SetClientName", wp01Ptr("contexo-vscode/0.7.0"), wp01Ptr("claude-code"), "contexo-vscode/0.7.0"},
		{"env beats default", wp01Ptr("contexo-vscode/0.7.0"), nil, "contexo-vscode/0.7.0"},
		{"empty env ignored", wp01Ptr(""), wp01Ptr("claude-code"), "claude-code"},
		{"env with CRLF ignored", wp01Ptr("evil\r\nX-Injected: 1"), wp01Ptr("claude-code"), "claude-code"},
		{"env with control char ignored", wp01Ptr("bad\x07name"), nil, DefaultClientName},
		{"empty SetClientName falls back", nil, wp01Ptr(""), DefaultClientName},
		{"SetClientName with LF falls back", nil, wp01Ptr("agent\nX: y"), DefaultClientName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == nil {
				wp01UnsetClientEnv(t)
			} else {
				t.Setenv("CONTEXO_CLIENT", *tc.env)
			}
			hs := wp01NewHeaderServer(t)
			c := NewClient(hs.URL, "tok")
			if tc.setName != nil {
				c.SetClientName(*tc.setName)
			}
			for _, m := range wp01AllMethods() {
				_, _, before := hs.last()
				if err := m.call(c); err != nil {
					t.Errorf("%s: %v", m.name, err)
				}
				client, auth, n := hs.last()
				if n != before+1 {
					t.Errorf("%s: made %d requests, want 1", m.name, n-before)
					continue
				}
				if client != tc.want {
					t.Errorf("%s: X-Contexo-Client = %q, want %q", m.name, client, tc.want)
				}
				if auth != "Bearer tok" {
					t.Errorf("%s: Authorization = %q, want Bearer tok", m.name, auth)
				}
			}
		})
	}

	t.Run("env is read per request", func(t *testing.T) {
		wp01UnsetClientEnv(t)
		hs := wp01NewHeaderServer(t)
		c := NewClient(hs.URL, "tok")
		if _, err := c.ListPages("team-repo"); err != nil {
			t.Fatal(err)
		}
		if got, _, _ := hs.last(); got != DefaultClientName {
			t.Errorf("before env: %q", got)
		}
		t.Setenv("CONTEXO_CLIENT", "contexo-vscode/0.7.1")
		if _, err := c.ListPages("team-repo"); err != nil {
			t.Fatal(err)
		}
		if got, _, _ := hs.last(); got != "contexo-vscode/0.7.1" {
			t.Errorf("after env: %q", got)
		}
	})
}

func wp01Ptr(s string) *string { return &s }

func TestPushFileExpectAbsentOmitEmpty(t *testing.T) {
	cases := []struct {
		f    PushFile
		want string
	}{
		{PushFile{Path: "wiki/concepts/a.md", Content: "x"}, `{"path":"wiki/concepts/a.md","content":"x"}`},
		{PushFile{Path: "wiki/concepts/a.md", Content: "x", ParentSHA: wp01SHA1},
			`{"path":"wiki/concepts/a.md","content":"x","parent_sha":"` + wp01SHA1 + `"}`},
		{PushFile{Path: "wiki/concepts/a.md", Content: "x", ExpectAbsent: true},
			`{"path":"wiki/concepts/a.md","content":"x","expect_absent":true}`},
	}
	for _, tc := range cases {
		got, err := json.Marshal(tc.f)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("Marshal(%+v) = %s, want %s", tc.f, got, tc.want)
		}
	}

	// And on the wire through PushPages.
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		_, _ = w.Write([]byte(`{"new_head":"` + wp01SHA2 + `"}`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, "tok").PushPages("team-repo", &PushRequest{
		AuthorName: "Alice", AuthorEmail: "alice@acme.dev", Message: "m",
		Files: []PushFile{
			{Path: "wiki/concepts/new.md", Content: "n", ExpectAbsent: true},
			{Path: "wiki/concepts/old.md", Content: "o", ParentSHA: wp01SHA1},
		},
	})
	if err != nil {
		t.Fatalf("PushPages: %v", err)
	}
	want := `{"author_name":"Alice","author_email":"alice@acme.dev","message":"m","files":[` +
		`{"path":"wiki/concepts/new.md","content":"n","expect_absent":true},` +
		`{"path":"wiki/concepts/old.md","content":"o","parent_sha":"` + wp01SHA1 + `"}]}`
	if string(body) != want {
		t.Errorf("wire body = %s\nwant        %s", body, want)
	}
}

// TestPushPagesConflictIsData: a 409 still returns the PushResponse with no
// error (conflicts are data, never an error code).
func TestPushPagesConflictIsData(t *testing.T) {
	srv := wp01Respond(t, 409, `{"new_head":"`+wp01SHA2+`","conflicts":[{"path":"wiki/concepts/a.md","current_sha":"`+wp01SHA2+`","current_content":"eA==","expected_parent_sha":"`+wp01SHA1+`"}]}`, nil)
	pr, err := NewClient(srv.URL, "tok").PushPages("team-repo", &PushRequest{Files: []PushFile{{Path: "wiki/concepts/a.md", Content: "x", ParentSHA: wp01SHA1}}})
	if err != nil {
		t.Fatalf("PushPages 409: %v", err)
	}
	if len(pr.Conflicts) != 1 || pr.Conflicts[0].CurrentSHA != wp01SHA2 || string(pr.Conflicts[0].CurrentContent) != "x" {
		t.Errorf("conflicts = %+v", pr.Conflicts)
	}
}
