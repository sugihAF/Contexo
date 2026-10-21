package sync

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// New sentinels (E4). Existing sentinels (ErrPageNotFound, ErrNotOwner,
// ErrMemberNotFound, ErrLastOwner) are unchanged.
var (
	// ErrServerUnsupported: the server lacks a needed route or capability
	// (e.g. a ?at= read without the X-Contexo-At echo).
	ErrServerUnsupported = errors.New("sync: server unsupported")
	// ErrSHANotFound: the requested sha is unknown, or the path is absent at it.
	ErrSHANotFound = errors.New("sync: sha not found")
	// ErrRepoNotFound: the server has no such repo.
	ErrRepoNotFound = errors.New("sync: repo not found")
)

// HTTPError is a non-success HTTP response (or an unparseable success body),
// mapped to a contract error code (E14). Error() is the legacy per-method
// text, byte-stable; Unwrap() returns the legacy sentinel so existing
// errors.Is callers keep working.
type HTTPError struct {
	Status     int
	Code       contract.ErrorCode
	ServerCode string // the JSON body's "code", when present
	Body       string
	Msg        string
	Details    any
	sentinel   error
}

func (e *HTTPError) Error() string { return e.Msg }

// Unwrap returns the legacy sentinel, or nil.
func (e *HTTPError) Unwrap() error { return e.sentinel }

// Is matches the code sentinels by Code, independent of the legacy sentinel
// Unwrap returns: ErrRepoNotFound, ErrSHANotFound, ErrServerUnsupported and
// ErrPageNotFound each match iff Code is the corresponding contract code.
func (e *HTTPError) Is(target error) bool {
	switch target {
	case ErrRepoNotFound:
		return e.Code == contract.CodeRepoNotFound
	case ErrSHANotFound:
		return e.Code == contract.CodeSHANotFound
	case ErrServerUnsupported:
		return e.Code == contract.CodeServerUnsupported
	case ErrPageNotFound:
		return e.Code == contract.CodePageNotFound
	}
	return false
}

// ErrorCode is the duck-typed hook ui.ToContractError reads.
func (e *HTTPError) ErrorCode() string { return string(e.Code) }

// HTTPStatus is the duck-typed hook that fills the envelope's http_status.
func (e *HTTPError) HTTPStatus() int { return e.Status }

// ErrorDetails is the duck-typed hook that fills the envelope's details.
func (e *HTTPError) ErrorDetails() any { return e.Details }

// NewHTTPError is the single status -> code mapping (E14). Every Client
// method uses it; it is exported so fakes (syncopstest) produce identical
// values. msg is the legacy Error() text, sentinel the legacy sentinel (may
// be nil), and at reports a GET pages/*path?at=<sha> request.
//
// Mapping:
//   - 2xx (only reached for an unparseable body): server_error
//   - 400: invalid_path iff the JSON body code is invalid_path, else usage
//   - 401 auth_rejected; 403 forbidden; 429 rate_limited; 5xx server_error
//   - 402: quota_exceeded, Details contract.QuotaDetails from the body
//   - 404: the body code when it is page_not_found, repo_not_found or
//     sha_not_found; a JSON body without one is repo_not_found iff its
//     error is "repo not found", else sha_not_found when at, else
//     page_not_found; a non-JSON 404 (route missing) is server_unsupported,
//     with Details {Missing: [pages.at]} when at
//   - anything else, including 409 (never conflict), is internal
func NewHTTPError(status int, body []byte, msg string, sentinel error, at bool) *HTTPError {
	sb := parseServerBody(body)
	e := &HTTPError{
		Status:     status,
		Code:       contract.CodeInternal,
		ServerCode: sb.code,
		Body:       string(body),
		Msg:        msg,
		sentinel:   sentinel,
	}
	switch {
	case status >= 200 && status < 300:
		e.Code = contract.CodeServerError
	case status == 400:
		e.Code = contract.CodeUsage
		if sb.code == string(contract.CodeInvalidPath) {
			e.Code = contract.CodeInvalidPath
		}
	case status == 401:
		e.Code = contract.CodeAuthRejected
	case status == 402:
		e.Code = contract.CodeQuotaExceeded
		e.Details = contract.QuotaDetails{QuotaCode: sb.code, Limit: sb.limit, UpgradeURL: sb.upgradeURL}
	case status == 403:
		e.Code = contract.CodeForbidden
	case status == 404:
		e.Code, e.Details = map404(sb, at)
	case status == 429:
		e.Code = contract.CodeRateLimited
	case status >= 500 && status < 600:
		e.Code = contract.CodeServerError
	}
	return e
}

// map404 picks the code (and details) of a 404 response.
func map404(sb serverBody, at bool) (contract.ErrorCode, any) {
	if !sb.isJSON {
		// A route the server does not have (Gin's text/plain 404).
		if at {
			return contract.CodeServerUnsupported, contract.ServerUnsupportedDetails{
				Missing: []contract.ServerCapability{contract.SrvPagesAt},
			}
		}
		return contract.CodeServerUnsupported, nil
	}
	switch c := contract.ErrorCode(sb.code); c {
	case contract.CodePageNotFound, contract.CodeRepoNotFound, contract.CodeSHANotFound:
		return c, nil
	}
	switch {
	case sb.err == "repo not found":
		return contract.CodeRepoNotFound, nil
	case at:
		return contract.CodeSHANotFound, nil
	}
	return contract.CodePageNotFound, nil
}

// serverBody is the subset of a server error body the mapping reads:
// {"error", "code", "limit", "upgrade_url"} (handler.go 402 shape, WP-0.2
// per-code 404/400 bodies). isJSON is true iff the body is a JSON object.
type serverBody struct {
	isJSON     bool
	err        string
	code       string
	limit      int
	upgradeURL string
}

// parseServerBody reads the fields leniently: a field of an unexpected type
// is ignored rather than making the whole body "not JSON".
func parseServerBody(body []byte) serverBody {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil || m == nil {
		return serverBody{}
	}
	sb := serverBody{isJSON: true}
	if raw, ok := m["error"]; ok {
		_ = json.Unmarshal(raw, &sb.err)
	}
	if raw, ok := m["code"]; ok {
		_ = json.Unmarshal(raw, &sb.code)
	}
	if raw, ok := m["limit"]; ok {
		_ = json.Unmarshal(raw, &sb.limit)
	}
	if raw, ok := m["upgrade_url"]; ok {
		_ = json.Unmarshal(raw, &sb.upgradeURL)
	}
	return sb
}

// NetworkError is a transport failure (DNS, refused, reset, timeout) before
// any HTTP response arrived. Error() keeps the legacy "sync: <op>: <err>" text.
type NetworkError struct {
	Op  string
	Err error
}

func (e *NetworkError) Error() string { return fmt.Sprintf("sync: %s: %v", e.Op, e.Err) }

// Unwrap returns the transport error (*url.Error and friends).
func (e *NetworkError) Unwrap() error { return e.Err }

// ErrorCode is the duck-typed hook ui.ToContractError reads.
func (e *NetworkError) ErrorCode() string { return string(contract.CodeNetwork) }
