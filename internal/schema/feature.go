package schema

import "time"

// FeatureOverview provides a high-level view of a feature's context.
// Schema: ctx.feature_overview.v1
type FeatureOverview struct {
	Schema    string          `json:"schema"`
	RepoID    string          `json:"repo_id"`
	Feature   string          `json:"feature"`
	Summary   string          `json:"summary,omitempty"`
	Status    string          `json:"status,omitempty"`
	Branches  []BranchSummary `json:"branches,omitempty"`
	CommitIDs []string        `json:"commit_ids,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// BranchSummary describes a branch related to a feature.
type BranchSummary struct {
	Name       string    `json:"name"`
	Status     string    `json:"status,omitempty"`
	LastCommit string    `json:"last_commit,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

// ActivityEntry records an activity event for a feature.
type ActivityEntry struct {
	ID        string    `json:"id"`
	RepoID    string    `json:"repo_id"`
	Feature   string    `json:"feature"`
	Type      string    `json:"type"`
	Summary   string    `json:"summary"`
	CommitID  string    `json:"commit_id,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Actor     string    `json:"actor,omitempty"`
	Ts        time.Time `json:"ts"`
}
