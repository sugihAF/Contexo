package schema

import "time"

// SessionEvent represents a single event in a captured AI session.
// Schema: ctx.session_event.v1
type SessionEvent struct {
	Schema  string     `json:"schema"`
	EventID string     `json:"event_id"`
	Ts      time.Time  `json:"ts"`
	Session SessionRef `json:"session"`
	Type    string     `json:"type"`
	Turn    int        `json:"turn,omitempty"`
	Actor   ActorRef   `json:"actor,omitempty"`
	Content Content    `json:"content,omitempty"`
}

// SessionRef identifies the session this event belongs to.
type SessionRef struct {
	ID     string   `json:"id"`
	Source string   `json:"source,omitempty"`
	Repo   *RepoRef `json:"repo,omitempty"`
}

// RepoRef identifies a repository.
type RepoRef struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Remote string `json:"remote,omitempty"`
}

// ActorRef identifies who created this event.
type ActorRef struct {
	Role  string `json:"role,omitempty"`
	Model string `json:"model,omitempty"`
	Tool  string `json:"tool,omitempty"`
}

// Content holds the event payload.
type Content struct {
	Text string       `json:"text,omitempty"`
	Refs []ContentRef `json:"refs,omitempty"`
}

// ContentRef is a reference to a file or resource mentioned in content.
type ContentRef struct {
	Path   string `json:"path,omitempty"`
	Type   string `json:"type,omitempty"`
	Lines  string `json:"lines,omitempty"`
	BlobID string `json:"blob_id,omitempty"`
}

// SessionMeta holds metadata about a session for index listing.
type SessionMeta struct {
	ID         string     `json:"id"`
	Source     string     `json:"source"`
	Repo       *RepoRef   `json:"repo,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	EventCount int        `json:"event_count"`
	Feature    string     `json:"feature,omitempty"`
}
