# CtxHub Plan (plan.md)

> **Working name:** CtxHub (server) + `ctx` (CLI) + `ctxd` (local recorder)  
> **One‑liner:** “GitHub for AI-assisted development context”: capture every AI conversation, then create small “context commits” that agents can browse first and drill into only when needed.

---

## 1. Executive summary

Modern teams collaborate via Git for code, but AI-assisted development introduces a second collaboration layer: **agent context** (prompts, responses, decisions, next steps). Today that context is fragmented across tools (Claude Code, Codex CLI, Cursor, VS Code chat, etc.), making handoffs slow and inconsistent.

CtxHub provides:

- **Always-on capture** of every AI conversation (prompts + responses; optionally tool traces) into immutable **Session Logs**.
- A Git-like workflow to create **Context Commits**: small, structured summaries tied to repo/branch/features and linked to Git commits/PRs.
- **Selective retrieval**: agents see commit titles + summaries first (like `git log`). They only open specific commits/sessions when relevant.
- Cross-tool interoperability via **MCP (Model Context Protocol) Resources** + optional tool-specific plugins/adapters.

---

## 2. Problem statement

### Symptoms
- Dev A works with one assistant. Dev B uses another. Context is not portable.
- Handoffs require repeating explanations and re-learning codebase intent.
- Agent-generated changes lack “why”, so future work becomes brittle.

### Root cause
Context is trapped inside each assistant’s session/history. Git stores *what* changed, but not *what the agent and developer discussed* nor *why* decisions were made.

### Desired outcome
A developer (or their agent) can continue work **within minutes**, by:
1) reading a short context commit history, and
2) drilling into the relevant recorded conversation slice only if needed.

---

## 3. Design principles

1. **Capture everything, share selectively**  
   Capture every prompt/response so nothing is lost. But do not dump everything into the model—use summaries and on-demand drilldown.

2. **Tool-agnostic first, tool-optimized second**  
   Standardize storage formats and APIs so any client can consume them; use plugins where possible for higher fidelity.

3. **Immutable logs, editable summaries**  
   Session logs are append-only (audit trail). Context commits can be amended (like `git commit --amend`) but preserve history.

4. **Human + agent readable**  
   Store canonical data as JSON/JSONL. Provide Markdown render for humans.

5. **Secure by default**  
   Logging AI chats is sensitive. Redaction, encryption, permissions, retention, and “pause capture” must exist from day 1.

---

## 4. Key concepts

### 4.1 Session
A continuous conversation “run” in a tool (Claude Code session, Codex task, Cursor chat tab, VS Code chat session export, etc.).

**Stored as:** JSONL event stream (append-only).  
**Purpose:** forensic “source of truth” for what was said/done.

### 4.2 Context Commit
A small structured summary representing a meaningful unit of work (like a Git commit message + diff summary, but for context).

**Stored as:** JSON file + optional Markdown.  
**Purpose:** default “log” view for humans/agents. Enables selective loading.

### 4.3 Feature stream
A lightweight label for grouping commits/sessions by topic (e.g., `onboarding`, `payments`, `infra/auth`).

### 4.4 Evidence slice
A pointer into a session log (session id + turn range / filters) backing a context commit.

### 4.5 Feature overview / roadmap (GCC-inspired)
A small “living” overview per **feature stream** (and optionally per git branch/PR) that captures:
- purpose (“why does this feature exist?”)
- objectives and milestones
- current status (active, blocked, done)
- key decisions (with short “why”)
- pointers to the most important context commits

**Stored as:** JSON + optional Markdown render.  
**Updated on:** `ctx commit`, `ctx merge` (synthesis), PR merge automation.

### 4.6 Recent activity log (OTA-inspired, derived)
A condensed, rolling “recent activity” view (default cap: last 50 entries) derived from session events.
Each entry is short and structured (Observation → Decision → Action → Result), and links back to the underlying session + turn range.

**Purpose:** fast “where were we?” and “what just happened?” without opening full transcripts.


---

## 5. Product surfaces

### 5.1 `ctx` CLI (Git-like)
- `ctx init`
- `ctx capture on/off/status`
- `ctx session start/stop/ls/show/tail`
- `ctx commit -m "..."`
- `ctx log`, `ctx show`
- `ctx open-session`
- `ctx context [--feature <name> | --log [n] | --metadata | --full]` (multi-resolution context view)
- `ctx feature summary [<feature>]` (view/generate feature overview/roadmap)
- `ctx config set proactive_commits <true|false>` (auto-suggest ctx commits after milestones)
- `ctx merge [<branch>]` (generate a synthesis ctx commit when a branch/PR is merged; optional MVP-2+)
- `ctx export gcc` / `ctx import gcc` (optional compatibility with `.GCC/` format)
- `ctx link <gitsha>`
- `ctx blame <symbol>`
- `ctx push`, `ctx pull`
- `ctx mcp` (run local stdio MCP server)
### 5.2 CtxHub server (GitHub-like)
- Multi-tenant: orgs → repos → branches/features
- Stores commits, sessions, indexes
- Auth + permissions
- REST API for CLI/Web
- MCP server façade for AI clients (resources + templates)

### 5.3 Integrations / adapters
- **Claude Code plugin**: best-in-class capture via hooks/events.
- **Codex wrapper**: pipe `--json` output into recorder.
- **Cursor importer**: watch local `state.vscdb` (best-effort).
- **VS Code exporter/importer**: import exported chat JSON.

### 5.4 Web dashboard (optional MVP, recommended)
- Browse context history, filter by feature/file/symbol
- Review sessions and slices
- Admin settings (retention, redaction rules, permissions)

---

## 6. Architecture overview

### 6.1 Local-first capture
**`ctxd` local recorder** runs per developer machine:
- Receives events from adapters/plugins/wrappers
- Writes session logs (`.ctx/sessions/.../*.jsonl`)
- Maintains a local SQLite index for fast `ctx log/blame/search`
- Syncs to server via `ctx push`

### 6.2 Server storage
- PostgreSQL for metadata (repos, commits, symbol index, permissions)
- Object storage (S3/R2/MinIO) for session logs and large blobs
- Optional full-text search (Postgres FTS initially, OpenSearch later)

### 6.3 MCP read path
AI tools connect through MCP to browse:
- commit summaries
- commit details
- session slices
- symbol blame
without ingesting everything.

---

## 7. MVP scope

### MVP-1 (core)
- CLI + local recorder (`ctxd`) + server
- Capture prompts/responses for:
  - Claude Code (hooks)
  - Codex CLI (json mode wrapper)
  - Manual import for VS Code chat exports
- Context commits: create, log, show, link to git commit
- MCP resources: commit list, commit show, session slice read

### MVP-2 (team usability)
- Cursor importer (best-effort)
- `ctx blame <symbol>`
- Web dashboard minimal: list commits + view sessions
- Redaction rules + pause capture

### MVP-3 (scale & polish)
- SSO / OAuth + org management
- Search + relevance ranking
- PR checks: require ctx commit for significant code changes (optional)
- Observability and audit logs

---

## 8. Key risks & mitigations

### Risk: privacy/secrets leakage
Mitigation:
- local redaction before writing logs
- server-side secondary scanning
- allowlist/denylist paths
- per-session pause/resume
- encryption at rest + optional client-side encryption

### Risk: tool integration brittleness (esp. Cursor)
Mitigation:
- treat importers as “best-effort”
- canonical schema isolates tool-specific raw payload in `raw` field
- keep main UX functional with Claude + Codex

### Risk: context overload
Mitigation:
- commit-first browsing
- session slices by range/filter
- MCP resource annotations (priority) and templates

---

## 9. Success metrics

- **Handoff time**: “new dev + new agent can continue” in < 10 minutes (target).
- **Context capture coverage**: % of AI sessions captured per repo.
- **Context commit coverage**: % of PRs/feature branches with at least one ctx commit.
- **Retrieval efficiency**: average tokens loaded per continuation task decreases over time.

---

## 10. Open questions (to validate early)

1. Do you require server access to raw conversations, or should sessions be encrypted client-side so server stores opaque blobs?
2. Should “commit” be manual only, or should the system propose commits based on activity (tests run, diff size, time)? (See GCC-inspired `proactive_commits` behavior in Section 11.3.)
3. Should context be branch-scoped, feature-scoped, issue-scoped, or all three?
4. What is the minimum acceptable Cursor support for v1?

---

## 11. What to reuse from Git Context Controller (GCC)

Git Context Controller (GCC) is a Git-inspired *local* context controller that stores a structured memory hierarchy under a `.GCC/` directory (roadmap, commit history, activity log, metadata, and per-branch summaries). CtxHub is a *hosted, team-shared* system, but we can reuse several GCC patterns to improve usability and consistency:

### 11.1 Multi-resolution context retrieval (“ctx context”)
**Borrowed pattern:** GCC’s `CONTEXT` command supports retrieving historical memory at different resolution levels (branch summary, recent log entries, metadata, or “full” roadmap).  
**CtxHub mapping:** Add a first-class `ctx context` command (and corresponding MCP resources) with these levels:

- `ctx context --feature <name>`: feature overview + latest context commits (default view)
- `ctx context --log [n]`: a condensed recent-activity log (like an OTA log; see 11.4)
- `ctx context --metadata`: repo structure + policies + active feature streams + branch/PR mapping
- `ctx context --full`: “roadmap view” (objectives, milestones, active experiments, major decisions)

This becomes the fastest “where were we?” action for both humans and agents.

### 11.2 Roadmap + feature summaries
**Borrowed pattern:** GCC keeps a global roadmap (`main.md`) and per-branch `summary.md` (purpose, status, key decisions).  
**CtxHub mapping:** Maintain **Feature Overview** artifacts for each feature stream and branch/PR:

- **Feature Overview**: purpose, objectives, milestones, key decisions, status
- **Branch/Experiment Summary**: hypotheses, evaluation criteria, outcome, status (`active | merged | abandoned`)

These should be *small* and always safe to load (like context commits).

### 11.3 Proactive commits (“auto-suggest”)
**Borrowed pattern:** GCC supports `proactive_commits` to automatically suggest commits after coherent milestones.  
**CtxHub mapping:** Add a repo-level policy: `proactive_commits = true/false` (default off in MVP, on in teams that want it). When enabled, the CLI/plugin can suggest:

- after tests pass
- after large diffs
- after N turns in a session
- after “task boundary” events (e.g., `git commit`, PR opened)

### 11.4 Condensed activity log (OTA-inspired, derived)
**Borrowed pattern:** GCC logs a sequential OTA (Observation–Thought–Action) trace and caps it to a rolling window (e.g., last 50 entries).  
**CtxHub mapping:** Keep *full transcripts* as your source of truth, but also derive a **condensed activity log** per feature/branch:

- Each entry is a short record: `Observation / Decision / Action / Result`
- Cap the “quick log” to the most recent N entries (default 50, configurable)
- Link each entry back to session IDs and turn ranges

This gives a fast “recent activity” view without forcing anyone (or any model) to open full transcripts.

### 11.5 Merge synthesis commit + roadmap update
**Borrowed pattern:** GCC’s MERGE creates a synthesis commit summarizing what was tried/learned and updates the roadmap and metadata.  
**CtxHub mapping:** On PR merge (or explicit `ctx merge`), automatically create a **synthesis context commit** that:

- summarizes branch outcomes
- records key decisions + tradeoffs
- states what was integrated vs. abandoned
- updates the Feature Overview milestones

### 11.6 Optional GCC import/export
**Borrowed pattern:** GCC’s `.GCC/` directory structure is readable by humans and agents, and some teams may already be using it.  
**CtxHub mapping:** Provide optional compatibility:

- `ctx export gcc`: generate a `.GCC/` view from CtxHub data (roadmap, summaries, commit history, log)
- `ctx import gcc`: ingest an existing `.GCC/` directory into CtxHub (convert to feature overviews + context commits)

This is a low-risk way to support GCC-style workflows while keeping CtxHub’s server + MCP model as the primary approach.

---

## 12. References (for implementation inspiration)

- MCP Resources specification (resources/list, resources/read, URI schemes, templates, annotations): https://modelcontextprotocol.io/specification/2025-06-18/server/resources
- MCP TypeScript SDK (server/client, stdio + Streamable HTTP transports): https://github.com/modelcontextprotocol/typescript-sdk
- Claude Code hooks reference (UserPromptSubmit, Stop, async hooks, transcript_path): https://code.claude.com/docs/en/hooks
- OpenAI Codex CLI reference (`--json` output for automation): https://developers.openai.com/codex/cli/reference
- VS Code “Manage chat sessions” (export sessions as JSON): https://code.visualstudio.com/docs/copilot/chat/chat-sessions
- Cursor chat export (state.vscdb sqlite approach): https://github.com/somogyijanos/cursor-chat-export
- Git Context Controller (GCC) repo (commands, OTA log, proactive commits, file formats): https://github.com/faugustdev/git-context-controller
- GCC file formats (main.md, commit.md, log.md, metadata.yaml, branch summaries): https://raw.githubusercontent.com/faugustdev/git-context-controller/dev/references/file_formats.md
- Supermemory MCP setup (remote MCP URL + OAuth discovery): https://supermemory.ai/docs/supermemory-mcp/setup
- OpenContext MCP integration pattern (stdio MCP server bridging core): https://deepwiki.com/0xranx/OpenContext/5.2-mcp-server-integration
