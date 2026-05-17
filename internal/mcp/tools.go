package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sugihAF/contexo/internal/capture"
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
func (s *Server) ListTools() []Tool {
	return []Tool{
		{
			Name: "ctx_push",
			Description: "Push local Contexo pages to the team server. Use when the user says something like " +
				"'sync my stripe knowledge to contexthub' or 'share this with the team'. Filter by " +
				"feature (= tag), tag, or type to push a subset. If a capture buffer is present and the " +
				"push includes concept/analysis pages, the tool will pause and ask you to write a source " +
				"page first (a structured reasoning-trail page); then re-invoke with distill_done=true " +
				"and source_slug set.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"feature":      map[string]interface{}{"type": "string", "description": "Tag to filter by (e.g. 'stripe')"},
					"tag":          map[string]interface{}{"type": "string", "description": "Alias of feature"},
					"type":         map[string]interface{}{"type": "string", "enum": []string{"concept", "entity", "source", "analysis"}},
					"message":      map[string]interface{}{"type": "string", "description": "Commit message"},
					"no_distill":   map[string]interface{}{"type": "boolean", "description": "Skip the reasoning-trail handshake for this push"},
					"distill_done": map[string]interface{}{"type": "boolean", "description": "Set true after authoring the source page that the previous PUSH_PAUSED response requested"},
					"source_slug":  map[string]interface{}{"type": "string", "description": "Slug of the source page just written (required when distill_done=true)"},
				},
			},
		},
		{
			Name: "ctx_pull",
			Description: "Pull new pages from the team Contexo server into the local .contexo/. Call this at the start " +
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
			Description: "Show local .contexo status: server, repo, auth, local page count, last pull sha, never-pushed pages.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name: "ctx_write_page",
			Description: "Write a Contexo knowledge page to .contexo/. Use this when distilling research, decisions, " +
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
		{
			Name: "ctx_capture_session",
			Description: "Produce a template + the local capture buffer so you can author a structured source " +
				"page (raw/sessions/<date>-<slug>.md) capturing the reasoning trail of the current session. " +
				"Useful for mid-session checkpoints when the user says 'capture what we've decided so far'. " +
				"Does NOT push. After calling this, write the page with ctx_write_page(type=source, ...).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"session_id": map[string]interface{}{"type": "string", "description": "Optional Claude Code session id; defaults to the most recent buffer (within 6h)"},
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
func (s *Server) HandleToolCall(ctx context.Context, name string, args map[string]interface{}) *ToolResult {
	switch name {
	case "ctx_push":
		return s.toolPush(args)
	case "ctx_pull":
		return s.toolPull(args)
	case "ctx_status":
		return s.toolStatus()
	case "ctx_write_page":
		return s.toolWritePage(args)
	case "ctx_capture_session":
		return s.toolCaptureSession(args)
	default:
		return errorResult(fmt.Sprintf("unknown tool: %s", name))
	}
}

func (s *Server) rootDir() string {
	return filepath.Dir(s.store.Root)
}

func (s *Server) toolStatus() *ToolResult {
	root := s.rootDir()
	cfg, _ := config.Load(root)
	creds, _ := config.LoadCredentials(root)
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

	pendingBuffers, _ := capture.List(s.store.Root)

	return textResult(fmt.Sprintf(
		"Server: %s\nRepo: %s\nUser: %s\nAuthenticated: %t\nLocal pages: %d\nLast pull: %s\nNever-pushed pages: %d\nPending capture buffers: %d",
		server, repo, user, creds != nil && creds.Bearer() != "", len(pages), lastPull, unpushed, len(pendingBuffers),
	))
}

func (s *Server) toolPush(args map[string]interface{}) *ToolResult {
	root := s.rootDir()
	cfg, _ := config.Load(root)
	creds, _ := config.LoadCredentials(root)
	if creds == nil || cfg.ServerURL == "" || cfg.RepoID == "" {
		return errorResult("ctx_push: server not configured (run 'ctx remote set <url>', 'ctx remote set-repo <id>', 'ctx auth login')")
	}

	feature, _ := args["feature"].(string)
	tag, _ := args["tag"].(string)
	typ, _ := args["type"].(string)
	message, _ := args["message"].(string)
	noDistill, _ := args["no_distill"].(bool)
	distillDone, _ := args["distill_done"].(bool)
	sourceSlug, _ := args["source_slug"].(string)

	pages, _ := s.store.List(pagestore.Filter{})
	filtered := filterPages(pages, feature, tag, typ)
	if len(filtered) == 0 && !distillDone {
		return textResult("Nothing to push (no pages match filters)")
	}

	// Distill handshake (Phase 1).
	if !distillDone && !noDistill && os.Getenv("CONTEXO_DISTILL_DISABLE") != "1" {
		if directive, ok := s.buildDistillDirective(filtered); ok {
			return textResult(directive)
		}
	}

	// distill_done=true: locate the source page the agent just wrote, patch
	// concept/analysis pages' sources: frontmatter to point to it, archive
	// the buffer, and include the source page in the push batch.
	if distillDone {
		if strings.TrimSpace(sourceSlug) == "" {
			return errorResult("ctx_push: distill_done=true requires source_slug (the slug just authored with ctx_write_page type=source)")
		}
		srcFm := schema.PageFrontmatter{Type: schema.TypeSource, Slug: sourceSlug}
		srcPage, err := s.store.Read(srcFm.RelPath())
		if err != nil {
			return errorResult(fmt.Sprintf("ctx_push: distill_done set but source page %q not found at raw/sessions/%s.md — write it first with ctx_write_page(type=\"source\", slug=%q, ...)", sourceSlug, sourceSlug, sourceSlug))
		}
		filtered = s.linkSourceIntoFiltered(filtered, sourceSlug)
		if !containsSlug(filtered, sourceSlug, schema.TypeSource) {
			filtered = append(filtered, srcPage)
		}
		if buf, _ := capture.MostRecent(s.store.Root, 6*time.Hour); buf != nil {
			_ = buf.Archive()
		}
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

	client := sync.NewClient(cfg.ServerURL, creds.Bearer())
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

// buildDistillDirective returns the PUSH_PAUSED text the agent should act
// on, when (a) the push batch includes a concept/analysis page and (b) a
// pending buffer with at least one turn exists within the last 6h. Returns
// (text, true) when the handshake should fire; ("", false) otherwise.
func (s *Server) buildDistillDirective(batch []*schema.Page) (string, bool) {
	if !hasKnowledgePage(batch) {
		return "", false
	}
	buf, err := capture.MostRecent(s.store.Root, 6*time.Hour)
	if err != nil || buf == nil {
		return "", false
	}
	recs, err := buf.Records()
	if err != nil || len(recs) == 0 {
		return "", false
	}

	suggestedSlug := suggestedSourceSlug(batch)
	pageLines := make([]string, 0, len(batch))
	tagUnion := make(map[string]bool)
	for _, p := range batch {
		pageLines = append(pageLines, fmt.Sprintf("- %s (%s)", p.Frontmatter.RelPath(), p.Frontmatter.Type))
		for _, t := range p.Frontmatter.Tags {
			tagUnion[t] = true
		}
	}
	tags := make([]string, 0, len(tagUnion))
	for t := range tagUnion {
		tags = append(tags, t)
	}

	bufferText := inlineBuffer(recs)

	return strings.Join([]string{
		"<PUSH_PAUSED reason=distill_required>",
		"",
		"Before pushing, write a 'source' page that captures the reasoning trail behind",
		"the knowledge pages in this batch. Then re-invoke ctx_push with the same args,",
		"plus distill_done=true and source_slug=<the slug you used>.",
		"",
		"STEP 1 — call ctx_write_page with:",
		fmt.Sprintf("  type:              \"source\""),
		fmt.Sprintf("  slug:              %q   (suggested; adjust topic to taste)", suggestedSlug),
		fmt.Sprintf("  tags:              %v   (union of the batch's tags)", tags),
		"  reasoning_summary: one-line distillation (<= 100 chars)",
		"  body:              follow the TEMPLATE below",
		"",
		"TEMPLATE (drop sections that genuinely don't apply, keep them in this order):",
		"",
		"  ## Decision",
		"  What we ended up doing (1-3 sentences).",
		"",
		"  ## Why this approach",
		"  Bullets covering the load-bearing reasons.",
		"",
		"  ## Rejected alternatives",
		"  - <alternative> -> rejected because <reason>",
		"",
		"  ## Path of inquiry",
		"  1. ...",
		"  2. ...",
		"",
		"  ## Dead-ends",
		"  - <thing we tried that didn't work> (so the next agent doesn't repeat it)",
		"",
		"  ## Open questions",
		"  - Still TBD: ...",
		"",
		"  ## Sources",
		"  - <doc URLs, related concept pages, etc.>",
		"",
		"IMPORTANT: redact any API keys, tokens, passwords, or PII you encounter.",
		"",
		"STEP 2 — call ctx_push again with the same filter args plus:",
		"  distill_done: true",
		fmt.Sprintf("  source_slug:  %q", suggestedSlug),
		"",
		"That second call will link the source into each concept/analysis page's",
		"`sources:` frontmatter, archive the buffer, and push everything in one commit.",
		"",
		"---",
		"",
		"PAGES being pushed in this batch:",
		strings.Join(pageLines, "\n"),
		"",
		"BUFFER (turn-by-turn summaries from this session, oldest first):",
		bufferText,
		"",
		"</PUSH_PAUSED>",
	}, "\n"), true
}

func (s *Server) toolCaptureSession(args map[string]interface{}) *ToolResult {
	sessionID, _ := args["session_id"].(string)
	var buf *capture.Buffer
	if sessionID != "" {
		buf = capture.Open(s.store.Root, sessionID)
		if !buf.Exists() {
			return errorResult(fmt.Sprintf("ctx_capture_session: no buffer for session %q", sessionID))
		}
	} else {
		b, err := capture.MostRecent(s.store.Root, 24*time.Hour)
		if err != nil || b == nil {
			return errorResult("ctx_capture_session: no recent capture buffer (run 'ctx hooks install' once per project, then have a Claude Code session here)")
		}
		buf = b
	}
	recs, err := buf.Records()
	if err != nil || len(recs) == 0 {
		return errorResult("ctx_capture_session: buffer is empty")
	}
	suggested := time.Now().UTC().Format("2006-01-02") + "-session-" + buf.SessionID
	if len(suggested) > 80 {
		suggested = suggested[:80]
	}

	return textResult(strings.Join([]string{
		"<CAPTURE_TEMPLATE>",
		"",
		"Author a 'source' page capturing the reasoning trail of this session. Call",
		"ctx_write_page with:",
		"",
		fmt.Sprintf("  type:              \"source\""),
		fmt.Sprintf("  slug:              %q   (rename to reflect the topic)", suggested),
		"  reasoning_summary: one-line distillation (<= 100 chars)",
		"  body:              follow the TEMPLATE below",
		"",
		"TEMPLATE:",
		"  ## Decision",
		"  ## Why this approach",
		"  ## Rejected alternatives",
		"  ## Path of inquiry",
		"  ## Dead-ends",
		"  ## Open questions",
		"  ## Sources",
		"",
		"IMPORTANT: redact any API keys, tokens, passwords, or PII you encounter.",
		"",
		"BUFFER (turn-by-turn summaries from this session, oldest first):",
		inlineBuffer(recs),
		"",
		"</CAPTURE_TEMPLATE>",
	}, "\n"))
}

func (s *Server) toolPull(args map[string]interface{}) *ToolResult {
	root := s.rootDir()
	cfg, _ := config.Load(root)
	creds, _ := config.LoadCredentials(root)
	if creds == nil || cfg.ServerURL == "" || cfg.RepoID == "" {
		return errorResult("ctx_pull: server not configured")
	}

	full, _ := args["full"].(bool)
	state, _ := sync.LoadState(s.store.Root)
	since := state.LastPullSHA
	if full {
		since = ""
	}

	client := sync.NewClient(cfg.ServerURL, creds.Bearer())
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

func (s *Server) toolWritePage(args map[string]interface{}) *ToolResult {
	slug, _ := args["slug"].(string)
	typStr, _ := args["type"].(string)
	body, _ := args["body"].(string)
	if slug == "" || typStr == "" || body == "" {
		return errorResult("ctx_write_page: slug, type, body are required")
	}

	creds, _ := config.LoadCredentials(s.rootDir())

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

// linkSourceIntoFiltered appends sourceSlug to the Sources frontmatter
// list of every concept/analysis page in batch (in-memory + on-disk), so
// the upstream push commits the link in the same atomic commit. Returns
// the same slice (mutated in place).
func (s *Server) linkSourceIntoFiltered(batch []*schema.Page, sourceSlug string) []*schema.Page {
	for _, p := range batch {
		if p.Frontmatter.Type != schema.TypeConcept && p.Frontmatter.Type != schema.TypeAnalysis {
			continue
		}
		if containsString(p.Frontmatter.Sources, sourceSlug) {
			continue
		}
		p.Frontmatter.Sources = append(p.Frontmatter.Sources, sourceSlug)
		p.Frontmatter.Updated = time.Now().UTC()
		_ = s.store.Write(p) // best-effort; push will still include the in-memory version
	}
	return batch
}

func hasKnowledgePage(batch []*schema.Page) bool {
	for _, p := range batch {
		if p.Frontmatter.Type == schema.TypeConcept || p.Frontmatter.Type == schema.TypeAnalysis {
			return true
		}
	}
	return false
}

func containsSlug(batch []*schema.Page, slug string, typ schema.PageType) bool {
	for _, p := range batch {
		if p.Frontmatter.Slug == slug && p.Frontmatter.Type == typ {
			return true
		}
	}
	return false
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func suggestedSourceSlug(batch []*schema.Page) string {
	date := time.Now().UTC().Format("2006-01-02")
	for _, p := range batch {
		if p.Frontmatter.Type == schema.TypeConcept || p.Frontmatter.Type == schema.TypeAnalysis {
			return fmt.Sprintf("%s-%s-reasoning", date, p.Frontmatter.Slug)
		}
	}
	return date + "-session"
}

// inlineBuffer renders the buffer's records as a single text block the
// agent can read. We cap total size at ~30 KB by keeping the first 10
// turns + the last 40 turns when the buffer grows past 50 turns.
func inlineBuffer(recs []capture.TurnRecord) string {
	const headKeep, tailKeep = 10, 40
	var rendered []capture.TurnRecord
	if len(recs) <= headKeep+tailKeep {
		rendered = recs
	} else {
		rendered = append(rendered, recs[:headKeep]...)
		rendered = append(rendered, capture.TurnRecord{
			Truncated: &capture.TruncationTag{
				Dropped: len(recs) - headKeep - tailKeep,
				Reason:  "inline_window",
			},
		})
		rendered = append(rendered, recs[len(recs)-tailKeep:]...)
	}
	var sb strings.Builder
	for _, r := range rendered {
		line, _ := json.Marshal(r)
		sb.Write(line)
		sb.WriteByte('\n')
	}
	return sb.String()
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
