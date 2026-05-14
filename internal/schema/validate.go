package schema

import (
	"errors"
	"fmt"
)

var (
	ErrMissingField = errors.New("missing required field")
)

func fieldErr(structName, field string) error {
	return fmt.Errorf("%w: %s.%s", ErrMissingField, structName, field)
}

// Validate checks required fields on SessionEvent.
func (e *SessionEvent) Validate() error {
	if e.Schema == "" {
		return fieldErr("SessionEvent", "schema")
	}
	if e.EventID == "" {
		return fieldErr("SessionEvent", "event_id")
	}
	if e.Ts.IsZero() {
		return fieldErr("SessionEvent", "ts")
	}
	if e.Session.ID == "" {
		return fieldErr("SessionEvent", "session.id")
	}
	if e.Type == "" {
		return fieldErr("SessionEvent", "type")
	}
	return nil
}

// Validate checks required fields on ContextCommit.
func (c *ContextCommit) Validate() error {
	if c.Schema == "" {
		return fieldErr("ContextCommit", "schema")
	}
	if c.CommitID == "" {
		return fieldErr("ContextCommit", "commit_id")
	}
	if c.Title == "" {
		return fieldErr("ContextCommit", "title")
	}
	if c.CreatedAt.IsZero() {
		return fieldErr("ContextCommit", "created_at")
	}
	return nil
}

// Validate checks required fields on FeatureOverview.
func (f *FeatureOverview) Validate() error {
	if f.Schema == "" {
		return fieldErr("FeatureOverview", "schema")
	}
	if f.RepoID == "" {
		return fieldErr("FeatureOverview", "repo_id")
	}
	if f.Feature == "" {
		return fieldErr("FeatureOverview", "feature")
	}
	if f.UpdatedAt.IsZero() {
		return fieldErr("FeatureOverview", "updated_at")
	}
	return nil
}

// Validate checks required fields on ActivityEntry.
func (a *ActivityEntry) Validate() error {
	if a.ID == "" {
		return fieldErr("ActivityEntry", "id")
	}
	if a.RepoID == "" {
		return fieldErr("ActivityEntry", "repo_id")
	}
	if a.Feature == "" {
		return fieldErr("ActivityEntry", "feature")
	}
	if a.Type == "" {
		return fieldErr("ActivityEntry", "type")
	}
	if a.Summary == "" {
		return fieldErr("ActivityEntry", "summary")
	}
	if a.Ts.IsZero() {
		return fieldErr("ActivityEntry", "ts")
	}
	return nil
}

// Validate checks required fields on RepoPolicy.
func (p *RepoPolicy) Validate() error {
	if p.Schema == "" {
		return fieldErr("RepoPolicy", "schema")
	}
	if p.RepoID == "" {
		return fieldErr("RepoPolicy", "repo_id")
	}
	return nil
}
