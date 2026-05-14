package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// KeyValidator is a function that checks if a key is valid and returns the user ID.
type KeyValidator func(key string) (userID string, valid bool)

// GinMiddleware returns Gin middleware that validates API keys.
func GinMiddleware(validate KeyValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing authorization header",
			})
			return
		}

		key := strings.TrimPrefix(authHeader, "Bearer ")
		if key == authHeader {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid authorization format, expected Bearer <key>",
			})
			return
		}

		userID, valid := validate(key)
		if !valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid API key",
			})
			return
		}

		c.Set("user_id", userID)
		c.Next()
	}
}
