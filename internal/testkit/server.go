package testkit

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server"
	"github.com/sugihAF/contexo/internal/server/gitstore"
	"github.com/sugihAF/contexo/internal/server/handler"
	"github.com/sugihAF/contexo/internal/userstore"
)

// Server is a running in-process Contexo server. It is closed by t.Cleanup.
type Server struct {
	// URL is the server's base URL, without a trailing slash.
	URL string
	// Key is the legacy API key; send "Authorization: Bearer " + Key.
	Key string
	// Store is the server's own gitstore, for seeding and inspecting repos.
	Store *gitstore.Store
}

// ServerOption configures StartServer.
type ServerOption func(*wp02ServerConfig)

type wp02ServerConfig struct {
	key string
}

const (
	wp02DefaultKey = "testkit-key"
	// wp02SessionSecret is fixed so runs are reproducible; nothing in the
	// harness mints session tokens, so it only has to satisfy the signer.
	wp02SessionSecret = "testkit-wp02-fixed-session-secret-0123456789"
)

// WithKey sets the legacy API key the server accepts (and Server.Key) in
// place of "testkit-key". The key must be non-empty and must not look like
// a personal access token, or no request could authenticate.
func WithKey(k string) ServerOption {
	return func(c *wp02ServerConfig) { c.key = k }
}

// StartServer runs the real server.NewRouter, wired like app.Run without a
// user store or Google sign-in, over a fresh temporary gitstore. It calls
// t.Setenv (rate limiting off), so the calling test must not be parallel.
func StartServer(t testing.TB, opts ...ServerOption) *Server {
	t.Helper()
	h, s := wp02NewRouter(t, opts)
	return wp02Serve(t, h, s)
}

// StartOldServer is StartServer behind a wrapper that emulates a server from
// before contract v1, for testing a client's fallbacks:
//
//   - GET /health answers exactly {"status":"ok"};
//   - the "at" query parameter is dropped, so ?at=<sha> serves HEAD content
//     with 200, as an old server that ignores the parameter does;
//   - "expect_absent" is removed from push bodies, as an old server ignores
//     the field;
//   - no X-Contexo-At, X-Contexo-Capabilities or X-Contexo-Server-Version
//     header is sent.
//
// It does not reproduce the pre-fix no-op write sha (a re-pushed unchanged
// page reported with the repo HEAD instead of its own commit, which the
// write.path_sha capability announces as fixed): the gitstore underneath is
// the fixed one. Tests that need that behavior use the sync engines' fake
// remote in old-server mode.
func StartOldServer(t testing.TB) *Server {
	t.Helper()
	h, s := wp02NewRouter(t, nil)
	return wp02Serve(t, wp02OldServer{next: h}, s)
}

// wp02NewRouter builds the real router and returns it with a Server whose URL
// is not yet set.
func wp02NewRouter(t testing.TB, opts []ServerOption) (http.Handler, *Server) {
	t.Helper()
	cfg := wp02ServerConfig{key: wp02DefaultKey}
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	if cfg.key == "" {
		t.Fatal("testkit: WithKey(\"\"): an empty legacy key disables legacy auth")
	}
	if strings.HasPrefix(cfg.key, userstore.PATPrefix) {
		t.Fatalf("testkit: WithKey(%q): keys starting with %q are resolved as personal access tokens, never as the legacy key", cfg.key, userstore.PATPrefix)
	}

	// gin.SetMode writes a package-level global; write it only when it
	// would change, so a second harness never writes while handlers read it.
	if gin.Mode() != gin.TestMode {
		gin.SetMode(gin.TestMode)
	}
	// NewRouter reads this at construction; tests make many requests from one
	// loopback address and must not be throttled.
	t.Setenv("CONTEXO_RATELIMIT_DISABLE", "1")

	store, err := gitstore.Open(filepath.Join(t.TempDir(), "repos"))
	if err != nil {
		t.Fatalf("testkit: open gitstore: %v", err)
	}
	signer, err := auth.NewSessionSigner(wp02SessionSecret)
	if err != nil {
		t.Fatalf("testkit: session signer: %v", err)
	}
	resolver := auth.NewResolver(signer, nil, cfg.key)
	h := handler.New(store, nil, signer, nil)
	return server.NewRouter(h, resolver, nil, nil), &Server{Key: cfg.key, Store: store}
}

// wp02Serve listens on a loopback port and closes the listener at cleanup
// (before the temp dirs created earlier are removed).
func wp02Serve(t testing.TB, h http.Handler, s *Server) *Server {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	s.URL = strings.TrimSuffix(ts.URL, "/")
	return s
}

// wp02ContexoHeaders are the response headers an old server never sends.
var wp02ContexoHeaders = []string{"X-Contexo-At", "X-Contexo-Capabilities", "X-Contexo-Server-Version"}

// wp02OldHealth is the pre-v1 /health body, byte for byte as gin wrote it.
const wp02OldHealth = `{"status":"ok"}`

// wp02OldServer emulates a pre-v1 server in front of the real router.
type wp02OldServer struct {
	next http.Handler
}

func (o wp02OldServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/health" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, wp02OldHealth)
		return
	}
	r = wp02DropAt(r)
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/sync/push") {
		r = wp02StripExpectAbsent(r)
	}
	o.next.ServeHTTP(&wp02StripWriter{ResponseWriter: w}, r)
}

// wp02DropAt returns r without any "at" query parameter, keeping every other
// parameter byte for byte.
func wp02DropAt(r *http.Request) *http.Request {
	if r.URL.RawQuery == "" {
		return r
	}
	parts := strings.Split(r.URL.RawQuery, "&")
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		k, _, _ := strings.Cut(p, "=")
		if uk, err := url.QueryUnescape(k); err == nil && uk == "at" {
			continue
		}
		kept = append(kept, p)
	}
	if len(kept) == len(parts) {
		return r
	}
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = strings.Join(kept, "&")
	r2.RequestURI = r2.URL.RequestURI()
	return r2
}

// wp02StripExpectAbsent removes "expect_absent" from every files[] element
// of a push body. A body that is not a JSON object with a files array of
// objects passes through unchanged, so the router still answers it.
func wp02StripExpectAbsent(r *http.Request) *http.Request {
	if r.Body == nil || r.Body == http.NoBody {
		return r
	}
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r2 := r.Clone(r.Context())
	if err != nil {
		// Replay what was read, then the same error, so the handler sees
		// the failure it would have seen (e.g. a body over the size cap).
		r2.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), wp02ErrReader{err}))
		return r2
	}
	if out, ok := wp02WithoutExpectAbsent(body); ok {
		body = out
	}
	r2.Body = io.NopCloser(bytes.NewReader(body))
	r2.ContentLength = int64(len(body))
	r2.TransferEncoding = nil
	r2.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return r2
}

// wp02WithoutExpectAbsent re-encodes body without files[].expect_absent. It
// reports false when there was nothing to strip or body is not the expected
// shape.
func wp02WithoutExpectAbsent(body []byte) ([]byte, bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, false
	}
	rawFiles, ok := top["files"]
	if !ok {
		return nil, false
	}
	var files []map[string]json.RawMessage
	if err := json.Unmarshal(rawFiles, &files); err != nil {
		return nil, false
	}
	stripped := false
	for _, f := range files {
		if _, ok := f["expect_absent"]; ok {
			delete(f, "expect_absent")
			stripped = true
		}
	}
	if !stripped {
		return nil, false
	}
	newFiles, err := json.Marshal(files)
	if err != nil {
		return nil, false
	}
	top["files"] = newFiles
	out, err := json.Marshal(top)
	if err != nil {
		return nil, false
	}
	return out, true
}

type wp02ErrReader struct{ err error }

func (e wp02ErrReader) Read([]byte) (int, error) { return 0, e.err }

// wp02StripWriter deletes the contract v1 headers just before the response
// header is written. The router's middleware sets them before any handler
// runs, so removing them after ServeHTTP returns would be too late.
type wp02StripWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *wp02StripWriter) strip() {
	if w.wroteHeader {
		return
	}
	h := w.ResponseWriter.Header()
	for _, k := range wp02ContexoHeaders {
		h.Del(k)
	}
}

func (w *wp02StripWriter) WriteHeader(code int) {
	w.strip()
	// 1xx responses do not commit the final header; keep stripping until a
	// final status is written.
	if code >= 200 {
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *wp02StripWriter) Write(b []byte) (int, error) {
	w.strip()
	w.wroteHeader = true
	return w.ResponseWriter.Write(b)
}

// Flush keeps streaming responses working through the wrapper.
func (w *wp02StripWriter) Flush() {
	w.strip()
	w.wroteHeader = true
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *wp02StripWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
