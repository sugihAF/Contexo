package contract

// PageState is the CLOSED v1 enum. Adding a value is a contract major bump.
type PageState string

const (
	StateUnchanged    PageState = "unchanged"
	StateModified     PageState = "modified"
	StateNew          PageState = "new"
	StateIncoming     PageState = "incoming"
	StateIncomingNew  PageState = "incoming_new"
	StateConflict     PageState = "conflict"
	StateDeletedLocal PageState = "deleted_local"
	StateInvalid      PageState = "invalid"
	StateUnverified   PageState = "unverified"
)

type ConflictKind string

const (
	ConflictBothModified ConflictKind = "both_modified"
	ConflictBothAdded    ConflictKind = "both_added"
)

type InvalidStage string

const (
	InvalidParse        InvalidStage = "parse"         // schema.ParsePage failed
	InvalidValidate     InvalidStage = "validate"      // PageFrontmatter.Validate failed
	InvalidPathMismatch InvalidStage = "path_mismatch" // file path != frontmatter RelPath()
)

type NoteCode string

const (
	NoteMissingOnServer NoteCode = "missing_on_server" // a base exists but the server no longer lists the path
	NoteRemoteChanged   NoteCode = "remote_changed"    // server moved past base (on invalid / deleted_local entries)
	NoteCaseCollision   NoteCode = "case_collision"    // another path differs only by letter case
)

type URLSource string

const (
	URLFromCredentials URLSource = "credentials"
	URLFromConfig      URLSource = "config"
	URLFromDefault     URLSource = "default"
)

type AuthState string

const (
	AuthAuthenticated    AuthState = "authenticated"     // credentials present and the server accepted them this run
	AuthUnchecked        AuthState = "unchecked"         // credentials present, server not contacted (offline / network error)
	AuthNotAuthenticated AuthState = "not_authenticated" // no credentials file with a token
	AuthRejected         AuthState = "rejected"          // server answered 401
)

type RemoteSource string

const (
	RemoteLive RemoteSource = "live" // fetched during this invocation
	// RemoteCache is reserved; v1 never emits it (E8: remote.json is reserved,
	// never written or read). Offline and failed-fetch views use RemoteNone.
	RemoteCache RemoteSource = "cache"
	RemoteNone  RemoteSource = "none" // nothing known about the server (sidecars may still supply R, E9)
)

// StatusDoc is stdout of `ctx status --json [--offline]`.
type StatusDoc struct {
	Schema      string      `json:"schema"`
	GeneratedAt Time        `json:"generated_at"`
	Offline     bool        `json:"offline"`
	Root        string      `json:"root"`        // absolute project root, native separators
	ContexoDir  string      `json:"contexo_dir"` // absolute .contexo dir, native separators
	Project     ProjectInfo `json:"project"`
	Server      ServerInfo  `json:"server"`
	Auth        AuthInfo    `json:"auth"`
	Remote      RemoteInfo  `json:"remote"`
	Counts      StateCounts `json:"counts"`
	Pages       []PageEntry `json:"pages"` // sorted by path (byte order); never null
	Captures    CaptureInfo `json:"captures"`
	Warnings    []Warning   `json:"warnings"` // never null
}

// ProjectInfo describes the project's configuration.
//
// DashboardURL is always present and may be "" when the server is self-hosted
// and no dashboard URL is configured (E23, WP-0.3 ResolveRemote rule). This is
// an explicit exception to "optional scalars are omitted".
// DashboardPageURLTemplate is omitted when DashboardURL is "", and the
// extension then disables Open in dashboard.
type ProjectInfo struct {
	RepoID                   string `json:"repo_id,omitempty"`
	ConfigServerURL          string `json:"config_server_url,omitempty"`
	DashboardURL             string `json:"dashboard_url"`
	DashboardPageURLTemplate string `json:"dashboard_page_url_template,omitempty"` // "{path}" is replaced by a page path
	LastPullSHA              string `json:"last_pull_sha,omitempty"`
}

type ServerInfo struct {
	URL               string             `json:"url,omitempty"`
	URLSource         URLSource          `json:"url_source,omitempty"`
	Version           string             `json:"version,omitempty"`
	CapabilitiesKnown bool               `json:"capabilities_known"`
	Capabilities      []ServerCapability `json:"capabilities"` // never null
}

type AuthInfo struct {
	State             AuthState `json:"state"`
	CredentialsSource string    `json:"credentials_source,omitempty"` // "project" | "global"
	TokenKind         string    `json:"token_kind,omitempty"`         // "pat" | "session" | "legacy" | "invite_key"
	UserName          string    `json:"user_name,omitempty"`
	UserEmail         string    `json:"user_email,omitempty"`
}

type RemoteInfo struct {
	Source     RemoteSource `json:"source"`
	FetchedAt  *Time        `json:"fetched_at,omitempty"`
	RepoExists *bool        `json:"repo_exists,omitempty"`
	Head       string       `json:"head,omitempty"`
	PageCount  int          `json:"page_count"`
	Error      *ErrorBody   `json:"error,omitempty"` // why a live fetch failed (status still exits 0)
}

type StateCounts struct {
	Unchanged    int `json:"unchanged"`
	Modified     int `json:"modified"`
	New          int `json:"new"`
	Incoming     int `json:"incoming"`
	IncomingNew  int `json:"incoming_new"`
	Conflict     int `json:"conflict"`
	DeletedLocal int `json:"deleted_local"`
	Invalid      int `json:"invalid"`
	Unverified   int `json:"unverified"`
	Ahead        int `json:"ahead"`  // modified + new
	Behind       int `json:"behind"` // incoming + incoming_new
}

// PageEntry is one path in the union of: working files, base records, and
// server paths. Path is a '/'-separated path relative to .contexo/.
type PageEntry struct {
	Path             string       `json:"path"`
	State            PageState    `json:"state"`
	ConflictKind     ConflictKind `json:"conflict_kind,omitempty"` // only when state == conflict
	Type             string       `json:"type,omitempty"`          // from local frontmatter when valid
	Slug             string       `json:"slug,omitempty"`
	Title            string       `json:"title,omitempty"` // first "# " line of the body
	Tags             []string     `json:"tags"`            // never null
	Author           string       `json:"author,omitempty"`
	Agent            string       `json:"agent,omitempty"`
	Updated          *Time        `json:"updated,omitempty"`
	ReasoningSummary string       `json:"reasoning_summary,omitempty"`
	Local            *LocalSide   `json:"local,omitempty"`
	Base             *BaseSide    `json:"base,omitempty"`
	Remote           *RemoteSide  `json:"remote,omitempty"`
	Invalid          *InvalidInfo `json:"invalid,omitempty"` // only when state == invalid
	Notes            []NoteCode   `json:"notes"`             // never null
}

type LocalSide struct {
	File string `json:"file"`           // working-file path relative to .contexo/ (== Path in v1)
	Hash string `json:"hash,omitempty"` // "sha256:<hex>" of normalized bytes; absent when invalid
}

type BaseSide struct {
	SHA      string `json:"sha"`                 // server commit the base corresponds to (== state.json page_shas[path])
	Hash     string `json:"hash,omitempty"`      // absent when the base record is stale
	SyncedAt *Time  `json:"synced_at,omitempty"` // absent when stale
	Stale    bool   `json:"stale"`
}

// RemoteSide is the server side of an entry. When it comes from a conflict
// sidecar instead of a live listing (E9), only SHA is set.
type RemoteSide struct {
	SHA     string `json:"sha"` // last commit that touched the path on the server
	Author  string `json:"author,omitempty"`
	Email   string `json:"email,omitempty"`
	Time    *Time  `json:"time,omitempty"`
	Message string `json:"message,omitempty"`
}

type InvalidInfo struct {
	Stage        InvalidStage `json:"stage"`
	Message      string       `json:"message"`
	ExpectedPath string       `json:"expected_path,omitempty"` // path_mismatch only
}

type CaptureInfo struct {
	Pending    int              `json:"pending"`     // non-archived buffers in raw/sessions/_pending
	Fresh      int              `json:"fresh"`       // pending, >=1 turn, modified within the 6h distill window
	BlocksPush bool             `json:"blocks_push"` // fresh > 0 AND a pushable page is concept/analysis
	Sessions   []CaptureSession `json:"sessions"`    // most recent first; never null
}

type CaptureSession struct {
	SessionID string `json:"session_id"`
	Turns     int    `json:"turns"`
	UpdatedAt Time   `json:"updated_at"`
	Fresh     bool   `json:"fresh"`
}
