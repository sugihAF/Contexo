package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store"
	"github.com/sugihAF/contexo/internal/store/jsonl"
)

// ResourcePriority defines MCP resource annotation priorities.
const (
	PriorityCommitList   = 0.6
	PriorityCommitDetail = 0.8
	PrioritySessionSlice = 0.4
	PriorityFeature      = 0.6
	PriorityActivity     = 0.4
	PriorityFeatureList  = 0.5
	PriorityContext      = 0.7
	PriorityBlame        = 0.6
)

// Resource represents an MCP resource with metadata.
type Resource struct {
	URI         string                 `json:"uri"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	MimeType    string                 `json:"mimeType,omitempty"`
	Annotations map[string]interface{} `json:"annotations,omitempty"`
}

// ResourceTemplate represents an MCP resource template.
type ResourceTemplate struct {
	URITemplate string                 `json:"uriTemplate"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	MimeType    string                 `json:"mimeType,omitempty"`
	Annotations map[string]interface{} `json:"annotations,omitempty"`
}

// Server provides MCP resource handling backed by local stores.
type Server struct {
	commitStore  store.CommitStore
	featureStore store.FeatureStore
	sessionsDir  string
}

// NewServer creates an MCP Server.
func NewServer(cs store.CommitStore, fs store.FeatureStore, sessionsDir string) *Server {
	return &Server{
		commitStore:  cs,
		featureStore: fs,
		sessionsDir:  sessionsDir,
	}
}

// ListResources returns all registered resource templates.
func (s *Server) ListResources() []ResourceTemplate {
	return []ResourceTemplate{
		{
			URITemplate: "ctx://commits?feature={feature}",
			Name:        "Context Commit List",
			Description: "List context commits, optionally filtered by feature",
			MimeType:    "application/json",
			Annotations: map[string]interface{}{"priority": PriorityCommitList},
		},
		{
			URITemplate: "ctx://commits/{commitId}",
			Name:        "Context Commit Detail",
			Description: "Full detail of a specific context commit",
			MimeType:    "application/json",
			Annotations: map[string]interface{}{"priority": PriorityCommitDetail},
		},
		{
			URITemplate: "ctx://sessions/{sessionId}?from={fromTurn}&to={toTurn}",
			Name:        "Session Slice",
			Description: "Turn-filtered session event log",
			MimeType:    "application/jsonl",
			Annotations: map[string]interface{}{"priority": PrioritySessionSlice},
		},
		{
			URITemplate: "ctx://features/{feature}",
			Name:        "Feature Overview",
			Description: "Feature overview with summary and status",
			MimeType:    "application/json",
			Annotations: map[string]interface{}{"priority": PriorityFeature},
		},
		{
			URITemplate: "ctx://features/{feature}/activity?limit={limit}",
			Name:        "Activity Log",
			Description: "Recent activity entries for a feature",
			MimeType:    "application/json",
			Annotations: map[string]interface{}{"priority": PriorityActivity},
		},
		{
			URITemplate: "ctx://features",
			Name:        "Feature List",
			Description: "List all features in the repository",
			MimeType:    "application/json",
			Annotations: map[string]interface{}{"priority": PriorityFeatureList},
		},
		{
			URITemplate: "ctx://context?level={level}&feature={feature}&limit={limit}",
			Name:        "Context Level",
			Description: "Multi-resolution context: feature overview, activity log, or metadata",
			MimeType:    "application/json",
			Annotations: map[string]interface{}{"priority": PriorityContext},
		},
		{
			URITemplate: "ctx://blame/{symbolKey}",
			Name:        "Symbol Blame",
			Description: "Context history for a symbol",
			MimeType:    "application/json",
			Annotations: map[string]interface{}{"priority": PriorityBlame},
		},
	}
}

// ReadCommitList returns commits as JSON.
func (s *Server) ReadCommitList(ctx context.Context, feature string) ([]byte, error) {
	commits, err := s.commitStore.ListCommits(ctx, store.CommitFilter{Feature: feature})
	if err != nil {
		return nil, fmt.Errorf("mcp: list commits: %w", err)
	}
	return json.Marshal(commits)
}

// ReadCommitDetail returns a single commit as JSON.
func (s *Server) ReadCommitDetail(ctx context.Context, commitID string) ([]byte, error) {
	commit, err := s.commitStore.GetCommit(ctx, commitID)
	if err != nil {
		return nil, fmt.Errorf("mcp: get commit: %w", err)
	}
	if commit == nil {
		return nil, fmt.Errorf("mcp: commit not found: %s", commitID)
	}
	return json.Marshal(commit)
}

// ReadSessionSlice returns turn-filtered events as JSON array.
func (s *Server) ReadSessionSlice(ctx context.Context, sessionID, source string, fromTurn, toTurn int) ([]byte, error) {
	path := fmt.Sprintf("%s/%s/%s.jsonl", s.sessionsDir, source, sessionID)
	reader := jsonl.NewReader(path)
	events, err := reader.ReadRange(fromTurn, toTurn)
	if err != nil {
		return nil, fmt.Errorf("mcp: read session: %w", err)
	}
	return json.Marshal(events)
}

// ReadFeatureOverview returns the feature overview as JSON.
func (s *Server) ReadFeatureOverview(ctx context.Context, repoID, feature string) ([]byte, error) {
	overview, err := s.featureStore.GetOverview(ctx, repoID, feature)
	if err != nil {
		return nil, fmt.Errorf("mcp: get overview: %w", err)
	}
	if overview == nil {
		// Return minimal response
		return json.Marshal(&schema.FeatureOverview{
			Schema:  "ctx.feature_overview.v1",
			Feature: feature,
		})
	}
	return json.Marshal(overview)
}

// ReadActivityLog returns activity entries as JSON.
func (s *Server) ReadActivityLog(ctx context.Context, repoID, feature string, limit int) ([]byte, error) {
	entries, err := s.featureStore.ListActivity(ctx, repoID, feature, limit)
	if err != nil {
		return nil, fmt.Errorf("mcp: list activity: %w", err)
	}
	return json.Marshal(entries)
}

// ReadFeatureList returns all features as JSON.
func (s *Server) ReadFeatureList(ctx context.Context, repoID string) ([]byte, error) {
	// Use ListActivity with empty feature to get unique features, or
	// iterate commits. For now we list known feature overviews.
	// The featureStore has PutOverview, but no ListFeatures.
	// We'll return features that have overviews.
	// For a complete list, we'd need to scan commits.
	// Return features from commit list for completeness.
	commits, err := s.commitStore.ListCommits(ctx, store.CommitFilter{})
	if err != nil {
		return nil, fmt.Errorf("mcp: list features: %w", err)
	}

	seen := make(map[string]bool)
	var features []map[string]interface{}
	for _, c := range commits {
		if c.Feature != "" && !seen[c.Feature] {
			seen[c.Feature] = true
			overview, _ := s.featureStore.GetOverview(ctx, repoID, c.Feature)
			entry := map[string]interface{}{
				"feature": c.Feature,
			}
			if overview != nil {
				entry["summary"] = overview.Summary
				entry["status"] = overview.Status
			}
			features = append(features, entry)
		}
	}
	return json.Marshal(features)
}

// ReadContextLevel returns multi-resolution context as JSON.
func (s *Server) ReadContextLevel(ctx context.Context, repoID, level, feature string, limit int) ([]byte, error) {
	switch level {
	case "feature":
		if feature == "" {
			return s.ReadFeatureList(ctx, repoID)
		}
		return s.ReadFeatureOverview(ctx, repoID, feature)

	case "log":
		commits, err := s.commitStore.ListCommits(ctx, store.CommitFilter{Feature: feature, Limit: limit})
		if err != nil {
			return nil, fmt.Errorf("mcp: context log: %w", err)
		}
		return json.Marshal(commits)

	case "metadata":
		meta := map[string]interface{}{
			"repo_id": repoID,
			"level":   "metadata",
		}
		return json.Marshal(meta)

	default:
		return nil, fmt.Errorf("mcp: unknown context level: %s", level)
	}
}

// ReadSymbolBlame returns commits that touch a symbol as JSON.
func (s *Server) ReadSymbolBlame(ctx context.Context, symbolKey string) ([]byte, error) {
	commits, err := s.commitStore.GetBySymbol(ctx, symbolKey)
	if err != nil {
		return nil, fmt.Errorf("mcp: blame: %w", err)
	}
	return json.Marshal(map[string]interface{}{
		"symbol_key": symbolKey,
		"commits":    commits,
	})
}
