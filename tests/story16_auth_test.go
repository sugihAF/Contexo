package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/config"
)

func TestStory16_GenerateAPIKey(t *testing.T) {
	key, err := auth.GenerateAPIKey()
	require.NoError(t, err)
	assert.NotEmpty(t, key)
	// base64url encoded 32 bytes = 43 chars
	assert.GreaterOrEqual(t, len(key), 40)
}

func TestStory16_HashValidateRoundTrip(t *testing.T) {
	key, err := auth.GenerateAPIKey()
	require.NoError(t, err)

	hash := auth.HashKey(key)
	assert.NotEmpty(t, hash)
	assert.True(t, auth.ValidateKey(key, hash))
	assert.False(t, auth.ValidateKey("wrong-key", hash))
}

func TestStory16_GinMiddlewareRejects(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validator := func(key string) (string, bool) {
		if key == "valid-key" {
			return "user-1", true
		}
		return "", false
	}

	r := gin.New()
	r.Use(auth.GinMiddleware(validator))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"user": c.GetString("user_id")})
	})

	// No auth header
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Invalid key
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer bad-key")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestStory16_GinMiddlewareAccepts(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validator := func(key string) (string, bool) {
		if key == "valid-key" {
			return "user-1", true
		}
		return "", false
	}

	r := gin.New()
	r.Use(auth.GinMiddleware(validator))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"user": c.GetString("user_id")})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer valid-key")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "user-1")
}

func TestStory16_CredentialsSaveLoad(t *testing.T) {
	root := t.TempDir()

	creds := &config.Credentials{
		APIKey:    "test-key-123",
		ServerURL: "https://ctxhub.example.com",
		UserID:    "user-1",
	}

	err := config.SaveCredentials(root, creds)
	require.NoError(t, err)

	loaded, err := config.LoadCredentials(root)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, "test-key-123", loaded.APIKey)
	assert.Equal(t, "https://ctxhub.example.com", loaded.ServerURL)
}
