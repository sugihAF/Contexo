// Package server wires the Contexo HTTP routes.
package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server/handler"
	"github.com/sugihAF/contexo/internal/version"
)

// APIVersion is the wire protocol version reported by GET /health.
const APIVersion = 1

// Capabilities are the contract's server capability names. They are
// advertised on every response (X-Contexo-Capabilities, comma-joined in this
// order) and listed by GET /health, so a client can tell this server from an
// older one before relying on a feature. Only names this server implements
// belong here; reserved names stay out until they ship.
var Capabilities = []string{"pages.at", "push.expect_absent", "write.path_sha"}

// healthResponse is the GET /health body. A struct, not a map, so the JSON
// keys keep this order.
type healthResponse struct {
	Status       string   `json:"status"`
	API          int      `json:"api"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

// serverHeaders stamps the server version and capability list on every
// response. NewRouter registers it before every other middleware, so the
// headers are already set when MaxBody, RateLimit, auth or gin's own 404
// write their response.
func serverHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Contexo-Server-Version", version.Version)
		c.Header("X-Contexo-Capabilities", strings.Join(Capabilities, ","))
		c.Next()
	}
}

// NewRouter wires the git-backed Contexo endpoints. The version/capability
// header middleware runs first, then the body cap, rate limiting and CORS
// (which answers preflight OPTIONS without auth). /v1/auth/google is
// unauthenticated (it establishes the session). All other /v1/* requires a
// Bearer token that the resolver can map to a user_id (session JWT, PAT, or
// legacy API key).
//
// v1Extras and rootExtras are open-core seam hooks. v1Extras mount cloud-only
// routes on the authenticated /v1 group (they see the resolved user_id).
// rootExtras mount on the engine root, outside the auth middleware, for routes
// that authenticate themselves — e.g. payment webhooks verified by signature.
// The OSS server passes nil for both and behaves identically.
func NewRouter(h *handler.Handler, resolver *auth.Resolver, v1Extras []func(v1 *gin.RouterGroup), rootExtras []func(root *gin.RouterGroup)) *gin.Engine {
	r := gin.Default()
	// Trust only the local reverse proxy (Caddy) for X-Forwarded-For, so the
	// rate-limit key is the real client IP and cannot be spoofed via a header
	// by a direct client. Self-host behind a different proxy: set
	// CONTEXO_TRUSTED_PROXIES (comma-separated) to that proxy's address(es).
	_ = r.SetTrustedProxies(trustedProxies())

	// First, so every response carries the version and capability headers,
	// including the 401/404/413/429 that later middleware or gin write.
	r.Use(serverHeaders())

	// DoS resilience: cap request bodies and rate-limit per client IP. The
	// origin has no CDN/WAF, so these app-side controls are the only defense.
	r.Use(MaxBody(envInt64("CONTEXO_MAX_BODY_BYTES", defaultMaxBodyBytes)))
	if rateLimitingEnabled() {
		general := newRateLimiter(
			envInt("CONTEXO_RATELIMIT_PER_MIN", defaultRatePerMin),
			envInt("CONTEXO_RATELIMIT_BURST", defaultRateBurst),
		)
		r.Use(RateLimit(general))
	}

	r.Use(CORS())

	// Unauthenticated. The Docker HEALTHCHECK only needs the 200; clients
	// read the api version and capabilities.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, healthResponse{
			Status:       "ok",
			API:          APIVersion,
			Version:      version.Version,
			Capabilities: Capabilities,
		})
	})

	// Unauthenticated: establishes identity. Rate-limited harder than the rest
	// because each call runs an RS256 id_token verification plus DB writes — a
	// prime target for credential-stuffing / cost-burn floods.
	if rateLimitingEnabled() {
		authLimiter := newRateLimiter(
			envInt("CONTEXO_AUTH_RATELIMIT_PER_MIN", defaultAuthRatePerMin),
			envInt("CONTEXO_AUTH_RATELIMIT_BURST", defaultAuthRateBurst),
		)
		r.POST("/v1/auth/google", RateLimit(authLimiter), h.GoogleAuth)
	} else {
		r.POST("/v1/auth/google", h.GoogleAuth)
	}

	// Authenticated routes.
	v1 := r.Group("/v1")
	v1.Use(auth.GinMiddleware(resolver.Validator()))

	v1.GET("/me", h.Me)

	v1.POST("/pats", h.CreatePAT)
	v1.GET("/pats", h.ListPATs)
	v1.DELETE("/pats/:id", h.DeletePAT)

	v1.GET("/repos", h.ListRepos)
	v1.POST("/repos", h.CreateRepo)
	v1.POST("/repos/join", h.JoinRepo)

	// Legacy: POST /v1/repos/:id (kept for back-compat with the old CLI).
	v1.POST("/repos/:id", h.CreateRepoLegacy)

	v1.GET("/repos/:id/pages", h.ListPages)
	v1.GET("/repos/:id/pages/*path", h.ReadPage)
	v1.POST("/repos/:id/sync/push", h.Push)
	v1.GET("/repos/:id/sync/pull", h.Pull)
	v1.POST("/repos/:id/sync/distill", h.Distill)
	v1.GET("/repos/:id/timeline", h.Timeline)
	v1.GET("/repos/:id/activity", h.Activity)
	v1.GET("/repos/:id/history/*path", h.History)
	v1.GET("/repos/:id/diff/*path", h.Diff)
	v1.GET("/repos/:id/evolution/*path", h.Evolution)

	v1.POST("/repos/:id/invite-keys", h.MintInviteKey)
	v1.GET("/repos/:id/invite-keys", h.ListInviteKeys)
	v1.DELETE("/repos/:id/invite-keys/:keyId", h.DeleteInviteKey)

	v1.GET("/repos/:id/members", h.ListMembers)
	v1.DELETE("/repos/:id/members/:userId", h.RemoveMember)

	// Open-core seam: cloud-only routes from a private build mount here, on the
	// same authenticated /v1 group (so they see the resolved user_id).
	for _, add := range v1Extras {
		add(v1)
	}

	// Root seam: unauthenticated cloud routes (e.g. a Stripe webhook that
	// authenticates by signature, not a bearer token) mount on the engine root,
	// outside the /v1 auth middleware.
	for _, add := range rootExtras {
		add(&r.RouterGroup)
	}

	return r
}
