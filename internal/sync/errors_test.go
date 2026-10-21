package sync

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// TestNewHTTPErrorMapping pins the single status -> code mapping (E14 plus
// the WP-0.1 lead rulings) independently of any Client method.
func TestNewHTTPErrorMapping(t *testing.T) {
	atMissing := contract.ServerUnsupportedDetails{
		Missing: []contract.ServerCapability{contract.SrvPagesAt},
	}
	cases := []struct {
		name       string
		status     int
		body       string
		at         bool
		code       contract.ErrorCode
		serverCode string
		details    any
	}{
		{"400 invalid_path", 400, `{"error":"bad path","code":"invalid_path"}`, false, contract.CodeInvalidPath, "invalid_path", nil},
		{"400 invalid_sha is usage", 400, `{"error":"bad sha","code":"invalid_sha"}`, true, contract.CodeUsage, "invalid_sha", nil},
		{"400 codeless is usage", 400, `{"error":"bad request"}`, false, contract.CodeUsage, "", nil},
		{"400 non-JSON is usage", 400, `bad request`, false, contract.CodeUsage, "", nil},
		{"401", 401, `{"error":"bad token"}`, false, contract.CodeAuthRejected, "", nil},
		{"402 quota details", 402,
			`{"error":"Free tier is limited to 5 repos.","code":"quota_repos","limit":5,"upgrade_url":"https://contexo.live/#pricing"}`,
			false, contract.CodeQuotaExceeded, "quota_repos",
			contract.QuotaDetails{QuotaCode: "quota_repos", Limit: 5, UpgradeURL: "https://contexo.live/#pricing"}},
		{"402 non-JSON", 402, `payment required`, false, contract.CodeQuotaExceeded, "", contract.QuotaDetails{}},
		{"403", 403, `{"error":"not a member"}`, false, contract.CodeForbidden, "", nil},
		{"404 body page_not_found", 404, `{"error":"x","code":"page_not_found"}`, true, contract.CodePageNotFound, "page_not_found", nil},
		{"404 body repo_not_found beats at", 404, `{"error":"x","code":"repo_not_found"}`, true, contract.CodeRepoNotFound, "repo_not_found", nil},
		{"404 body sha_not_found", 404, `{"error":"x","code":"sha_not_found"}`, false, contract.CodeSHANotFound, "sha_not_found", nil},
		{"404 codeless repo not found", 404, `{"error":"repo not found"}`, false, contract.CodeRepoNotFound, "", nil},
		{"404 codeless repo not found on at", 404, `{"error":"repo not found"}`, true, contract.CodeRepoNotFound, "", nil},
		{"404 codeless at is sha_not_found", 404, `{"error":"path not found at sha"}`, true, contract.CodeSHANotFound, "", nil},
		{"404 codeless page", 404, `{"error":"page not found"}`, false, contract.CodePageNotFound, "", nil},
		{"404 unknown code falls back", 404, `{"error":"x","code":"invalid_path"}`, false, contract.CodePageNotFound, "invalid_path", nil},
		{"404 non-JSON", 404, `404 page not found`, false, contract.CodeServerUnsupported, "", nil},
		{"404 empty body", 404, ``, false, contract.CodeServerUnsupported, "", nil},
		{"404 non-JSON at", 404, `404 page not found`, true, contract.CodeServerUnsupported, "", atMissing},
		{"409 is never conflict", 409, `{"error":"conflict"}`, false, contract.CodeInternal, "", nil},
		{"418 other 4xx", 418, `teapot`, false, contract.CodeInternal, "", nil},
		{"429", 429, `{"error":"slow down"}`, false, contract.CodeRateLimited, "", nil},
		{"500", 500, `boom`, false, contract.CodeServerError, "", nil},
		{"501", 501, `{"error":"not implemented"}`, false, contract.CodeServerError, "", nil},
		{"503", 503, `unavailable`, false, contract.CodeServerError, "", nil},
		{"garbled 200", 200, `garbage{`, false, contract.CodeServerError, "", nil},
		{"garbled 201", 201, `{"key":`, false, contract.CodeServerError, "", nil},
		{"302 internal", 302, ``, false, contract.CodeInternal, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewHTTPError(tc.status, []byte(tc.body), "legacy text", nil, tc.at)
			if e.Code != tc.code {
				t.Errorf("Code = %q, want %q", e.Code, tc.code)
			}
			if e.ErrorCode() != string(tc.code) {
				t.Errorf("ErrorCode() = %q, want %q", e.ErrorCode(), tc.code)
			}
			if e.ServerCode != tc.serverCode {
				t.Errorf("ServerCode = %q, want %q", e.ServerCode, tc.serverCode)
			}
			if e.Status != tc.status || e.HTTPStatus() != tc.status {
				t.Errorf("Status = %d / HTTPStatus() = %d, want %d", e.Status, e.HTTPStatus(), tc.status)
			}
			if e.Body != tc.body {
				t.Errorf("Body = %q, want %q", e.Body, tc.body)
			}
			if e.Error() != "legacy text" || e.Msg != "legacy text" {
				t.Errorf("Error() = %q, Msg = %q, want the legacy text", e.Error(), e.Msg)
			}
			if !reflect.DeepEqual(e.Details, tc.details) {
				t.Errorf("Details = %#v, want %#v", e.Details, tc.details)
			}
			if !reflect.DeepEqual(e.ErrorDetails(), tc.details) {
				t.Errorf("ErrorDetails() = %#v, want %#v", e.ErrorDetails(), tc.details)
			}
		})
	}
}

// TestHTTPErrorIsCodeSentinels: the four code sentinels match by Code, and
// Unwrap still reaches the legacy sentinel passed in.
func TestHTTPErrorIsCodeSentinels(t *testing.T) {
	sentinels := map[contract.ErrorCode]error{
		contract.CodeRepoNotFound:      ErrRepoNotFound,
		contract.CodeSHANotFound:       ErrSHANotFound,
		contract.CodeServerUnsupported: ErrServerUnsupported,
		contract.CodePageNotFound:      ErrPageNotFound,
	}
	bodies := map[contract.ErrorCode]string{
		contract.CodeRepoNotFound:      `{"error":"x","code":"repo_not_found"}`,
		contract.CodeSHANotFound:       `{"error":"x","code":"sha_not_found"}`,
		contract.CodeServerUnsupported: `not json`,
		contract.CodePageNotFound:      `{"error":"x","code":"page_not_found"}`,
	}
	for code, body := range bodies {
		e := NewHTTPError(404, []byte(body), "m", nil, false)
		if e.Code != code {
			t.Fatalf("setup: body %s mapped to %q, want %q", body, e.Code, code)
		}
		for other, s := range sentinels {
			if got, want := errors.Is(e, s), other == code; got != want {
				t.Errorf("code %q: errors.Is(%v) = %v, want %v", code, s, got, want)
			}
		}
	}

	legacy := errors.New("legacy sentinel")
	e := NewHTTPError(403, []byte(`{"error":"owner role required"}`), legacy.Error(), legacy, false)
	if !errors.Is(e, legacy) {
		t.Error("errors.Is(legacy sentinel) = false, want true via Unwrap")
	}
	if !errors.Is(fmt.Errorf("wrapped: %w", e), legacy) {
		t.Error("wrapped HTTPError lost the legacy sentinel")
	}
	if errors.Is(e, ErrPageNotFound) || errors.Is(e, ErrRepoNotFound) {
		t.Error("a 403 must not match a not-found sentinel")
	}
	var he *HTTPError
	if !errors.As(fmt.Errorf("wrapped: %w", e), &he) || he != e {
		t.Error("errors.As(*HTTPError) failed through a wrap")
	}
}

// TestNetworkErrorText: NetworkError keeps the legacy fmt.Errorf("sync: <op>:
// %w") text byte-for-byte and stays unwrappable to the transport error.
func TestNetworkErrorText(t *testing.T) {
	inner := &url.Error{Op: "Get", URL: "http://127.0.0.1:1/v1/repos", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
	for _, op := range []string{"create repo", "push", "pull", "join", "list repos", "timeline",
		"mint invite key", "list invite keys", "read page", "page history", "page evolution",
		"page diff", "delete invite key", "list members", "remove member", "list activity",
		"distill", "list pages", "read page at"} {
		ne := &NetworkError{Op: op, Err: inner}
		if want := fmt.Errorf("sync: %s: %w", op, inner).Error(); ne.Error() != want {
			t.Errorf("op %q: Error() = %q, want %q", op, ne.Error(), want)
		}
		if ne.ErrorCode() != "network" {
			t.Errorf("op %q: ErrorCode() = %q, want network", op, ne.ErrorCode())
		}
		var ue *url.Error
		if !errors.As(ne, &ue) || ue != inner {
			t.Errorf("op %q: errors.As(*url.Error) failed", op)
		}
	}
}
