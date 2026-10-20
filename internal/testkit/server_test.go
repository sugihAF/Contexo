package testkit

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ctxsync "github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/version"
)

const wp02TestCapabilities = "pages.at,push.expect_absent,write.path_sha"

// wp02Do sends one request to s with "Authorization: Bearer "+key (no
// header when key is "") and returns the response and its fully read body.
func wp02Do(t *testing.T, s *Server, method, path, key string, body []byte) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, s.URL+path, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	return resp, b
}

// wp02PushOne pushes one page through the real sync client and returns the
// sha the server reported for it.
func wp02PushOne(t *testing.T, s *Server, repo, path, content, parent string) string {
	t.Helper()
	resp, err := ctxsync.NewClient(s.URL, s.Key).PushPages(repo, &ctxsync.PushRequest{
		AuthorName:  "Testkit",
		AuthorEmail: "testkit@example.com",
		Message:     "push " + path,
		Files:       []ctxsync.PushFile{{Path: path, Content: content, ParentSHA: parent}},
	})
	if err != nil {
		t.Fatalf("push %s: %v", path, err)
	}
	if len(resp.Conflicts) != 0 || len(resp.Pushed) != 1 || resp.Pushed[0].Path != path || resp.Pushed[0].SHA == "" {
		t.Fatalf("push %s: unexpected response %+v", path, resp)
	}
	return resp.Pushed[0].SHA
}

func TestTestkitServerRoundTrip(t *testing.T) {
	s := StartServer(t)
	if s.Key != "testkit-key" {
		t.Errorf("Key = %q, want testkit-key", s.Key)
	}
	if !strings.HasPrefix(s.URL, "http://") || strings.HasSuffix(s.URL, "/") {
		t.Errorf("URL = %q, want an http base URL without a trailing slash", s.URL)
	}
	if s.Store == nil {
		t.Fatal("Store is nil")
	}

	const repo, page, content = "wp02-roundtrip", "wiki/concepts/a.md", "# A\n\nfirst\n"
	sha := wp02PushOne(t, s, repo, page, content, "")

	if got, err := s.Store.HeadSHAForPath(repo, page); err != nil || got != sha {
		t.Errorf("Store.HeadSHAForPath = %q, %v; want %q (Store must be the server's store)", got, err, sha)
	}

	body, pageSHA, err := ctxsync.NewClient(s.URL, s.Key).ReadPage(repo, page)
	if err != nil {
		t.Fatalf("ReadPage: %v", err)
	}
	if string(body) != content || pageSHA != sha {
		t.Errorf("ReadPage = %q, %q; want %q, %q", body, pageSHA, content, sha)
	}

	resp, raw := wp02Do(t, s, http.MethodGet, "/v1/repos/"+repo+"/pages", s.Key, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list pages: %d %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("X-Contexo-Capabilities"); got != wp02TestCapabilities {
		t.Errorf("X-Contexo-Capabilities = %q, want %q", got, wp02TestCapabilities)
	}
	if got := resp.Header.Get("X-Contexo-Server-Version"); got != version.Version {
		t.Errorf("X-Contexo-Server-Version = %q, want %q", got, version.Version)
	}
	var listing struct {
		Pages []struct {
			Path string `json:"path"`
			SHA  string `json:"sha"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatalf("decode listing %s: %v", raw, err)
	}
	found := false
	for _, p := range listing.Pages {
		if p.Path == page {
			found = true
			if p.SHA != sha {
				t.Errorf("listing sha for %s = %q, want %q", page, p.SHA, sha)
			}
		}
	}
	if !found {
		t.Errorf("listing %s does not contain %s", raw, page)
	}

	// The new server honours expect_absent, so the old-server test's 200
	// for the same request proves the wrapper strips the field.
	push := []byte(`{"author_name":"T","author_email":"t@example.com","files":[` +
		`{"path":"` + page + `","content":"other","parent_sha":"` + sha + `","expect_absent":true}]}`)
	resp, raw = wp02Do(t, s, http.MethodPost, "/v1/repos/"+repo+"/sync/push", s.Key, push)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(raw), `"expected_parent_sha":""`) {
		t.Errorf("expect_absent onto an existing page: %d %s; want 409 with expected_parent_sha \"\"", resp.StatusCode, raw)
	}
}

// TestTestkitServerReadsAt proves ?at works end to end on StartServer, as
// the contrast to the old server: the historic content, the requested sha
// echoed verbatim and the page's own commit at or before it.
func TestTestkitServerReadsAt(t *testing.T) {
	s := StartServer(t)
	const repo, page = "wp02-at", "wiki/concepts/c.md"
	v1 := wp02PushOne(t, s, repo, page, "# C\n\nv1\n", "")
	v2 := wp02PushOne(t, s, repo, page, "# C\n\nv2\n", v1)
	if v1 == v2 {
		t.Fatalf("v1 and v2 share sha %s", v1)
	}

	for _, at := range []string{v1, v1[:7]} {
		resp, raw := wp02Do(t, s, http.MethodGet, "/v1/repos/"+repo+"/pages/"+page+"?at="+at, s.Key, nil)
		if resp.StatusCode != http.StatusOK || string(raw) != "# C\n\nv1\n" {
			t.Errorf("?at=%s = %d %q, want 200 with the v1 content", at, resp.StatusCode, raw)
		}
		if got := resp.Header.Values("X-Contexo-At"); len(got) != 1 || got[0] != at {
			t.Errorf("?at=%s: X-Contexo-At = %q, want [%q]", at, got, at)
		}
		if got := resp.Header.Get("X-Page-SHA"); got != v1 {
			t.Errorf("?at=%s: X-Page-SHA = %q, want %q", at, got, v1)
		}
		if got := resp.Header.Get("X-Contexo-Capabilities"); got != wp02TestCapabilities {
			t.Errorf("?at=%s: X-Contexo-Capabilities = %q, want %q", at, got, wp02TestCapabilities)
		}
	}
}

func TestTestkitOldServerHidesAt(t *testing.T) {
	s := StartOldServer(t)
	if s.Key != "testkit-key" || s.Store == nil {
		t.Fatalf("StartOldServer = %+v, want the default key and a store", s)
	}

	const repo, page = "wp02-old", "wiki/concepts/b.md"
	v1 := wp02PushOne(t, s, repo, page, "# B\n\nv1\n", "")
	v2 := wp02PushOne(t, s, repo, page, "# B\n\nv2\n", v1)
	if v1 == v2 {
		t.Fatalf("v1 and v2 share sha %s", v1)
	}

	// An old server ignores ?at: HEAD content, 200, and no echo or
	// capability headers for the client to key on.
	resp, raw := wp02Do(t, s, http.MethodGet, "/v1/repos/"+repo+"/pages/"+page+"?at="+v1, s.Key, nil)
	if resp.StatusCode != http.StatusOK || string(raw) != "# B\n\nv2\n" {
		t.Errorf("?at=v1 = %d %q, want 200 with the HEAD (v2) content", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("X-Page-SHA"); got != v2 {
		t.Errorf("X-Page-SHA = %q, want HEAD path sha %q", got, v2)
	}
	for _, h := range []string{"X-Contexo-At", "X-Contexo-Capabilities", "X-Contexo-Server-Version"} {
		if v, ok := resp.Header[h]; ok {
			t.Errorf("old server sent %s: %q", h, v)
		}
	}

	resp, raw = wp02Do(t, s, http.MethodGet, "/health", "", nil)
	if resp.StatusCode != http.StatusOK || string(raw) != `{"status":"ok"}` {
		t.Errorf("/health = %d %q, want 200 {\"status\":\"ok\"}", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("/health Content-Type = %q, want application/json", ct)
	}
	for _, h := range []string{"X-Contexo-Capabilities", "X-Contexo-Server-Version"} {
		if v, ok := resp.Header[h]; ok {
			t.Errorf("old /health sent %s: %q", h, v)
		}
	}

	// An old server ignores expect_absent, so pushing onto the existing
	// page with the right parent is an ordinary accepted write.
	push := []byte(`{"author_name":"T","author_email":"t@example.com","files":[` +
		`{"path":"` + page + `","content":"# B\n\nv3\n","parent_sha":"` + v2 + `","expect_absent":true}]}`)
	resp, raw = wp02Do(t, s, http.MethodPost, "/v1/repos/"+repo+"/sync/push", s.Key, push)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expect_absent push on the old server = %d %s, want 200", resp.StatusCode, raw)
	}
	var pr ctxsync.PushResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		t.Fatalf("decode push %s: %v", raw, err)
	}
	if len(pr.Conflicts) != 0 || len(pr.Pushed) != 1 || pr.Pushed[0].Path != page {
		t.Errorf("push response = %+v, want one pushed page and no conflicts", pr)
	}
	if v, ok := resp.Header["X-Contexo-Capabilities"]; ok {
		t.Errorf("old push response sent X-Contexo-Capabilities: %q", v)
	}
	if got, err := s.Store.HeadSHAForPath(repo, page); err != nil || got == v2 {
		t.Errorf("page sha after the push = %q, %v; want a new commit", got, err)
	}
}

// TestTestkitOldServerRewrites covers the old-server request rewrites on
// inputs the end-to-end tests do not reach.
func TestTestkitOldServerRewrites(t *testing.T) {
	for raw, want := range map[string]string{
		"at=abc":                  "",
		"since=s1&at=abc&limit=5": "since=s1&limit=5",
		"%61t=abc&since=s1":       "since=s1",
		"atx=1&since=s1":          "atx=1&since=s1",
		"since=a%2Bb":             "since=a%2Bb",
	} {
		r := httptest.NewRequest(http.MethodGet, "/v1/repos/r/pages/p.md?"+raw, nil)
		if got := wp02DropAt(r).URL.RawQuery; got != want {
			t.Errorf("wp02DropAt(%q) = %q, want %q", raw, got, want)
		}
	}

	for _, body := range []string{
		`not json`,
		`[]`,
		`{"files":"x"}`,
		`{"files":[1]}`,
		`{"files":[{"path":"a.md"}]}`,
	} {
		if out, ok := wp02WithoutExpectAbsent([]byte(body)); ok {
			t.Errorf("wp02WithoutExpectAbsent(%s) = %s, want pass-through", body, out)
		}
	}

	out, ok := wp02WithoutExpectAbsent([]byte(`{"message":"m","files":[{"path":"a.md","content":"x","expect_absent":true},{"path":"b.md","expect_absent":false}]}`))
	if !ok {
		t.Fatal("wp02WithoutExpectAbsent did not strip")
	}
	var got struct {
		Message string                       `json:"message"`
		Files   []map[string]json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	if got.Message != "m" || len(got.Files) != 2 || string(got.Files[0]["content"]) != `"x"` {
		t.Errorf("stripped body lost data: %s", out)
	}
	for i, f := range got.Files {
		if _, ok := f["expect_absent"]; ok {
			t.Errorf("files[%d] still has expect_absent: %s", i, out)
		}
	}

	// A malformed push still reaches the router, which rejects it as before.
	s := StartOldServer(t)
	if resp, raw := wp02Do(t, s, http.MethodPost, "/v1/repos/r/sync/push", s.Key, []byte(`{"files":`)); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed push = %d %s, want 400", resp.StatusCode, raw)
	}
}

func TestTestkitWithKey(t *testing.T) {
	s := StartServer(t, WithKey("k2"))
	if s.Key != "k2" {
		t.Fatalf("Key = %q, want k2", s.Key)
	}
	if resp, raw := wp02Do(t, s, http.MethodGet, "/v1/repos", "k2", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("GET /v1/repos with k2 = %d %s, want 200", resp.StatusCode, raw)
	}
	if resp, raw := wp02Do(t, s, http.MethodGet, "/v1/repos", "testkit-key", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /v1/repos with the default key = %d %s, want 401", resp.StatusCode, raw)
	}
}
