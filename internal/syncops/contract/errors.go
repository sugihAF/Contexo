package contract

// ErrorCode is a stable machine-readable failure code. New codes may be added
// in a minor release; callers map unknown codes by exit-code category.
type ErrorCode string

const (
	CodeInternal          ErrorCode = "internal"
	CodeUsage             ErrorCode = "usage"
	CodeInvalidPath       ErrorCode = "invalid_path"
	CodeNotAuthenticated  ErrorCode = "not_authenticated"
	CodeAuthRejected      ErrorCode = "auth_rejected"
	CodeForbidden         ErrorCode = "forbidden"
	CodeNotInitialized    ErrorCode = "not_initialized"
	CodeRepoNotConfigured ErrorCode = "repo_not_configured"
	CodeServerUnresolved  ErrorCode = "server_unresolved"
	CodeConflict          ErrorCode = "conflict"
	CodeStale             ErrorCode = "stale"
	CodeNetwork           ErrorCode = "network"
	CodeServerError       ErrorCode = "server_error"
	CodeRateLimited       ErrorCode = "rate_limited"
	CodeRepoNotFound      ErrorCode = "repo_not_found"
	CodePageNotFound      ErrorCode = "page_not_found"
	CodeBaseUnavailable   ErrorCode = "base_unavailable"
	CodeSHANotFound       ErrorCode = "sha_not_found"
	CodeDistillRequired   ErrorCode = "distill_required"
	CodeNotResolvable     ErrorCode = "not_resolvable"
	CodeServerUnsupported ErrorCode = "server_unsupported"
	// CodeNotImplemented is returned by a hidden stub command in a
	// pre-release build (E17). The extension treats it like ctx too old and
	// offers Update ctx. No release returns it for an advertised capability.
	CodeNotImplemented ErrorCode = "not_implemented"
	CodeQuotaExceeded  ErrorCode = "quota_exceeded"
	CodeBusy           ErrorCode = "busy"
)

// Exit codes (only in --json mode and for `ctx show`; human mode keeps 1).
// 2 is never used deliberately: the Go runtime exits 2 on a panic.
const (
	ExitOK          = 0
	ExitInternal    = 1
	ExitUsage       = 3
	ExitAuth        = 4
	ExitConfig      = 5
	ExitConflict    = 6
	ExitNetwork     = 7
	ExitNotFound    = 8
	ExitRefused     = 9
	ExitUnsupported = 10
	ExitQuota       = 11
	ExitBusy        = 12
)

type codeInfo struct {
	Exit      int
	Retryable bool
}

var codeTable = map[ErrorCode]codeInfo{
	CodeInternal:          {ExitInternal, false},
	CodeUsage:             {ExitUsage, false},
	CodeInvalidPath:       {ExitUsage, false},
	CodeNotAuthenticated:  {ExitAuth, false},
	CodeAuthRejected:      {ExitAuth, false},
	CodeForbidden:         {ExitAuth, false},
	CodeNotInitialized:    {ExitConfig, false},
	CodeRepoNotConfigured: {ExitConfig, false},
	CodeServerUnresolved:  {ExitConfig, false},
	CodeConflict:          {ExitConflict, false},
	CodeStale:             {ExitConflict, false},
	CodeNetwork:           {ExitNetwork, true},
	CodeServerError:       {ExitNetwork, true},
	CodeRateLimited:       {ExitNetwork, true},
	CodeRepoNotFound:      {ExitNotFound, false},
	CodePageNotFound:      {ExitNotFound, false},
	CodeBaseUnavailable:   {ExitNotFound, false},
	CodeSHANotFound:       {ExitNotFound, false},
	CodeDistillRequired:   {ExitRefused, false},
	CodeNotResolvable:     {ExitRefused, false},
	CodeServerUnsupported: {ExitUnsupported, false},
	CodeNotImplemented:    {ExitUnsupported, false},
	CodeQuotaExceeded:     {ExitQuota, false},
	CodeBusy:              {ExitBusy, true},
}

// ExitCodeFor returns the process exit code for code (internal for unknown).
func ExitCodeFor(code ErrorCode) int {
	if ci, ok := codeTable[code]; ok {
		return ci.Exit
	}
	return ExitInternal
}

// Retryable reports whether the same invocation may succeed if repeated.
func Retryable(code ErrorCode) bool { return codeTable[code].Retryable }

// KnownCode reports whether code is one of the stable error codes (E17).
func KnownCode(code ErrorCode) bool {
	_, ok := codeTable[code]
	return ok
}

// ErrorEnvelope is the ONE line of JSON written to stderr on failure.
type ErrorEnvelope struct {
	Schema   string    `json:"schema"`
	ExitCode int       `json:"exit_code"`
	Error    ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code       ErrorCode `json:"code"`
	Message    string    `json:"message"`
	Advice     []string  `json:"advice"` // shell commands that fix it; never null
	Retryable  bool      `json:"retryable"`
	HTTPStatus int       `json:"http_status,omitempty"`
	Details    any       `json:"details,omitempty"` // one of the *Details types below, keyed by Code
}

// Error is the Go error type that carries a contract code through RunE.
type Error struct {
	Code       ErrorCode
	Message    string
	Advice     []string
	HTTPStatus int
	Details    any
	Err        error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

// New builds a contract error.
func New(code ErrorCode, msg string, advice ...string) *Error {
	return &Error{Code: code, Message: msg, Advice: advice}
}

// Body converts e to its wire form.
func (e *Error) Body() ErrorBody {
	adv := e.Advice
	if adv == nil {
		adv = []string{}
	}
	return ErrorBody{Code: e.Code, Message: e.Message, Advice: adv,
		Retryable: Retryable(e.Code), HTTPStatus: e.HTTPStatus, Details: e.Details}
}

// Envelope converts e to the stderr envelope.
func (e *Error) Envelope() ErrorEnvelope {
	return ErrorEnvelope{Schema: SchemaError, ExitCode: ExitCodeFor(e.Code), Error: e.Body()}
}

// Per-code details payloads.

type DistillRequiredDetails struct { // distill_required
	Pages       []string         `json:"pages"`        // concept/analysis pages in the refused batch
	Sessions    []CaptureSession `json:"sessions"`     // fresh pending sessions
	AgentPrompt string           `json:"agent_prompt"` // text the UI offers to copy into the agent chat
}

type ConflictDetails struct { // conflict
	Paths []string `json:"paths"`
}

type StaleDetails struct { // stale
	Path        string `json:"path"`
	ExpectedSHA string `json:"expected_sha"`
	ServerSHA   string `json:"server_sha"`
}

type NotResolvableDetails struct { // not_resolvable
	Path     string          `json:"path"`
	State    PageState       `json:"state"`
	Strategy ResolveStrategy `json:"strategy"`
}

type ServerUnsupportedDetails struct { // server_unsupported
	Missing []ServerCapability `json:"missing"`
}

type BusyDetails struct { // busy
	PID       int    `json:"pid"`
	Host      string `json:"host"`
	Command   string `json:"command"`
	StartedAt Time   `json:"started_at"`
}

type QuotaDetails struct { // quota_exceeded (from the server's 402 body)
	QuotaCode  string `json:"quota_code"`
	Limit      int    `json:"limit"`
	UpgradeURL string `json:"upgrade_url,omitempty"`
}

// NewDetails returns a pointer to the typed details struct for code, or nil
// when the code carries no details. Decoders use it to keep field order.
func NewDetails(code ErrorCode) any {
	switch code {
	case CodeDistillRequired:
		return &DistillRequiredDetails{}
	case CodeConflict:
		return &ConflictDetails{}
	case CodeStale:
		return &StaleDetails{}
	case CodeNotResolvable:
		return &NotResolvableDetails{}
	case CodeServerUnsupported:
		return &ServerUnsupportedDetails{}
	case CodeBusy:
		return &BusyDetails{}
	case CodeQuotaExceeded:
		return &QuotaDetails{}
	}
	return nil
}
