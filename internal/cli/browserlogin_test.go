package cli

import (
	"net/url"
	"strings"
	"testing"
)

func TestBuildCliLoginURL(t *testing.T) {
	got, err := buildCliLoginURL("https://app.contexo.live/", 42713, "abc123")
	if err != nil {
		t.Fatalf("buildCliLoginURL: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Host != "app.contexo.live" {
		t.Errorf("host: got %q", u.Host)
	}
	if u.Path != "/cli-login" {
		t.Errorf("path: got %q", u.Path)
	}
	q := u.Query()
	if q.Get("port") != "42713" {
		t.Errorf("port query: got %q", q.Get("port"))
	}
	if q.Get("state") != "abc123" {
		t.Errorf("state query: got %q", q.Get("state"))
	}
	if q.Get("hostname") == "" {
		t.Errorf("hostname should be populated")
	}
}

func TestBuildCliLoginURLBadInput(t *testing.T) {
	_, err := buildCliLoginURL("://not-a-url", 0, "")
	if err == nil {
		t.Errorf("expected error for malformed URL")
	}
}

func TestRandomStateUniqueAndCorrectLength(t *testing.T) {
	s1, err := randomState(16)
	if err != nil {
		t.Fatalf("randomState: %v", err)
	}
	if len(s1) != 32 { // 16 bytes hex-encoded
		t.Errorf("len: got %d, want 32", len(s1))
	}
	s2, _ := randomState(16)
	if s1 == s2 {
		t.Errorf("two random states should differ")
	}
}

func TestSuccessAndFailureHTMLNonEmpty(t *testing.T) {
	if !strings.Contains(successHTML, "CLI authorized") {
		t.Errorf("success HTML missing expected copy")
	}
	if !strings.Contains(failureHTML, "{{REASON}}") {
		t.Errorf("failure HTML missing the reason placeholder")
	}
}

// TestBrowserLoginResultZeroValue documents the zero-value shape so that
// callers (auth.go) can rely on "" meaning "dashboard didn't send this."
func TestBrowserLoginResultZeroValue(t *testing.T) {
	var r BrowserLoginResult
	if r.Token != "" || r.Name != "" || r.Email != "" {
		t.Errorf("zero value should have empty fields, got %+v", r)
	}
}
