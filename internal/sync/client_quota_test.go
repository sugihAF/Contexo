package sync

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// A 402 from the server (hosted quota hit) should reach the user as a clean
// upgrade prompt, not raw JSON or an HTTP-status dump.
func TestJoinRepo_402SurfacesCleanUpgradeMessage(t *testing.T) {
	const msg = "Free tier is limited to 3 members per repo. The owner can upgrade to Team for unlimited members: https://contexo.live/#pricing"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"` + msg + `","code":"quota_members","limit":3,"upgrade_url":"https://contexo.live/#pricing"}`))
	}))
	defer srv.Close()

	_, _, err := NewClient(srv.URL, "token").JoinRepo("ctxi_whatever")
	if err == nil {
		t.Fatal("expected an error on 402")
	}
	if err.Error() != msg {
		t.Errorf("want the clean upgrade message, got: %q", err.Error())
	}
	if strings.Contains(err.Error(), "{") || strings.Contains(err.Error(), "402") {
		t.Errorf("error should not leak raw JSON or status code: %q", err.Error())
	}
}

// `ctx push` to a new repo over the cap gets a 402 — it must reach the user as a
// clean upgrade prompt, not raw JSON.
func TestPushPages_402SurfacesCleanUpgradeMessage(t *testing.T) {
	const msg = "Free tier is limited to 5 repos. Upgrade to Team ($10/mo) for unlimited repos: https://contexo.live/#pricing"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"` + msg + `","code":"quota_repos","limit":5,"upgrade_url":"https://contexo.live/#pricing"}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "token").PushPages("newrepo", &PushRequest{
		Files: []PushFile{{Path: "wiki/concepts/a.md", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected an error on 402")
	}
	if err.Error() != msg {
		t.Errorf("want the clean upgrade message, got: %q", err.Error())
	}
	if strings.Contains(err.Error(), "{") || strings.Contains(err.Error(), "402") {
		t.Errorf("error should not leak raw JSON or status code: %q", err.Error())
	}
}

// A 402 is also a typed quota_exceeded error carrying the body's quota code,
// limit and upgrade URL as QuotaDetails, while Error() stays the clean text.
func TestQuota402IsTypedQuotaExceeded(t *testing.T) {
	const body = `{"error":"Free tier is limited to 3 members per repo.","code":"quota_members","limit":3,"upgrade_url":"https://contexo.live/#pricing"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	_, _, joinErr := c.JoinRepo("ctxi_whatever")
	_, pushErr := c.PushPages("newrepo", &PushRequest{Files: []PushFile{{Path: "wiki/concepts/a.md", Content: "x"}}})
	for name, err := range map[string]error{"join": joinErr, "push": pushErr} {
		var he *HTTPError
		if !errors.As(err, &he) {
			t.Fatalf("%s: %T is not an *HTTPError", name, err)
		}
		if he.Code != contract.CodeQuotaExceeded || he.ServerCode != "quota_members" || he.HTTPStatus() != 402 {
			t.Errorf("%s: Code=%q ServerCode=%q Status=%d", name, he.Code, he.ServerCode, he.HTTPStatus())
		}
		want := contract.QuotaDetails{QuotaCode: "quota_members", Limit: 3, UpgradeURL: "https://contexo.live/#pricing"}
		if he.ErrorDetails() != any(want) {
			t.Errorf("%s: details = %#v, want %#v", name, he.ErrorDetails(), want)
		}
		if err.Error() != "Free tier is limited to 3 members per repo." {
			t.Errorf("%s: Error() = %q", name, err.Error())
		}
	}
}

// A 402 whose body is not the JSON quota shape keeps the legacy fallback text.
func TestQuota402NonJSONKeepsFallbackText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte("payment required"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	_, _, joinErr := c.JoinRepo("ctxi_whatever")
	_, pushErr := c.PushPages("newrepo", &PushRequest{})
	for name, err := range map[string]error{"join": joinErr, "push": pushErr} {
		if err == nil || err.Error() != "upgrade required (402): payment required" {
			t.Errorf("%s: Error() = %v", name, err)
		}
		var he *HTTPError
		if !errors.As(err, &he) || he.Code != contract.CodeQuotaExceeded {
			t.Errorf("%s: not a quota_exceeded *HTTPError: %#v", name, err)
		}
	}
}
