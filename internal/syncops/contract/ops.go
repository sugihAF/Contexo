package contract

// SkipReason explains why push/pull left a path alone.
type SkipReason string

const (
	SkipUnchanged         SkipReason = "unchanged"           // push: named path has nothing to send
	SkipIncoming          SkipReason = "incoming"            // push: named path is behind the server; pull first
	SkipConflict          SkipReason = "conflict"            // push+pull: resolve first
	SkipInvalid           SkipReason = "invalid"             // push+pull: fix the file first
	SkipDeletedLocal      SkipReason = "deleted_local"       // push: deletes are not synced in v1; pull: never resurrects
	SkipUnverified        SkipReason = "unverified"          // push+pull: base could not be verified
	SkipModified          SkipReason = "modified"            // pull: local edits are never overwritten
	SkipChangedDuringPull SkipReason = "changed_during_pull" // pull: file changed between classify and write
	SkipCaseCollision     SkipReason = "case_collision"      // pull: would overwrite a case-variant file
	SkipNotFound          SkipReason = "not_found"           // push: named path unknown locally and remotely
)

type SkippedPage struct {
	Path      string     `json:"path"`
	State     PageState  `json:"state,omitempty"` // classification at the time (absent for not_found)
	Reason    SkipReason `json:"reason"`
	ServerSHA string     `json:"server_sha,omitempty"`
}

type WriteKind string

const (
	WriteCreate WriteKind = "create"
	WriteUpdate WriteKind = "update"
)

// PushDoc is stdout of `ctx push --json`. Present on exit 0 AND on exit 6
// (conflict): pages without conflicts in the same batch were committed.
type PushDoc struct {
	Schema    string         `json:"schema"`
	DryRun    bool           `json:"dry_run"`
	Message   string         `json:"message"`
	ServerURL string         `json:"server_url"`
	RepoID    string         `json:"repo_id"`
	NewHead   string         `json:"new_head,omitempty"` // absent on dry-run / nothing sent
	Pushed    []PushedPage   `json:"pushed"`             // dry-run: what would be sent (sha absent)
	Conflicts []PushConflict `json:"conflicts"`
	Skipped   []SkippedPage  `json:"skipped"`
	Distill   DistillCheck   `json:"distill"`
	Warnings  []Warning      `json:"warnings"`
}

type PushedPage struct {
	Path string    `json:"path"`
	Kind WriteKind `json:"kind"`
	SHA  string    `json:"sha,omitempty"` // the path's new last-touch commit
}

type PushConflict struct {
	Path      string       `json:"path"`
	Kind      ConflictKind `json:"kind"`
	BaseSHA   string       `json:"base_sha,omitempty"` // parent_sha that was sent (absent for both_added)
	ServerSHA string       `json:"server_sha"`
}

type DistillCheck struct {
	Required bool `json:"required"` // the guard predicate was true for this batch
	// Bypassed is true when --no-distill was passed or CONTEXO_DISTILL_DISABLE=1
	// was set (E24). The extension never passes the flag and strips the env var.
	Bypassed bool `json:"bypassed"`
}

// PullDoc is stdout of `ctx pull --json`. Skips are normal and exit 0.
type PullDoc struct {
	Schema    string        `json:"schema"`
	ServerURL string        `json:"server_url"`
	RepoID    string        `json:"repo_id"`
	Head      string        `json:"head,omitempty"`
	Written   []PulledPage  `json:"written"` // working file + base written
	Adopted   []PulledPage  `json:"adopted"` // content already identical: base/sha fast-forwarded, file untouched
	Skipped   []SkippedPage `json:"skipped"`
	Reindexed bool          `json:"reindexed"`
	Warnings  []Warning     `json:"warnings"`
}

type PulledPage struct {
	Path string    `json:"path"`
	SHA  string    `json:"sha"`
	Kind WriteKind `json:"kind,omitempty"` // written only
}

type ResolveStrategy string

const (
	ResolveOurs   ResolveStrategy = "ours"
	ResolveTheirs ResolveStrategy = "theirs"
)

// ResolveDoc is stdout of `ctx resolve <path> --ours|--theirs --json`.
type ResolveDoc struct {
	Schema        string          `json:"schema"`
	Path          string          `json:"path"`
	Strategy      ResolveStrategy `json:"strategy"`
	PreviousState PageState       `json:"previous_state"`
	State         PageState       `json:"state"`
	BaseSHA       string          `json:"base_sha"`
	FileWritten   bool            `json:"file_written"`
	BackupFile    string          `json:"backup_file,omitempty"` // relative to .contexo/, theirs only
	Warnings      []Warning       `json:"warnings"`
}
