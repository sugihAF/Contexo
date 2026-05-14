package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// HandleResourceRead dispatches a ctx:// resource read to the appropriate handler.
func (s *Server) HandleResourceRead(ctx context.Context, uri string) ([]byte, string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, "", fmt.Errorf("mcp: parse uri: %w", err)
	}

	if parsed.Scheme != "ctx" {
		return nil, "", fmt.Errorf("mcp: unsupported scheme: %s", parsed.Scheme)
	}

	path := strings.TrimPrefix(parsed.Host+parsed.Path, "/")
	if path == "" {
		path = parsed.Host
	}
	query := parsed.Query()

	switch {
	case path == "commits" || strings.HasPrefix(path, "commits"):
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 2 && parts[1] != "" {
			// ctx://commits/{commitId}
			data, err := s.ReadCommitDetail(ctx, parts[1])
			return data, "application/json", err
		}
		// ctx://commits?feature=X
		feature := query.Get("feature")
		data, err := s.ReadCommitList(ctx, feature)
		return data, "application/json", err

	case strings.HasPrefix(path, "sessions/"):
		// ctx://sessions/{sessionId}?source=X&from=N&to=M
		sessionID := strings.TrimPrefix(path, "sessions/")
		source := query.Get("source")
		if source == "" {
			source = "unknown"
		}
		from, _ := strconv.Atoi(query.Get("from"))
		to, _ := strconv.Atoi(query.Get("to"))
		data, err := s.ReadSessionSlice(ctx, sessionID, source, from, to)
		return data, "application/jsonl", err

	case path == "features":
		// ctx://features — list all features
		data, err := s.ReadFeatureList(ctx, "")
		return data, "application/json", err

	case strings.HasPrefix(path, "features/"):
		featurePath := strings.TrimPrefix(path, "features/")
		parts := strings.SplitN(featurePath, "/", 2)
		feature := parts[0]

		if len(parts) == 2 && parts[1] == "activity" {
			limit, _ := strconv.Atoi(query.Get("limit"))
			if limit == 0 {
				limit = 10
			}
			data, err := s.ReadActivityLog(ctx, "", feature, limit)
			return data, "application/json", err
		}

		// ctx://features/{feature}
		data, err := s.ReadFeatureOverview(ctx, "", feature)
		return data, "application/json", err

	case path == "context":
		// ctx://context?level=feature&feature={feature}&limit={limit}
		level := query.Get("level")
		if level == "" {
			level = "feature"
		}
		feature := query.Get("feature")
		limit, _ := strconv.Atoi(query.Get("limit"))
		if limit == 0 {
			limit = 20
		}
		data, err := s.ReadContextLevel(ctx, "", level, feature, limit)
		return data, "application/json", err

	case strings.HasPrefix(path, "blame/"):
		// ctx://blame/{symbolKey}
		symbolKey := strings.TrimPrefix(path, "blame/")
		data, err := s.ReadSymbolBlame(ctx, symbolKey)
		return data, "application/json", err

	default:
		return nil, "", fmt.Errorf("mcp: unknown resource: %s", uri)
	}
}
