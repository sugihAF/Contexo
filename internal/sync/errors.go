package sync

import (
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
func NewHTTPError(status int, body []byte, msg string, sentinel error, at bool) *HTTPError {
	return &HTTPError{Status: status, Code: contract.CodeInternal, Body: string(body), Msg: msg, sentinel: sentinel}
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
