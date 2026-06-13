// Package app boots a Contexo server. It is the public seam the OSS server and
// private (contexo-backend) builds share: both call Run, and the private build
// passes extra Registrars that mount cloud-only routes on the authenticated
// /v1 group. Keeping this package public (not under internal/) lets a separate
// module build a Contexo server without copying the bootstrap or importing
// Contexo internals.
package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server"
	"github.com/sugihAF/contexo/internal/server/gitstore"
	"github.com/sugihAF/contexo/internal/server/handler"
	"github.com/sugihAF/contexo/internal/userstore"
	"github.com/sugihAF/contexo/quota"
)

// Registrar mounts extra routes on the authenticated /v1 group. The handler
// reads the caller's identity from the gin context set by the auth middleware
// (e.g. c.GetString("user_id")). Only public types cross this boundary, so a
// separate module can implement one without importing Contexo internals.
type Registrar func(v1 *gin.RouterGroup)

// RootRegistrar mounts routes on the engine root, outside the /v1 auth
// middleware — for endpoints that authenticate themselves (e.g. a Stripe
// webhook verified by signature rather than a bearer token).
type RootRegistrar func(root *gin.RouterGroup)

// Option configures Run. It is the open-core seam: a private build passes
// WithRegistrar (authenticated cloud routes), WithRootRegistrar (unauthenticated
// cloud routes), and/or WithQuota (hosted usage limits); the OSS server calls
// Run with no options and behaves exactly as before.
type Option func(*options)

type options struct {
	registrars     []Registrar
	rootRegistrars []RootRegistrar
	quota          quota.Policy
}

func collectOptions(opts ...Option) options {
	var cfg options
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithRegistrar mounts cloud-only routes on the authenticated /v1 group. Nil
// registrars are ignored; it may be passed more than once.
func WithRegistrar(r Registrar) Option {
	return func(o *options) {
		if r != nil {
			o.registrars = append(o.registrars, r)
		}
	}
}

// WithRootRegistrar mounts an unauthenticated route group on the engine root
// (e.g. payment webhooks). Nil registrars are ignored; it may be passed more
// than once.
func WithRootRegistrar(r RootRegistrar) Option {
	return func(o *options) {
		if r != nil {
			o.rootRegistrars = append(o.rootRegistrars, r)
		}
	}
}

// WithQuota installs a hosted usage-limit policy (repo/member caps). Without it
// the server is uncapped — the correct default for OSS/self-host builds.
func WithQuota(p quota.Policy) Option {
	return func(o *options) { o.quota = p }
}

// Run boots the Contexo server from environment configuration and blocks
// serving HTTP. Options let a private build add cloud-only routes and usage
// limits; the OSS server calls Run with none and behaves exactly as before.
func Run(opts ...Option) error {
	cfg := collectOptions(opts...)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dataRoot := os.Getenv("CONTEXO_DATA_ROOT")
	if dataRoot == "" {
		cwd, _ := os.Getwd()
		dataRoot = filepath.Join(cwd, "contexo-data")
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return fmt.Errorf("contexo: mkdir data root: %w", err)
	}

	store, err := gitstore.Open(dataRoot)
	if err != nil {
		return fmt.Errorf("contexo: open gitstore at %s: %w", dataRoot, err)
	}

	users, err := userstore.Open(filepath.Join(dataRoot, "contexo.db"))
	if err != nil {
		return fmt.Errorf("contexo: open userstore: %w", err)
	}
	defer users.Close()

	sessionSecret := os.Getenv("CONTEXO_SESSION_SECRET")
	if sessionSecret == "" {
		// Auto-generate a per-boot secret. Existing sessions invalidate on
		// restart — operators should set CONTEXO_SESSION_SECRET in production
		// for persistent sessions.
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("contexo: gen session secret: %w", err)
		}
		sessionSecret = hex.EncodeToString(b)
		log.Println("contexo: WARNING using ephemeral session secret (set CONTEXO_SESSION_SECRET to persist sessions across restarts)")
	}
	signer, err := auth.NewSessionSigner(sessionSecret)
	if err != nil {
		return fmt.Errorf("contexo: session signer: %w", err)
	}

	var googleVerifier auth.Verifier
	if clientID := os.Getenv("GOOGLE_OAUTH_CLIENT_ID"); clientID != "" {
		googleVerifier = auth.NewGoogleVerifier(clientID)
		log.Printf("contexo: google sign-in enabled (client_id=%s...)", clientID[:min(len(clientID), 12)])
	} else {
		log.Println("contexo: GOOGLE_OAUTH_CLIENT_ID unset — POST /v1/auth/google will return 503")
	}

	legacyKey := os.Getenv("CONTEXO_API_KEY")
	if legacyKey == "" {
		legacyKey = "dev-key"
	}

	resolver := auth.NewResolver(signer, users, legacyKey)
	h := handler.New(store, users, signer, googleVerifier)
	if cfg.quota != nil {
		h.SetQuota(cfg.quota)
	}

	routeExtras := make([]func(*gin.RouterGroup), len(cfg.registrars))
	for i := range cfg.registrars {
		routeExtras[i] = cfg.registrars[i]
	}
	rootExtras := make([]func(*gin.RouterGroup), len(cfg.rootRegistrars))
	for i := range cfg.rootRegistrars {
		rootExtras[i] = cfg.rootRegistrars[i]
	}
	router := server.NewRouter(h, resolver, routeExtras, rootExtras)

	// Use an explicit http.Server with timeouts instead of router.Run (gin's
	// default has none): without ReadHeaderTimeout/IdleTimeout a single client
	// can hold connections open indefinitely (Slowloris) and exhaust the
	// WAF-less origin. Listen address is overridable so prod can bind loopback
	// (CONTEXO_LISTEN_ADDR=127.0.0.1:8080) and let Caddy be the only ingress.
	addr := os.Getenv("CONTEXO_LISTEN_ADDR")
	if addr == "" {
		addr = fmt.Sprintf(":%s", port)
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("Contexo server starting on %s (data: %s)", addr, dataRoot)
	return srv.ListenAndServe()
}
