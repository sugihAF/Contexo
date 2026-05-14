package tests

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/schema"
)

func TestStory01_SessionEventJSON(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	evt := schema.SessionEvent{
		Schema:  "ctx.session_event.v1",
		EventID: "evt-001",
		Ts:      now,
		Session: schema.SessionRef{
			ID:     "sess-001",
			Source: "claude_code",
		},
		Type: "user_message",
		Turn: 1,
		Actor: schema.ActorRef{
			Role: "user",
		},
		Content: schema.Content{
			Text: "Hello, world!",
		},
	}

	data, err := json.Marshal(evt)
	require.NoError(t, err)

	var m map[string]interface{}
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.Equal(t, "ctx.session_event.v1", m["schema"])
	assert.Equal(t, "evt-001", m["event_id"])
	assert.Equal(t, "sess-001", m["session"].(map[string]interface{})["id"])
	assert.Equal(t, "user_message", m["type"])

	var decoded schema.SessionEvent
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, evt.EventID, decoded.EventID)
	assert.Equal(t, evt.Session.ID, decoded.Session.ID)
	assert.Equal(t, evt.Type, decoded.Type)
	assert.Equal(t, evt.Content.Text, decoded.Content.Text)
}

func TestStory01_ContextCommitJSON(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	commit := schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "cmt-001",
		Title:     "Implement user auth",
		Summary:   []string{"Added JWT-based authentication", "Configured middleware"},
		Feature:   "auth",
		CreatedAt: now,
		Author:    schema.AuthorInfo{Name: "dev@example.com", Tool: "claude-code"},
		Decisions: []schema.Decision{
			{
				Description: "Use JWT over sessions",
				Rationale:   "Stateless, scalable",
			},
		},
		Evidence: []schema.Evidence{
			{
				SessionID: "sess-001",
				FromTurn:  1,
				ToTurn:    5,
				Source:    "claude_code",
			},
		},
		Changes: &schema.ChangeSet{
			Files: []schema.FileChange{
				{Path: "auth/handler.go", Action: "create"},
			},
			Symbols: []string{"auth::Handler"},
		},
		NextSteps:     []string{"Add tests", "Deploy"},
		Branch:        "feature/auth",
		BranchPurpose: "Implement authentication",
		RepoID:        "repo-001",
	}

	data, err := json.Marshal(commit)
	require.NoError(t, err)

	var m map[string]interface{}
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.Equal(t, "ctx.commit.v1", m["schema"])
	assert.Equal(t, "cmt-001", m["commit_id"])
	assert.Equal(t, "Implement user auth", m["title"])

	// Verify new fields serialize
	summaryArr, ok := m["summary"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, summaryArr, 2)

	authorMap, ok := m["author"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "dev@example.com", authorMap["name"])
	assert.Equal(t, "claude-code", authorMap["tool"])

	nextSteps, ok := m["next_steps"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, nextSteps, 2)

	assert.Equal(t, "feature/auth", m["branch"])
	assert.Equal(t, "repo-001", m["repo_id"])

	var decoded schema.ContextCommit
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, commit.CommitID, decoded.CommitID)
	assert.Equal(t, commit.Title, decoded.Title)
	assert.Len(t, decoded.Decisions, 1)
	assert.Len(t, decoded.Evidence, 1)
	assert.Equal(t, "claude_code", decoded.Evidence[0].Source)
	assert.Len(t, decoded.Summary, 2)
	assert.Equal(t, "dev@example.com", decoded.Author.Name)
	assert.Equal(t, "claude-code", decoded.Author.Tool)
	assert.Len(t, decoded.NextSteps, 2)
	assert.Equal(t, "feature/auth", decoded.Branch)
	assert.Equal(t, "Added JWT-based authentication; Configured middleware", decoded.SummaryText())
}

func TestStory01_FeatureOverviewJSON(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	fo := schema.FeatureOverview{
		Schema:  "ctx.feature_overview.v1",
		RepoID:  "repo-001",
		Feature: "auth",
		Summary: "Authentication feature",
		Status:  "in_progress",
		Branches: []schema.BranchSummary{
			{Name: "feature/auth", Status: "active"},
		},
		CommitIDs: []string{"cmt-001", "cmt-002"},
		UpdatedAt: now,
	}

	data, err := json.Marshal(fo)
	require.NoError(t, err)

	var m map[string]interface{}
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.Equal(t, "ctx.feature_overview.v1", m["schema"])
	assert.Equal(t, "repo-001", m["repo_id"])
	assert.Equal(t, "auth", m["feature"])

	var decoded schema.FeatureOverview
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, fo.Feature, decoded.Feature)
	assert.Len(t, decoded.Branches, 1)
}

func TestStory01_ValidateSessionEvent_Valid(t *testing.T) {
	evt := schema.SessionEvent{
		Schema:  "ctx.session_event.v1",
		EventID: "evt-001",
		Ts:      time.Now(),
		Session: schema.SessionRef{ID: "sess-001"},
		Type:    "user_message",
	}
	assert.NoError(t, evt.Validate())
}

func TestStory01_ValidateSessionEvent_MissingFields(t *testing.T) {
	tests := []struct {
		name  string
		event schema.SessionEvent
		field string
	}{
		{"missing schema", schema.SessionEvent{EventID: "e", Ts: time.Now(), Session: schema.SessionRef{ID: "s"}, Type: "t"}, "schema"},
		{"missing event_id", schema.SessionEvent{Schema: "s", Ts: time.Now(), Session: schema.SessionRef{ID: "s"}, Type: "t"}, "event_id"},
		{"missing ts", schema.SessionEvent{Schema: "s", EventID: "e", Session: schema.SessionRef{ID: "s"}, Type: "t"}, "ts"},
		{"missing session.id", schema.SessionEvent{Schema: "s", EventID: "e", Ts: time.Now(), Type: "t"}, "session.id"},
		{"missing type", schema.SessionEvent{Schema: "s", EventID: "e", Ts: time.Now(), Session: schema.SessionRef{ID: "s"}}, "type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.event.Validate()
			assert.Error(t, err)
			assert.ErrorIs(t, err, schema.ErrMissingField)
			assert.Contains(t, err.Error(), tt.field)
		})
	}
}

func TestStory01_ValidateContextCommit_Valid(t *testing.T) {
	c := schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "cmt-001",
		Title:     "Test commit",
		CreatedAt: time.Now(),
	}
	assert.NoError(t, c.Validate())
}

func TestStory01_ValidateContextCommit_MissingFields(t *testing.T) {
	tests := []struct {
		name   string
		commit schema.ContextCommit
		field  string
	}{
		{"missing schema", schema.ContextCommit{CommitID: "c", Title: "t", CreatedAt: time.Now()}, "schema"},
		{"missing commit_id", schema.ContextCommit{Schema: "s", Title: "t", CreatedAt: time.Now()}, "commit_id"},
		{"missing title", schema.ContextCommit{Schema: "s", CommitID: "c", CreatedAt: time.Now()}, "title"},
		{"missing created_at", schema.ContextCommit{Schema: "s", CommitID: "c", Title: "t"}, "created_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.commit.Validate()
			assert.Error(t, err)
			assert.ErrorIs(t, err, schema.ErrMissingField)
		})
	}
}

func TestStory01_ValidateFeatureOverview_Valid(t *testing.T) {
	fo := schema.FeatureOverview{
		Schema:    "ctx.feature_overview.v1",
		RepoID:    "repo-001",
		Feature:   "auth",
		UpdatedAt: time.Now(),
	}
	assert.NoError(t, fo.Validate())
}

func TestStory01_ValidateFeatureOverview_MissingFields(t *testing.T) {
	tests := []struct {
		name    string
		feature schema.FeatureOverview
	}{
		{"missing schema", schema.FeatureOverview{RepoID: "r", Feature: "f", UpdatedAt: time.Now()}},
		{"missing repo_id", schema.FeatureOverview{Schema: "s", Feature: "f", UpdatedAt: time.Now()}},
		{"missing feature", schema.FeatureOverview{Schema: "s", RepoID: "r", UpdatedAt: time.Now()}},
		{"missing updated_at", schema.FeatureOverview{Schema: "s", RepoID: "r", Feature: "f"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.feature.Validate()
			assert.Error(t, err)
			assert.ErrorIs(t, err, schema.ErrMissingField)
		})
	}
}
