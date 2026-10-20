package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server/gitstore"
	"github.com/sugihAF/contexo/internal/server/handler"
	"github.com/sugihAF/contexo/internal/userstore"
	"github.com/sugihAF/contexo/internal/version"
)

// TestNewRouter_RegistersExtras proves the open-core seam: extra route
// registrars passed to NewRouter are mounted on the authenticated /v1 group,
// while the core routes are unaffected. This is the contract the private
// contexo-backend build depends on.
func TestNewRouter_RegistersExtras(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signer, err := auth.NewSessionSigner("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	users, err := userstore.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("userstore: %v", err)
	}
	defer users.Close()
	resolver := auth.NewResolver(signer, users, "legacy-key")
	h := handler.New(nil, users, signer, nil)

	called := false
	r := NewRouter(h, resolver, []func(*gin.RouterGroup){
		func(v1 *gin.RouterGroup) {
			v1.GET("/cloud/ping", func(c *gin.Context) {
				called = true
				c.String(http.StatusOK, "pong")
			})
		},
	}, nil)

	// Core route still works (unauthenticated).
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("health: got %d", w.Code)
	}

	// The extra route is mounted behind the same /v1 auth middleware.
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/cloud/ping", nil)
	req.Header.Set("Authorization", "Bearer legacy-key")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !called {
		t.Fatalf("extra route not reached: code=%d called=%v body=%q", w.Code, called, w.Body.String())
	}

	// Without auth it's blocked — proves it's under /v1, not public.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/cloud/ping", nil))
	if w.Code == http.StatusOK {
		t.Errorf("extra route should require auth, got %d", w.Code)
	}
}

// TestNewRouter_RootExtrasUnauthenticated proves the root seam: routes mounted
// via rootExtras sit outside the /v1 auth middleware, so a signature-verified
// endpoint like the Stripe webhook is reachable with no bearer token.
func TestNewRouter_RootExtrasUnauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signer, _ := auth.NewSessionSigner("0123456789abcdef0123456789abcdef")
	users, err := userstore.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("userstore: %v", err)
	}
	defer users.Close()
	resolver := auth.NewResolver(signer, users, "legacy-key")
	h := handler.New(nil, users, signer, nil)

	hit := false
	r := NewRouter(h, resolver, nil, []func(*gin.RouterGroup){
		func(root *gin.RouterGroup) {
			root.POST("/webhooks/stripe", func(c *gin.Context) {
				hit = true
				c.String(http.StatusOK, "ok")
			})
		},
	})

	// No Authorization header — the route must still be reached.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/webhooks/stripe", nil))
	if w.Code != http.StatusOK || !hit {
		t.Fatalf("root route should be reachable without auth: code=%d hit=%v body=%q", w.Code, hit, w.Body.String())
	}
}

// wp02WantCapabilities is the contract's capability header value: exactly
// these three names, in this order.
const wp02WantCapabilities = "pages.at,push.expect_absent,write.path_sha"

// wp02Router builds the real router over store (nil is fine for routes that
// never touch it) with legacy-key auth and no user store. Callers set any
// CONTEXO_* environment first: NewRouter reads it at construction.
func wp02Router(t *testing.T, store *gitstore.Store) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	signer, err := auth.NewSessionSigner("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	resolver := auth.NewResolver(signer, nil, "legacy-key")
	return NewRouter(handler.New(store, nil, signer, nil), resolver, nil, nil)
}

// wp02TopLevelKeys returns the keys of a JSON object in wire order.
func wp02TopLevelKeys(t *testing.T, body []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("body is not a JSON object: %q", body)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("decode key: %v (body %q)", err, body)
		}
		key, ok := tok.(string)
		if !ok {
			t.Fatalf("object key is %T, want string (body %q)", tok, body)
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("decode value: %v (body %q)", err, body)
		}
	}
	return keys
}

// wp02AssertContexoHeaders checks the version and capability headers are
// each present exactly once with the contract values.
func wp02AssertContexoHeaders(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Values("X-Contexo-Server-Version"); len(got) != 1 || got[0] != version.Version {
		t.Errorf("X-Contexo-Server-Version = %q, want [%q]", got, version.Version)
	}
	if got := w.Header().Values("X-Contexo-Capabilities"); len(got) != 1 || got[0] != wp02WantCapabilities {
		t.Errorf("X-Contexo-Capabilities = %q, want [%q]", got, wp02WantCapabilities)
	}
}

func TestHealthReportsCapabilities(t *testing.T) {
	// A stamped version proves both the body and the header read
	// version.Version rather than a hard-coded "dev".
	old := version.Version
	version.Version = "9.8.7-wp02"
	t.Cleanup(func() { version.Version = old })

	r := wp02Router(t, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("health: got %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body struct {
		Status       string   `json:"status"`
		API          int      `json:"api"`
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.API != 1 {
		t.Errorf("api = %d, want 1", body.API)
	}
	if body.Version != "9.8.7-wp02" {
		t.Errorf("version = %q, want 9.8.7-wp02", body.Version)
	}
	if len(body.Capabilities) != 3 || strings.Join(body.Capabilities, ",") != wp02WantCapabilities {
		t.Errorf("capabilities = %q, want exactly %q", body.Capabilities, wp02WantCapabilities)
	}
	keys := wp02TopLevelKeys(t, w.Body.Bytes())
	if got, want := strings.Join(keys, ","), "status,api,version,capabilities"; got != want {
		t.Errorf("key order = %s, want %s (body %s)", got, want, w.Body.String())
	}
	wp02AssertContexoHeaders(t, w)
}

// TestExportedContractNames pins the Go API other packages consume
// (server.APIVersion, server.Capabilities) to the contract values.
func TestExportedContractNames(t *testing.T) {
	if APIVersion != 1 {
		t.Errorf("APIVersion = %d, want 1", APIVersion)
	}
	if got := strings.Join(Capabilities, ","); got != wp02WantCapabilities || len(Capabilities) != 3 {
		t.Errorf("Capabilities = %q, want exactly %q", Capabilities, wp02WantCapabilities)
	}
}

// TestCapabilityHeadersOnEveryResponse proves the version and capability
// headers ride on every response, including the ones written by middleware
// (401, 413, 429) or by gin itself (404) rather than by a route handler.
func TestCapabilityHeadersOnEveryResponse(t *testing.T) {
	t.Run("health 200", func(t *testing.T) {
		t.Setenv("CONTEXO_RATELIMIT_DISABLE", "1")
		r := wp02Router(t, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200", w.Code)
		}
		wp02AssertContexoHeaders(t, w)
	})

	t.Run("unauthenticated 401", func(t *testing.T) {
		t.Setenv("CONTEXO_RATELIMIT_DISABLE", "1")
		r := wp02Router(t, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/me", nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401", w.Code)
		}
		wp02AssertContexoHeaders(t, w)
	})

	t.Run("unknown route 404", func(t *testing.T) {
		t.Setenv("CONTEXO_RATELIMIT_DISABLE", "1")
		r := wp02Router(t, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/no-such-route", nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("code = %d, want 404", w.Code)
		}
		wp02AssertContexoHeaders(t, w)
	})

	t.Run("oversize push 413", func(t *testing.T) {
		t.Setenv("CONTEXO_RATELIMIT_DISABLE", "1")
		t.Setenv("CONTEXO_MAX_BODY_BYTES", "64")
		store, err := gitstore.Open(t.TempDir())
		if err != nil {
			t.Fatalf("gitstore: %v", err)
		}
		r := wp02Router(t, store)
		body := `{"files":[{"path":"wiki/a.md","content":"` + strings.Repeat("x", 512) + `"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/repos/x/sync/push", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer legacy-key")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("code = %d, want 413 (body %q)", w.Code, w.Body.String())
		}
		wp02AssertContexoHeaders(t, w)
	})

	t.Run("rate limited 429", func(t *testing.T) {
		t.Setenv("CONTEXO_RATELIMIT_DISABLE", "")
		t.Setenv("CONTEXO_RATELIMIT_PER_MIN", "1")
		t.Setenv("CONTEXO_RATELIMIT_BURST", "1")
		r := wp02Router(t, nil)
		var codes []int
		var last *httptest.ResponseRecorder
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
			req.RemoteAddr = "203.0.113.7:4321"
			last = httptest.NewRecorder()
			r.ServeHTTP(last, req)
			codes = append(codes, last.Code)
		}
		if codes[1] != http.StatusTooManyRequests {
			t.Fatalf("codes = %v, want the second request to be 429", codes)
		}
		wp02AssertContexoHeaders(t, last)
	})
}
