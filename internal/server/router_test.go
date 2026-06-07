package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server/handler"
	"github.com/sugihAF/contexo/internal/userstore"
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
