package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/indexer"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/sync"
)

// Tool is an MCP tool definition.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ListTools returns the agent-invokable tools.
func (s *HubServer) ListTools() []Tool {
	return []Tool{
		{
			Name: "ctx_push",
			Description: "Push local CtxHub pages to the team server. Use when the user says something like " +
				"'sync my stripe knowledge to contexthub' or 'share this with the team'. Filter by " +
				"feature (= tag), tag, or type to push a subset.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"feature": map[string]interface{}{"type": "string", "description": "Tag to filter by (e.g. 'stripe')"},
					"tag":     map[string]interface{}{"type": "string", "description": "Alias of feature"},
					"type":    map[string]interface{}{"type": "string", "enum": []string{"concept", "entity", "source", "analysis"}},
					"message": map[string]interface{}{"type": "string", "description": "Commit message"},
				},
			},
		},
		{
			Name: "ctx_pull",
			Description: "Pull new pages from the team CtxHub server into the local .ctxhub/. Call this at the start " +
				"of a session when picking up work on a topic, to see what the team already knows.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"full": map[string]interface{}{"type": "boolean", "description": "Fetch all pages, ignoring last_pull_sha"},
				},
			},
		},
		{
			Name:        "ctx_status",
			Description: "Show local .ctxhub status: server, repo, auth, local page count, last pull sha, never-pushed pages.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name: "ctx_write_page",
			Description: "Write a CtxHub knowledge page to .ctxhub/. Use this when distilling research, decisions, " +
				"or analysis the team would benefit from. Always include reasoning_summary and an Agent Reasoning " +
				"section in the body explaining what was considered and rejected.",
			InputSchema: map[string]interface{}{
				"type":     "object",
				"required": []string{"slug", "type", "body"},
				"properties": map[string]interface{}{
					"slug":              map[string]interface{}{"type": "string", "description": "kebab-case identifier, e.g. 'stripe-subscription'"},
					"type":              map[string]interface{}{"type": "string", "enum": []string{"concept", "entity", "source", "analysis"}},
					"body":              map[string]interface{}{"type": "string", "description": "Markdown body (no frontmatter)"},
					"tags":              map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"related":           map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"sources":           map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "raw session slugs cited"},
					"reasoning_summary": map[string]interface{}{"type": "string", "description": "One-line distillation for the index"},
					"author":            map[string]interface{}{"type": "string", "description": "Defaults to credentials user_name"},
					"agent":             map[string]interface{}{"type": "string", "description": "Defaults to 'claude'"},
				},
			},
		},
	}
}

// ToolResult is what tools/call returns.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent is one item in a ToolResult.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// HandleToolCall dispatches a tool invocation by name.
func (s *HubServer) HandleToolCall(ctx context.Context, name string, args map[string]interface{}) *ToolResult {
	switch name {
	case "ctx_push":
		return s.toolPush(args)
	case "ctx_pull":
		return s.toolPull(args)
	case "ctx_status":
		return s.toolStatus()
	case "ctx_write_page":
		return s.toolWritePage(args)
	default:
		return errorResult(fmt.Sprintf("unknown tool: %s", name))
	}
}

func (s *HubServer) rootDir() string {
	return filepath.Dir(s.store.Root)
}

func (s *HubServer) toolStatus() *ToolResult {
	root := s.rootDir()
	cfg, _ := config.LoadHub(root)
	creds, _ := config.LoadCredentialsHub(root)
	pages, _ := s.store.List(pagestore.Filter{})
	state, _ := sync.LoadState(s.store.Root)

	server := cfg.ServerURL
	if server == "" {
		server = "(none)"
	}
	repo := cfg.RepoID
	if repo == "" {
		repo = "(none)"
	}
	lastPull := state.LastPullSHA
	if lastPull == "" {
		lastPull = "(never)"
	} else if len(lastPull) > 8 {
		lastPull = lastPull[:8]
	}

	unpushed := 0
	for _, p := range pages {
		if _, known := state.PageSHAs[p.Frontmatter.RelPath()]; !known {
			unpushed++
		}
	}

	user := "(anonymous)"
	if creds != nil && creds.UserName != "" {
		user = fmt.Sprintf("%s <%s>", creds.UserName, creds.UserEmail)
	}

	return textResult(fmt.Sprintf(
		"Server: %s\nRepo: %s\nUser: %s\nAuthenticated: %t\nLocal pages: %d\nLast pull: %s\nNever-pushed pages: %d",
		server, repo, user, creds != nil && creds.APIKey != "", len(pages), lastPull, unpushed,
	))
}

func (s *HubServer) toolPush(args map[string]interface{}) *ToolResult {
	root := s.rootDir()
	cfg, _ := config.LoadHub(root)
	creds, _ := config.LoadCredentialsHub(root)
	if creds == nil || cfg.ServerURL == "" || cfg.RepoID == "" {
		return errorResult("ctx_push: server not configured (run 'ctx remote set <url>', 'ctx remote set-repo <id>', 'ctx auth login')")
	}

	feature, _ := args["feature"].(string)
	tag, _ := args["tag"].(string)
	typ, _ := args["type"].(string)
	message, _ := args["message"].(string)

	pages, _ := s.store.List(pagestore.Filter{})
	filtered := filterPages(pages, feature, tag, typ)
	if len(filtered) == 0 {
		return textResult("Nothing to push (no pages match filters)")
	}

	state, _ := sync.LoadState(s.store.Root)
	files := make([]sync.PushFile, 0, len(filtered))
	for _, p := range filtered {
		data, err := schema.SerializePage(p)
		if err != nil {
			return errorResult(fmt.Sprintf("serialize %s: %v", p.Frontmatter.Slug, err))
		}
		path := p.Frontmatter.RelPath()
		files = append(files, sync.PushFile{
			Path:      path,
			Content:   string(data),
			ParentSHA: state.PageSHAs[path],
		})
	}

	if message == "" {
		message = fmt.Sprintf("agent push (%d pages)", len(files))
	}

	client := sync.NewClient(cfg.ServerURL, creds.APIKey)
	resp, err := client.PushPages(cfg.RepoID, &sync.PushRequest{
		AuthorName:  creds.UserName,
		AuthorEmail: creds.UserEmail,
		Message:     message,
		Files:       files,
	})
	if err != nil {
		return errorResult(err.Error())
	}

	for _, f := range resp.Pushed {
		state.PageSHAs[f.Path] = f.SHA
	}
	_ = sync.SaveState(s.store.Root, state)

	if len(resp.Conflicts) > 0 {
		b, _ := json.Marshal(resp.Conflicts)
		return errorResult(fmt.Sprintf("%d conflict(s): %s. Pull, merge, re-push.", len(resp.Conflicts), string(b)))
	}
	head := resp.NewHead
	if len(head) > 8 {
		head = head[:8]
	}
	return textResult(fmt.Sprintf("Pushed %d page(s); HEAD=%s", len(resp.Pushed), head))
}

func (s *HubServer) toolPull(args map[string]interface{}) *ToolResult {
	root := s.rootDir()
	cfg, _ := config.LoadHub(root)
	creds, _ := config.LoadCredentialsHub(root)
	if creds == nil || cfg.ServerURL == "" || cfg.RepoID == "" {
		return errorResult("ctx_pull: server not configured")
	}

	full, _ := args["full"].(bool)
	state, _ := sync.LoadState(s.store.Root)
	since := state.LastPullSHA
	if full {
		since = ""
	}

	client := sync.NewClient(cfg.ServerURL, creds.APIKey)
	resp, err := client.PullPages(cfg.RepoID, since)
	if err != nil {
		return errorResult(err.Error())
	}
	if len(resp.Files) == 0 {
		return textResult("Already up to date")
	}

	written := 0
	for _, f := range resp.Files {
		abs := filepath.Join(s.store.Root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return errorResult(fmt.Sprintf("mkdir %s: %v", f.Path, err))
		}
		if err := os.WriteFile(abs, []byte(f.Content), 0o644); err != nil {
			return errorResult(fmt.Sprintf("write %s: %v", f.Path, err))
		}
		state.PageSHAs[f.Path] = f.SHA
		written++
	}
	state.LastPullSHA = resp.NewHead
	_ = sync.SaveState(s.store.Root, state)
	_ = indexer.Generate(s.store)

	head := resp.NewHead
	if len(head) > 8 {
		head = head[:8]
	}
	return textResult(fmt.Sprintf("Pulled %d page(s); HEAD=%s", written, head))
}

func (s *HubServer) toolWritePage(args map[string]interface{}) *ToolResult {
	slug, _ := args["slug"].(string)
	typStr, _ := args["type"].(string)
	body, _ := args["body"].(string)
	if slug == "" || typStr == "" || body == "" {
		return errorResult("ctx_write_page: slug, type, body are required")
	}

	creds, _ := config.LoadCredentialsHub(s.rootDir())

	author, _ := args["author"].(string)
	if author == "" && creds != nil {
		author = creds.UserName
	}
	if author == "" {
		author = "anonymous"
	}
	agent, _ := args["agent"].(string)
	if agent == "" {
		agent = "claude"
	}
	reasoning, _ := args["reasoning_summary"].(string)

	now := time.Now().UTC()
	page := &schema.Page{
		Frontmatter: schema.PageFrontmatter{
			Schema:           schema.PageSchemaV1,
			Slug:             slug,
			Type:             schema.PageType(typStr),
			Author:           author,
			Agent:            agent,
			Created:          now,
			Updated:          now,
			Tags:             stringArr(args["tags"]),
			Related:          stringArr(args["related"]),
			Sources:          stringArr(args["sources"]),
			ReasoningSummary: reasoning,
		},
		Body: body,
	}

	if err := s.store.Write(page); err != nil {
		return errorResult(err.Error())
	}
	idxErr := indexer.Generate(s.store)
	if idxErr != nil {
		return textResult(fmt.Sprintf("Wrote %s (warning: index regen failed: %v)", page.Frontmatter.RelPath(), idxErr))
	}
	return textResult(fmt.Sprintf("Wrote %s", page.Frontmatter.RelPath()))
}

func filterPages(pages []*schema.Page, feature, tag, typ string) []*schema.Page {
	wanted := strings.ToLower(strings.TrimSpace(feature))
	if wanted == "" {
		wanted = strings.ToLower(strings.TrimSpace(tag))
	}
	typLow := strings.ToLower(strings.TrimSpace(typ))

	var out []*schema.Page
	for _, p := range pages {
		if typLow != "" && strings.ToLower(string(p.Frontmatter.Type)) != typLow {
			continue
		}
		if wanted != "" {
			has := false
			for _, t := range p.Frontmatter.Tags {
				if strings.ToLower(t) == wanted {
					has = true
					break
				}
			}
			if !has {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

func stringArr(v interface{}) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if str, ok := x.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

func errorResult(msg string) *ToolResult {
	return &ToolResult{
		Content: []ToolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}

func textResult(msg string) *ToolResult {
	return &ToolResult{
		Content: []ToolContent{{Type: "text", Text: msg}},
	}
}
