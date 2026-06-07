package sync

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
