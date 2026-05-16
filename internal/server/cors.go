package server

import (
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS returns a Gin middleware that allows requests from the origins listed in
// the CONTEXO_CORS_ORIGINS env var (comma-separated). Defaults to allowing
// localhost:5173 and localhost:3000 for local dev when unset.
//
// Sets Allow-Origin only when the request's Origin matches the allow-list, so
// unauthenticated origins get no special treatment. Allow-Credentials is NOT
// set because the API uses Bearer tokens, not cookies.
func CORS() gin.HandlerFunc {
	raw := os.Getenv("CONTEXO_CORS_ORIGINS")
	var origins []string
	if raw == "" {
		origins = []string{"http://localhost:5173", "http://localhost:3000"}
	} else {
		for _, o := range strings.Split(raw, ",") {
			if v := strings.TrimSpace(o); v != "" {
				origins = append(origins, v)
			}
		}
	}
	allowed := map[string]bool{}
	for _, o := range origins {
		allowed[o] = true
	}
	wildcard := allowed["*"]

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if wildcard {
				c.Header("Access-Control-Allow-Origin", "*")
			} else if allowed[origin] {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Vary", "Origin")
			}
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Page-SHA")
			c.Header("Access-Control-Expose-Headers", "X-Page-SHA")
			c.Header("Access-Control-Max-Age", "3600")
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
