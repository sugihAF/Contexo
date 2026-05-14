# CtxHub Technical Requirements (technical_requirements.md)

This document is an implementation-oriented specification for building:

- **CtxHub server** (GitHub-like context host)
- **`ctx` CLI** (Git-like developer UX)
- **`ctxd` local recorder** (always-on capture)
- **Adapters/plugins** for Claude Code, Codex CLI, Cursor, VS Code export/import
- **MCP server façade** (resources/templates) for AI clients

---

## 1. System overview (components)

### 1.1 `ctx` CLI
Responsibilities:
- Repository initialization (`ctx init`)
- Capture control (`ctx capture on/off/pause/resume/status`)
- Session lifecycle (`ctx session start/stop/ls/show/tail`)
- Context commits (`ctx commit`, `ctx log`, `ctx show`, `ctx link`, `ctx blame`)
- Multi-resolution context retrieval (`ctx context --feature/--log/--metadata/--full`)
- Feature overview/roadmap (`ctx feature summary`)
- Repo policy toggles (`ctx config set proactive_commits true/false`)
- Merge synthesis (`ctx merge` or automatic on PR merge)
- Optional GCC compatibility (`ctx export gcc`, `ctx import gcc`)
- Sync (`ctx push`, `ctx pull`)
- MCP local server (`ctx mcp`) for stdio transport

### 1.2 `ctxd` local recorder daemon
Responsibilities:
- Receive normalized events from adapters (stdin, local socket, HTTP on localhost)
- Write append-only session logs to `.ctx/sessions/**.jsonl`
- Store large tool outputs in `.ctx/blobs/` with content-addressed IDs
- Maintain local SQLite index for fast queries and offline operations
- Provide a local API for CLI queries (e.g., HTTP `localhost`, unix socket, or direct SQLite read)
- Support redaction and privacy policies (local-first)

### 1.3 Adapters / Integrations
- **Claude Code adapter**: uses Claude Code hooks to capture prompts and assistant outputs.
- **Codex CLI adapter**: wraps `codex exec` and captures `--json` machine-readable output.
- **VS Code adapter**: imports exported chat session JSON files.
- **Cursor adapter**: imports/watches `state.vscdb` SQLite database (best-effort; brittle by nature).

### 1.4 CtxHub server
Responsibilities:
- Multi-tenant org/repo model
- Auth + permissions
- Store context commits (metadata) + session logs (object storage)
- Search and symbol index
- REST API for CLI and web UI
- MCP server façade exposing resources by `ctx://` URIs

### 1.5 Web UI (recommended)
Responsibilities:
- Browse commit history
- Inspect commit details, linked code, evidence slices
- Search
- Admin policies (retention, redaction rules, access control)

---

## 2. Technology stack (recommended)

### 2.1 Languages
- **TypeScript** for server + MCP + CLI (fast iteration, shared types, MCP SDK maturity).
- Optional: Go/Rust for CLI if you prefer a single static binary later.

### 2.2 Server
- Node.js + Fastify (or NestJS)
- PostgreSQL (metadata)
- Object storage:
  - MinIO (dev)
  - S3 / Cloudflare R2 (prod)
- Cache/queue:
  - Redis + BullMQ (jobs), or Postgres-based job queue for MVP
- Search:
  - Postgres full-text search for MVP
  - OpenSearch/Elasticsearch later if needed

### 2.3 MCP
- Use official MCP TypeScript SDK for:
  - stdio server transport
  - Streamable HTTP server transport (if hosting MCP endpoint)
  - resources/templates/annotations support
  - OAuth helpers if using MCP auth patterns

### 2.4 Local recorder
- Node.js (ts-node / packaged) or Go
- SQLite for local index (`.ctx/index.sqlite`)
- File system for JSONL logs and blobs

---

## 3. Data model: canonical artifacts & schemas

### 3.1 Canonical storage rule
- **Session logs** are the immutable source of truth (append-only).
- **Context commits** are small, structured, and can be amended (like git) but changes are recorded.

### 3.2 Session event schema (`ctx.session_event.v1`)
Storage: `.ctx/sessions/<source>/<session_id>.jsonl` (JSONL stream)

Each line MUST be valid JSON. Minimum required keys:

```json
{
  "schema": "ctx.session_event.v1",
  "event_id": "uuid",
  "ts": "RFC3339 timestamp",
  "session": {
    "id": "S-B",
    "source": "claude_code|codex_cli|cursor|vscode",
    "tool_session_id": "tool-provided id (optional)"
  },
  "repo": {
    "remote": "git remote url (optional)",
    "worktree": "local path (optional)",
    "branch": "current branch name (optional)",
    "head": "git sha at capture time (optional)"
  },
  "actor": { "user": "username/email", "device": "hostname (optional)" },
  "turn": 42,
  "type": "session_start|session_end|user_message|assistant_message|tool_call|tool_result|checkpoint|error|decision_note",
  "content": {
    "text": "string (optional)",
    "refs": [
      { "kind": "file", "path": "src/..." },
      { "kind": "symbol", "name": "file::symbol" }
    ],
    "attachments": []
  },
  "tool": {
    "name": "rg|bash|pytest|...",
    "args": "string|object",
    "result_ref": "blob://<hash> (optional)"
  },
  "raw": { "vendor_specific": "anything" }
}
```

Constraints:
- `event_id` must be globally unique (UUIDv7 recommended).
- `turn` increments on each user prompt (assistant messages share the same turn).
- `tool_result` can store large outputs out-of-band via `result_ref`.

### 3.3 Blob store (local)
- `.ctx/blobs/<sha256>` stores:
  - tool outputs (stdout/stderr)
  - exported chat JSON files (optional)
- Blob metadata may live in SQLite (size, mime, created_at).

### 3.4 Context commit schema (`ctx.commit.v1`)
Storage: `.ctx/commits/<ctxCommitId>.json` (and optional `.md` render)

```json
{
  "schema": "ctx.commit.v1",
  "ctx_commit_id": "C-002",
  "repo_id": "repo_123",
  "feature": "onboarding",
  "branch": "feature/onboarding",
  "branch_purpose": "Short purpose statement (optional; from Feature Overview/branch summary)",
  "previous_progress": "1-2 sentence recap from previous related ctx commit (optional)",
  "created_at": "RFC3339",
  "author": { "name": "Jhon", "tool": "claude-code" },

  "title": "Email verification flow",
  "summary": [
    "Wired email verification endpoint",
    "Added resend skeleton with basic rate-limit guard"
  ],
  "decisions": [
    { "decision": "Rate limit resend", "why": "Prevent abuse and cost spikes" }
  ],
  "next_steps": [
    "Add UI resend button + cooldown",
    "Add tests for rate limit"
  ],

  "changes": {
    "git": { "linked_commits": ["a1b2c3d"], "pr": "PR-128" },
    "files_touched": [
      { "path": "src/onboarding/verify.ts", "symbols": ["sendVerificationEmail"] }
    ],
    "tests": [
      { "cmd": "pnpm test onboarding", "result": "pass", "ts": "RFC3339" }
    ]
  },

  "evidence": [
    { "session_id": "S-B", "source": "claude_code", "turns": [40, 65] }
  ]
}
```

Rules:
- `summary` SHOULD be short bullets.
- `decisions` MUST be explicit; do not rely on hidden chain-of-thought.
- `evidence` references a slice of sessions; do not inline full transcripts.

### 3.5 Local index (SQLite)
Suggested tables:
- `sessions(session_id, source, started_at, ended_at, branch, feature, last_event_ts, event_count)`
- `events(session_id, seq, ts, turn, type, text_preview, file_refs_json, symbol_refs_json, blob_ref)`
- `commits(ctx_commit_id, created_at, feature, branch, title, summary_text, author, linked_git_json)`
- `commit_files(ctx_commit_id, path)`
- `commit_symbols(ctx_commit_id, symbol_key)`
- `symbol_index(symbol_key, ctx_commit_id, created_at)` (materialized convenience)
- `git_links(git_sha, ctx_commit_id)` (bidirectional mapping)


### 3.6 Feature overview / roadmap (`ctx.feature_overview.v1`) — GCC-inspired
A **Feature Overview** is a small living artifact that answers:
- what is this feature trying to achieve?
- what milestones are done / remaining?
- what are the key decisions?
- what are the next steps?
- what experiments/branches exist?

Storage (local): `.ctx/features/<feature>/overview.json` (optional; can be cached from server)  
Storage (server): `feature_overviews` table (metadata) + versioned object in object storage (optional).

Example:

```json
{
  "schema": "ctx.feature_overview.v1",
  "repo_id": "repo_123",
  "feature": "onboarding",
  "updated_at": "RFC3339",

  "purpose": "Improve onboarding completion rate while keeping latency low.",
  "objectives": [
    { "id": "O1", "text": "Email verification", "status": "done" },
    { "id": "O2", "text": "Invite codes", "status": "active" }
  ],
  "milestones": [
    { "id": "M1", "title": "Step 1 UI + routing", "status": "done", "ctx_commits": ["C-001"] },
    { "id": "M2", "title": "Verification flow", "status": "done", "ctx_commits": ["C-002"] },
    { "id": "M3", "title": "Resend UX + cooldown", "status": "active", "ctx_commits": ["C-004"] }
  ],
  "key_decisions": [
    { "decision": "Rate-limit resend", "why": "Prevent abuse and provider costs", "ctx_commit_id": "C-002" }
  ],
  "active_experiments": [
    {
      "branch": "experiment/onboarding-cache",
      "status": "active",
      "purpose": "Reduce p95 onboarding latency via caching",
      "hypothesis": "Cache profile+org lookup for 60s reduces p95",
      "created_at": "RFC3339"
    }
  ],
  "pinned": {
    "ctx_commits": ["C-002"],
    "sessions": ["S-B"]
  },
  "next_steps": [
    "Implement resend cooldown UI",
    "Add tests for resend rate limit"
  ]
}
```

Notes:
- This is the CtxHub analog of GCC’s `main.md` and branch `summary.md` (roadmap + purpose + key decisions).
- Feature Overview should stay **small** (safe to load into agents).

### 3.7 Condensed activity log (`ctx.activity_entry.v1`) — GCC-inspired OTA log
You capture **every prompt + response** in session logs, but you also want a fast “recent activity” view.

**Approach:**
- Maintain a derived log per feature/branch with short entries (Observation → Decision → Action → Result).
- Default cap: last **50** entries (configurable), similar to GCC’s rolling OTA log window.

Storage (local): `.ctx/activity/<feature>.jsonl` (derived; can be regenerated)  
Storage (server): object storage (chunked by day/week) + index in Postgres.

Example entry:

```json
{
  "schema": "ctx.activity_entry.v1",
  "repo_id": "repo_123",
  "feature": "onboarding",
  "branch": "feature/onboarding",
  "ts": "RFC3339",
  "observation": "Resend endpoint returns 429 after 3 attempts",
  "decision": "Keep limit at 3 per 10 minutes",
  "action": "Added UI cooldown timer; updated resend handler",
  "result": "Tests pass",
  "evidence": { "session_id": "S-D", "turns": [12, 18] },
  "links": { "ctx_commit_id": "C-004", "git_sha": "d4e5f6" }
}
```

Generation options:
- **Automatic**: derive entries from session events (tool calls/tests/checkpoints) and commit summaries.
- **Manual**: `ctx note` or `ctx activity add` for high-signal moments.

### 3.8 Branch / experiment summary (`ctx.branch_summary.v1`) — GCC-inspired
For experiments/branches, store a short summary (purpose, hypothesis, criteria, outcome).

Server table: `branches` + `branch_summaries`  
Minimal fields:
- `branch_name`, `parent_branch`, `status` (`active|merged|abandoned`)
- `purpose`, `hypothesis`, `criteria`
- `outcome` (filled on merge/abandon)
- link to synthesis ctx commit (if merged)

### 3.9 Repo policy & configuration (`ctx.repo_policy.v1`)
Include at minimum:
- `proactive_commits` boolean (auto-suggest)
- retention window(s) per org/repo
- redaction rules / path denylist
- capture adapters enabled
- encryption mode (server-side vs client-side)

This can live in:
- local `.ctx/policy.json` (effective policy)
- server DB as canonical policy with audit history

### 3.10 Optional GCC import/export mapping
Support best-effort interoperability with `.GCC/`:
- `main.md` → Feature Overview roadmap fields
- `commit.md` → Context commits (no transcripts, but keep decisions/summary)
- `log.md` → condensed activity entries
- `metadata.yaml` → repo policy + branch registry + file tree snapshot


---

## 4. MCP layer design (resources, templates, annotations)

### 4.1 Custom URI scheme
- Use `ctx://` for all resources.
- URIs must be RFC3986-compliant and stable over time.
- Publish **resource templates** (`resources/templates/list`) so clients can discover parameterized URIs (repoId, feature, commitId, sessionId, symbolKey).

### 4.2 Required resources (v1)

#### Commit browsing
- `ctx://repo/<repoId>/features`
- `ctx://repo/<repoId>/features/<feature>/commits?limit=50&cursor=...`

#### Feature overview / roadmap (small, safe)
- `ctx://repo/<repoId>/features/<feature>/overview`

#### Multi-resolution context (“where were we?”)
- `ctx://repo/<repoId>/context?level=feature&feature=<feature>`
- `ctx://repo/<repoId>/context?level=log&feature=<feature>&limit=20`
- `ctx://repo/<repoId>/context?level=metadata`
- `ctx://repo/<repoId>/context?level=full` (roadmap)

Return: `application/json`  
Shape: array of `{ ctx_commit_id, title, summary_preview, created_at, author, linked_git[] }`

#### Commit details
- `ctx://repo/<repoId>/commit/<ctx_commit_id>`
Return: `application/json` (the full commit object)

#### Session browsing
- `ctx://repo/<repoId>/session/<session_id>/meta`
Return: small JSON: `{source, started_at, ended_at, branch, feature, event_count}`

#### Session slice read (critical for selective loading)
- `ctx://repo/<repoId>/session/<session_id>/turns/<from>-<to>`
Return: `application/x-jsonlines` (filtered JSONL)

Alternative query form:
- `ctx://repo/<repoId>/session/<session_id>?select=turns&from=40&to=65`
- `ctx://repo/<repoId>/session/<session_id>?select=decisions`
- `ctx://repo/<repoId>/session/<session_id>?select=tool_calls&file=src/onboarding/verify.ts`

#### Symbol blame
- `ctx://repo/<repoId>/blame/symbol/<urlencodedSymbolKey>`
Return: JSON array of commits + evidence pointers.

#### Search (MVP)
- `ctx://repo/<repoId>/search?q=<query>&limit=20`

### 4.3 Resource templates
Expose templates so clients can discover parameterized resources:

- `ctx://repo/{repoId}/commit/{ctxCommitId}`
- `ctx://repo/{repoId}/session/{sessionId}/turns/{from}-{to}`
- `ctx://repo/{repoId}/blame/symbol/{symbolKey}`

### 4.4 Resource annotations
For selective context loading, set annotations:
- commit list: `priority=0.6`, `audience=["assistant","user"]`
- commit detail: `priority=0.8`
- session slice: `priority=0.4`
- full session: `priority=0.1`

---

## 5. Server API requirements (REST)

> The MCP façade can call the same internal service methods; REST is primarily for CLI/web.

### 5.1 Auth
- Support API keys for MVP.
- Add OAuth/OIDC later (SSO).
- Token scopes:
  - `repo:read`, `repo:write`
  - `sessions:read`, `sessions:write`
  - `admin:policy`

### 5.2 Core endpoints (v1)
- `POST /v1/orgs`
- `POST /v1/repos`
- `GET  /v1/repos/{repoId}`
- `GET  /v1/repos/{repoId}/features`
- `GET  /v1/repos/{repoId}/features/{feature}/commits`
- `GET  /v1/repos/{repoId}/commits/{ctxCommitId}`
- `POST /v1/repos/{repoId}/commits` (upload commit JSON)

Feature overview / context levels:
- `GET  /v1/repos/{repoId}/features/{feature}/overview`
- `PUT  /v1/repos/{repoId}/features/{feature}/overview` (optional; usually server-generated)
- `GET  /v1/repos/{repoId}/context?level=feature&feature={feature}`
- `GET  /v1/repos/{repoId}/context?level=log&feature={feature}&limit=20`
- `GET  /v1/repos/{repoId}/context?level=metadata`
- `GET  /v1/repos/{repoId}/context?level=full`

Experiments/branches:
- `POST /v1/repos/{repoId}/branches` (create/update branch summary)
- `GET  /v1/repos/{repoId}/branches?status=active`
- `POST /v1/repos/{repoId}/merge-synthesis` (generate synthesis ctx commit for a branch/PR)

Policy:
- `GET  /v1/repos/{repoId}/policy`
- `PUT  /v1/repos/{repoId}/policy`

GCC compatibility (optional):
- `POST /v1/repos/{repoId}/import/gcc` (multipart or zip)
- `GET  /v1/repos/{repoId}/export/gcc?feature={feature}` (zip)

Sessions:
- `POST /v1/repos/{repoId}/sessions` (create session meta)
- `PUT  /v1/repos/{repoId}/sessions/{sessionId}/chunk` (upload JSONL chunk)
- `GET  /v1/repos/{repoId}/sessions/{sessionId}/meta`
- `GET  /v1/repos/{repoId}/sessions/{sessionId}/slice?fromTurn=&toTurn=&select=`
- `GET  /v1/repos/{repoId}/symbols/{symbolKey}/blame`

Git linking:
- `POST /v1/repos/{repoId}/git-links` with `{gitSha, ctxCommitId}`
- `GET  /v1/repos/{repoId}/git/{gitSha}/related`

### 5.3 Storage strategy
- Session JSONL is chunked:
  - by turn range (e.g., 0–50, 51–100)
  - or by size threshold (e.g., 1–5MB)
- Object keys include repoId/sessionId/chunkId.
- Compress JSONL with gzip/zstd.

---

## 6. Capture adapters: implementation requirements

### 6.1 Claude Code (hooks-based capture)
Claude Code provides hook events including:
- `UserPromptSubmit` with prompt text and session metadata
- `Stop` with the last assistant message
- Support for `async` command hooks (non-blocking)

Implementation approach:
1. `ctx capture on --client claude-code` generates/installs hooks:
   - Hook commands should send JSON payloads to `ctxd` (localhost HTTP or unix socket).
2. Hooks MUST:
   - write nothing to stdout unless explicitly desired (stdout may be added to context by Claude).
   - handle retries if `ctxd` is not available (buffer locally).
3. Each hook converts vendor payload into `ctx.session_event.v1`.

Minimum hook set:
- `UserPromptSubmit` → `user_message`
- `Stop` → `assistant_message`

Optional hook set:
- `PreToolUse`/`PostToolUse` → `tool_call`/`tool_result` (store large outputs as blobs)

### 6.2 Codex CLI (wrapper capture)
Codex CLI supports `--json` for automation output. Requirements:
- `ctx codex exec "<task>"` must:
  1) run `codex exec --json "<task>"`
  2) parse stdout JSON stream
  3) normalize to ctx session events
  4) write JSONL
- Handle non-zero exit codes:
  - create `error` event with stderr preview and exit status.

### 6.3 VS Code chat session import
VS Code supports exporting chat sessions as JSON. Requirements:
- `ctx vscode import <chat.json>` converts exported format into session events.
- Store original file as blob for audit if desired.

### 6.4 Cursor import/watch (best-effort)
Cursor chat history stored in `state.vscdb` SQLite DB per workspace. Requirements:
- `ctx cursor import --workspace <path>` locates DB and extracts chats into sessions.
- `ctx cursor watch` maintains a last-seen cursor and ingests incrementally.
- Mark source as `cursor` and store raw row JSON in `raw`.

---

## 7. Context commit generation requirements

### 7.0 Proactive commit suggestion engine (GCC-inspired)
When enabled via repo policy `proactive_commits: true`, the system SHOULD suggest creating a ctx commit after coherent milestones:
- tests pass events
- git commit created
- PR opened/updated
- session ended after N turns
- large diff detected

Implementation notes:
- suggestions are *client-side* (CLI/IDE) to avoid server spam
- suggestions include proposed title + evidence slice
- user can accept, dismiss, or snooze


### 7.1 Commit boundary selection
`ctx commit` must support:
- default: use latest active session + last N turns
- user overrides:
  - `--from-session <id>`
  - `--turns 40..65`
  - `--since 30m`
  - `--feature onboarding`

### 7.2 Summary generation
Two modes:
- **Manual-first MVP**:
  - CLI prompts user for summary bullets/decisions/next steps.
- **Assisted**:
  - CLI asks the current AI tool to produce a structured summary (optional, behind a flag).

Regardless, the output is stored as `ctx.commit.v1`.

### 7.3 Linkage signals
The commit generator should include:
- `git` info: branch, head, linked commits
- file list: `git diff --name-only`
- optional symbol extraction:
  - simple regex-based function/class detection per language
  - or tree-sitter later


### 7.4 Merge synthesis commit (GCC-inspired)
When a git branch/PR is merged (or explicitly via `ctx merge <branch>`), the system SHOULD create a **synthesis context commit** on the main feature stream.

**Trigger sources:**
- CLI: `ctx merge <branch>`
- Server automation: PR merged webhook (GitHub/GitLab) (MVP-3+ if you don’t want integrations early)

**Inputs:**
- Branch/experiment summary (`ctx.branch_summary.v1`) including purpose/hypothesis/criteria
- All ctx commits on that branch (or since branch creation)
- Linked git commits and PR metadata
- A small set of high-signal evidence slices (e.g., last commit on branch, failing/passing tests)

**Output:**
- A new `ctx.commit.v1` with:
  - `title`: “Merge <branch>: <short outcome>”
  - `summary`: what was tried, what worked, what was integrated
  - `decisions`: final decisions and “why”
  - `changes.git.pr`: link to PR
  - `previous_progress`: recap of the branch intent and prior state
  - `branch_purpose`: pulled from the branch summary

**Side effects:**
- Update the Feature Overview milestones and mark experiment status as `merged` or `abandoned`.
- (Optional) mark branch commits as “included in synthesis” for UI de-duplication.


---

## 8. Security, privacy, and compliance requirements (must-have)

### 8.1 Redaction pipeline (local-first)
- Regex patterns for common secrets
- Entropy heuristics for tokens
- Allow/deny path list
- User commands:
  - `ctx capture pause/resume`
  - `ctx redact scan` (optional)

### 8.2 Encryption
- At rest encryption for server object storage.
- Optional client-side encryption:
  - server stores encrypted blobs; only clients with key can decrypt.

### 8.3 Access control
- Org membership
- Repo-level read/write
- Optional per-branch/feature restrictions
- Session visibility level:
  - `team` (default)
  - `private` (local-only or encrypted)
  - `public` (rare)

### 8.4 Retention policies
- TTL for session logs (e.g., 90 days) configurable by org
- Context commits may live longer than session logs, but should keep evidence pointers with status:
  - `available` / `expired` / `redacted`

---

## 9. Performance and scaling requirements

### 9.1 Local performance
- Writing JSONL must be O(1) append with fsync batching.
- SQLite index updates must be batched.
- Large blobs must be content-addressed and deduplicated.

### 9.2 Server performance
- Session chunk uploads should be resumable.
- Search must be incremental.
- MCP resources should be cached (ETag/If-Modified-Since) where possible.

### 9.3 Retrieval efficiency (selective loading)
- `ctx log` should use local index first.
- MCP resources should prefer:
  - commit summaries
  - commit details
  - session slices
over full sessions.

---

## 10. Testing requirements

### 10.1 Unit tests
- schema validation
- redaction rules
- session chunking/slicing
- symbolKey encoding/decoding
- CLI argument parsing

### 10.2 Integration tests
- CLI ↔ ctxd
- ctx push/pull against local server + MinIO
- MCP resources/list/read correctness
- Claude hook simulation payloads
- Codex json stream parsing

### 10.3 End-to-end tests
- Scenario:
  - Dev A captures Claude session → commit → push
  - Dev B pulls → sees log → drills down → adds commit → push
  - AI via MCP reads commit list and a session slice

---

## 11. Implementation checklist (v1)

1. Define JSON schemas (`ctx.session_event.v1`, `ctx.commit.v1`) and validators.
2. Implement `ctxd`:
   - append-only JSONL writer
   - blob store
   - SQLite index
3. Implement `ctx` CLI baseline:
   - init, status, capture on/off, commit, log, show, open-session, push/pull
4. Implement server:
   - auth
   - commit endpoints
   - session chunk upload + slice endpoint
   - object storage integration
5. Implement MCP façade:
   - resources/list, resources/read, templates/list
6. Implement adapters:
   - Claude hooks generator + receiver
   - Codex wrapper
   - VS Code import
   - Cursor import/watch
7. Add redaction + pause capture
8. Add `ctx blame <symbol>` based on symbol index.

---

## 12. References (official / high-signal)

- GCC paper (conceptual design, COMMIT/BRANCH/MERGE/CONTEXT): https://arxiv.org/abs/2508.00031

- Git Context Controller (GCC) repo (local file structure, commands, proactive commits): https://github.com/faugustdev/git-context-controller
- GCC file formats (main.md/commit.md/log.md/metadata.yaml examples): https://raw.githubusercontent.com/faugustdev/git-context-controller/dev/references/file_formats.md

- MCP Resources spec (URIs, list/read, templates, annotations): https://modelcontextprotocol.io/specification/2025-06-18/server/resources
- MCP TypeScript SDK (server/client, stdio + Streamable HTTP): https://github.com/modelcontextprotocol/typescript-sdk
- Claude Code hooks reference (UserPromptSubmit, Stop, async): https://code.claude.com/docs/en/hooks
- OpenAI Codex CLI reference (`--json`): https://developers.openai.com/codex/cli/reference
- VS Code chat sessions export JSON: https://code.visualstudio.com/docs/copilot/chat/chat-sessions
- Cursor chat export from `state.vscdb` (sqlite): https://github.com/somogyijanos/cursor-chat-export
- Cursor MCP docs (client configuration): https://cursor.com/docs/context/mcp
- Supermemory MCP remote setup & OAuth discovery pattern: https://supermemory.ai/docs/supermemory-mcp/setup
- OpenContext MCP integration (stdio bridge pattern): https://deepwiki.com/0xranx/OpenContext/5.2-mcp-server-integration
