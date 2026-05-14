package schema

import (
	"strings"
	"time"
)

// AuthorInfo identifies the commit author and tool used.
type AuthorInfo struct {
	Name string `json:"name,omitempty"`
	Tool string `json:"tool,omitempty"`
}

// ContextCommit represents a structured summary of AI-assisted development context.
// Schema: ctx.commit.v1
type ContextCommit struct {
	Schema           string     `json:"schema"`
	CommitID         string     `json:"commit_id"`
	Title            string     `json:"title"`
	Summary          []string   `json:"summary,omitempty"`
	Feature          string     `json:"feature,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	Author           AuthorInfo `json:"author,omitempty"`
	Decisions        []Decision `json:"decisions,omitempty"`
	Evidence         []Evidence `json:"evidence,omitempty"`
	Changes          *ChangeSet `json:"changes,omitempty"`
	Tags             []string   `json:"tags,omitempty"`
	ParentID         string     `json:"parent_id,omitempty"`
	NextSteps        []string   `json:"next_steps,omitempty"`
	Branch           string     `json:"branch,omitempty"`
	BranchPurpose    string     `json:"branch_purpose,omitempty"`
	PreviousProgress string     `json:"previous_progress,omitempty"`
	RepoID           string     `json:"repo_id,omitempty"`
}

// SummaryText returns all summary bullets joined as a single string.
func (c *ContextCommit) SummaryText() string {
	return strings.Join(c.Summary, "; ")
}

// AuthorName returns the author's display name.
func (c *ContextCommit) AuthorName() string {
	return c.Author.Name
}

// Decision records a design or implementation decision.
type Decision struct {
	ID           string   `json:"id,omitempty"`
	Description  string   `json:"description"`
	Rationale    string   `json:"rationale,omitempty"`
	Alternatives []string `json:"alternatives,omitempty"`
	Status       string   `json:"status,omitempty"`
}

// Evidence links a commit to the source session data.
type Evidence struct {
	SessionID string `json:"session_id"`
	FromTurn  int    `json:"from_turn,omitempty"`
	ToTurn    int    `json:"to_turn,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Source    string `json:"source,omitempty"`
}

// ChangeSet describes what files were modified.
type ChangeSet struct {
	Files   []FileChange `json:"files,omitempty"`
	Symbols []string     `json:"symbols,omitempty"`
}

// FileChange records a modification to a single file.
type FileChange struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Diff   string `json:"diff,omitempty"`
}
