package schema

// RepoPolicy defines per-repo settings for context capture.
// Schema: ctx.repo_policy.v1
type RepoPolicy struct {
	Schema         string   `json:"schema"`
	RepoID         string   `json:"repo_id"`
	RedactionLevel string   `json:"redaction_level,omitempty"`
	DenyPaths      []string `json:"deny_paths,omitempty"`
	AllowSources   []string `json:"allow_sources,omitempty"`
	MaxSessionSize int64    `json:"max_session_size,omitempty"`
	RetentionDays  int      `json:"retention_days,omitempty"`
}
