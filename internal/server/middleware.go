package server

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// trustedProxies returns the proxy addresses whose X-Forwarded-For header gin
// should trust when deriving the client IP. Defaults to loopback (the local
// Caddy reverse proxy); override with CONTEXO_TRUSTED_PROXIES (comma-separated).
func trustedProxies() []string {
	if raw := os.Getenv("CONTEXO_TRUSTED_PROXIES"); raw != "" {
		var out []string
		for _, p := range strings.Split(raw, ",") {
			if v := strings.TrimSpace(p); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	return []string{"127.0.0.1", "::1"}
}

// Default DoS-resilience limits. All are overridable via environment so a
// self-host operator can tune them without a rebuild; set CONTEXO_RATELIMIT_DISABLE
// to turn rate limiting off entirely.
const (
	defaultMaxBodyBytes   = 8 << 20 // 8 MiB
	defaultRatePerMin     = 300     // general per-IP requests/minute
	defaultRateBurst      = 100     // general per-IP burst
	defaultAuthRatePerMin = 15      // POST /v1/auth/google per-IP requests/minute
	defaultAuthRateBurst  = 8       // auth burst
)

func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func envInt(key string, def int) int {
	return int(envInt64(key, int64(def)))
}

// MaxBody caps the request body so a single client cannot exhaust server memory
// with a multi-gigabyte push body. Reads past the limit fail, and the handler
// surfaces that as a 4xx. GET/DELETE requests carry no body, so this is a no-op
// for them.
func MaxBody(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

// tokenBucket is a single per-key bucket refilled continuously at ratePerSec.
type tokenBucket struct {
	tokens float64
	last   time.Time
}

// rateLimiter is an in-memory per-key token-bucket limiter. It keeps the
// single-binary property (no Redis) and self-prunes idle keys. Keyed by client
// IP, it throttles floods (auth spray, push/git abuse) on the WAF-less origin.
type rateLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*tokenBucket
	ratePerSec float64
	burst      float64
	lastGC     time.Time
}

func newRateLimiter(perMin, burst int) *rateLimiter {
	return &rateLimiter{
		buckets:    make(map[string]*tokenBucket),
		ratePerSec: float64(perMin) / 60.0,
		burst:      float64(burst),
	}
}

func (rl *rateLimiter) allow(key string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b := rl.buckets[key]
	if b == nil {
		b = &tokenBucket{tokens: rl.burst, last: now}
		rl.buckets[key] = b
	} else {
		b.tokens += now.Sub(b.last).Seconds() * rl.ratePerSec
		if b.tokens > rl.burst {
			b.tokens = rl.burst
		}
		b.last = now
	}

	// Opportunistically prune buckets idle for >10m so the map can't grow
	// unbounded under IP churn.
	if now.Sub(rl.lastGC) > 10*time.Minute {
		for k, bb := range rl.buckets {
			if now.Sub(bb.last) > 10*time.Minute {
				delete(rl.buckets, k)
			}
		}
		rl.lastGC = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RateLimit returns middleware that rejects requests from a client IP that has
// exhausted its bucket with 429. /health is always exempt so uptime checks are
// never throttled.
func RateLimit(rl *rateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.FullPath() == "/health" {
			c.Next()
			return
		}
		if !rl.allow(c.ClientIP(), time.Now()) {
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}

// rateLimitingEnabled reports whether the per-IP limiters should be installed.
// On by default; CONTEXO_RATELIMIT_DISABLE=1 turns it off (self-host / tests).
func rateLimitingEnabled() bool {
	switch os.Getenv("CONTEXO_RATELIMIT_DISABLE") {
	case "1", "true", "yes":
		return false
	}
	return true
}
