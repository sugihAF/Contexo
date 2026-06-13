package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRateLimiterAllow(t *testing.T) {
	rl := newRateLimiter(60, 3) // 1 token/sec, burst 3
	base := time.Unix(1_000_000, 0)

	for i := 0; i < 3; i++ {
		if !rl.allow("ip1", base) {
			t.Fatalf("request %d should be allowed within burst", i)
		}
	}
	if rl.allow("ip1", base) {
		t.Fatal("4th request at the same instant should be denied")
	}
	// 2 seconds later, ~2 tokens have refilled.
	if !rl.allow("ip1", base.Add(2*time.Second)) {
		t.Fatal("request should be allowed after refill")
	}
	// A different IP has its own full bucket.
	if !rl.allow("ip2", base) {
		t.Fatal("ip2 should start with a full burst")
	}
}

func TestMaxBodyRejectsOversize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MaxBody(10))
	r.POST("/x", func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.JSON(413, gin.H{"error": "too large"})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/x", strings.NewReader("hello")))
	if w.Code != 200 {
		t.Fatalf("small body: code=%d, want 200", w.Code)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat("a", 50))))
	if w.Code != 413 {
		t.Fatalf("oversize body: code=%d, want 413", w.Code)
	}
}

func TestRateLimitMiddleware429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimit(newRateLimiter(60, 2))) // burst 2
	r.GET("/x", func(c *gin.Context) { c.JSON(200, gin.H{}) })

	var codes []int
	for i := 0; i < 4; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = "9.9.9.9:1234"
		r.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("codes=%v, want 200,200,429,429", codes)
	}
}
