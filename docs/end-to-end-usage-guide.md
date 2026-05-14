# CtxHub End-to-End Usage Guide

> A complete walkthrough of the `ctx` CLI from the perspective of a developer using AI-assisted tools (Claude Code, Codex). This guide shows every user action and explains the internal mechanisms behind the curtain.

---

## Table of Contents

1. [Overview & Architecture](#1-overview--architecture)
2. [Installation & Build](#2-installation--build)
3. [Phase 1: Initialize a Project](#3-phase-1-initialize-a-project)
4. [Phase 2: Configure the Project](#4-phase-2-configure-the-project)
5. [Phase 3: Set Up Remote Server & Auth](#5-phase-3-set-up-remote-server--auth)
6. [Phase 4: Start Capturing AI Sessions](#6-phase-4-start-capturing-ai-sessions)
7. [Phase 5: AI Session Happens (Events Flow In)](#7-phase-5-ai-session-happens-events-flow-in)
8. [Phase 6: Browse Captured Sessions](#8-phase-6-browse-captured-sessions)
9. [Phase 7: Create a Context Commit](#9-phase-7-create-a-context-commit)
10. [Phase 8: Browse the Commit Log](#10-phase-8-browse-the-commit-log)
11. [Phase 9: Link to Git](#11-phase-9-link-to-git)
12. [Phase 10: Drill into Evidence](#12-phase-10-drill-into-evidence)
13. [Phase 11: Query Context at Multiple Resolutions](#13-phase-11-query-context-at-multiple-resolutions)
14. [Phase 12: Symbol Blame](#14-phase-12-symbol-blame)
15. [Phase 13: Pause & Resume Capture](#15-phase-13-pause--resume-capture)
16. [Phase 14: Stop Capture](#16-phase-14-stop-capture)
17. [Phase 15: Push & Pull (Server Sync)](#17-phase-15-push--pull-server-sync)
18. [Phase 16: Check Project Status](#18-phase-16-check-project-status)
19. [Phase 17: Run Codex Tasks](#19-phase-17-run-codex-tasks)
20. [Phase 18: MCP Server for AI Tools](#20-phase-18-mcp-server-for-ai-tools)
21. [Data Model Reference](#21-data-model-reference)
22. [File Structure Reference](#22-file-structure-reference)
23. [Server API Reference](#23-server-api-reference)
24. [MCP Resource Reference](#24-mcp-resource-reference)

---

## 1. Overview & Architecture

CtxHub ("Contexo") is a system that captures, stores, and retrieves the context from AI-assisted development sessions. Think of it as **"git for AI context"** — it records what an AI said, what decisions were made, and links that knowledge to your git commits.

```
┌────────────────────┐     ┌─────────────────────┐
│  Claude Code /     │     │   ctx CLI            │
│  Codex / VS Code   │────>│  (local machine)     │
│  (AI tools)        │     │                      │
└────────────────────┘     │  ┌─────────────────┐ │
     Events via hooks      │  │ Recorder Daemon  │ │
     (POST /event)         │  │ (HTTP on :19476) │ │
                           │  └────────┬────────┘ │
                           │           │           │
                           │  ┌────────▼────────┐ │
                           │  │ Storage Layer    │ │
                           │  │  SQLite (index)  │ │
                           │  │  BoltDB (blobs)  │ │
                           │  │  JSONL (events)  │ │
                           │  └─────────────────┘ │
                           └───────────┬───────────┘
                                       │ ctx push/pull
                           ┌───────────▼───────────┐
                           │   CtxHub Server        │
                           │  (ctxhub, Gin-based)   │
                           │  PostgreSQL + S3       │
                           └────────────────────────┘
```

**Two binaries:**
- `ctx` — the CLI tool developers interact with
- `ctxhub` — the server for team-wide context sharing

---

## 2. Installation & Build

```bash
# Clone the repository
git clone https://github.com/sugihAF/contexo.git
cd contexo

# Build both binaries (requires Go 1.23+ and CGO for SQLite)
CGO_ENABLED=1 go build -o bin/ctx ./cmd/ctx
CGO_ENABLED=1 go build -o bin/ctxhub ./cmd/ctxhub

# Add to PATH
export PATH="$PWD/bin:$PATH"
```

**Behind the curtain:**
- `cmd/ctx/main.go` calls `cli.NewRootCmd().Execute()` which registers all Cobra subcommands
- `cmd/ctxhub/main.go` starts the Gin HTTP server with `server.NewRouter()`
- CGO is required because `mattn/go-sqlite3` is a C binding to SQLite

---

## 3. Phase 1: Initialize a Project

### User Action
```bash
cd ~/myproject
ctx init
```

### Output
```
Initialized .ctx in /home/user/myproject
```

### Behind the Curtain (`internal/cli/init.go`)

1. **Creates directory structure:**
   ```
   .ctx/
   .ctx/sessions/       ← JSONL event files go here
   .ctx/commits/        ← Commit JSON files go here
   .ctx/blobs/          ← Large content blob files
   ```

2. **Writes `config.json`** (only if it doesn't exist):
   ```json
   {
     "version": 1,
     "recorder_port": 19476,
     "default_client": "claude_code",
     "redaction_level": "standard"
   }
   ```
   Default config is created by `config.DefaultConfig()` in `internal/config/config.go`.

3. **Creates SQLite database** at `.ctx/index.sqlite`:
   - Opens the DB via `sqlitestore.Open(path)`
   - Runs `db.Migrate()` which creates tables: `commits`, `sessions`, `events`, `git_links`, `commit_symbols`, `overviews`, `activity_log`, `sync_log`
   - Tables use `data_json TEXT` columns for full JSON storage alongside denormalized index columns (`commit_id`, `title`, `feature`, `author`, etc.)

4. **Creates BoltDB** at `.ctx/blobs.db`:
   - Used for storing large text content (>10KB) as content-addressed blobs
   - Opened via `boltdbstore.New(path, blobDir)`

---

## 4. Phase 2: Configure the Project

### User Action: Set config values
```bash
# Set a repository ID (used for server sync)
ctx config set repo_id my-project-123

# Set recorder port
ctx config set recorder_port 9999

# Set default AI client
ctx config set default_client claude_code

# Set redaction level
ctx config set redaction_level standard
```

### Output
```
Set repo_id = my-project-123
```

### User Action: Read config values
```bash
# Get a single value
ctx config get repo_id

# Get all config as JSON
ctx config get
```

### Behind the Curtain (`internal/cli/configcmd.go`)

1. `config.Load(root)` reads `.ctx/config.json` from disk
2. For `set`: the matching field in the `Config` struct is updated, then `config.Save(root, cfg)` writes back to disk
3. Valid keys: `server_url`, `repo_id`, `default_client`, `redaction_level`, `remote_name`, `recorder_port`
4. `recorder_port` is parsed as an integer via `strconv.Atoi()` — invalid values produce an error
5. Unknown keys return: `"unknown config key: <key>"`

---

## 5. Phase 3: Set Up Remote Server & Auth

### User Action: Add a remote server
```bash
ctx remote add origin https://ctxhub.example.com
```

### Output
```
Remote 'origin' added: https://ctxhub.example.com
```

### Behind the Curtain (`internal/cli/remote.go`)

1. Loads config from `.ctx/config.json`
2. Checks for duplicate remote names
3. Appends `Remote{Name: "origin", URL: "https://ctxhub.example.com"}` to `cfg.Remotes`
4. If this is the **first remote**, automatically sets it as default:
   - `cfg.RemoteName = "origin"`
   - `cfg.ServerURL = "https://ctxhub.example.com"`
5. Saves updated config

### User Action: List remotes
```bash
ctx remote ls
```

### Output
```
* origin	https://ctxhub.example.com
```
The `*` marks the active remote.

### User Action: Authenticate
```bash
# With flag (non-interactive)
ctx auth login --api-key sk-abc123def456

# Or interactive (prompts for key)
ctx auth login
```

### Output
```
Authenticated successfully (server: https://ctxhub.example.com)
```

### Behind the Curtain (`internal/cli/auth.go`)

1. If `--api-key` flag is empty, reads from stdin via `bufio.NewReader(os.Stdin)`
2. Resolves server URL from: `--server` flag → `cfg.ServerURL` → first remote URL
3. Writes credentials to `.ctx/credentials.json` via `config.SaveCredentials()`:
   ```json
   {
     "api_key": "sk-abc123def456",
     "server_url": "https://ctxhub.example.com"
   }
   ```
4. File has `0600` permissions for security

### User Action: Check auth status
```bash
ctx auth status
```

### Output
```
Authenticated: yes
Server: https://ctxhub.example.com
API Key: sk-a...f456
```
The API key is masked: first 4 chars + `...` + last 4 chars.

---

## 6. Phase 4: Start Capturing AI Sessions

### User Action
```bash
# Start capture for Claude Code (default)
ctx capture on

# Or specify client
ctx capture on --client claude-code
```

### Output
```
Capture started on port 19476 (client: claude_code)
Listening at http://127.0.0.1:19476
```
The process blocks here (stays in foreground).

### Behind the Curtain (`internal/cli/capture.go`)

This is a **long-running daemon process**. Here's what happens:

1. **Loads config** from `.ctx/config.json` to get `RecorderPort` and `DefaultClient`

2. **Opens stores:**
   - SQLite at `.ctx/index.sqlite` (for indexing events)
   - BoltDB at `.ctx/blobs.db` (for large content blobs)

3. **Creates Recorder** (`internal/recorder/recorder.go`):
   - `recorder.New(ctxDir, db, blobs)` — holds refs to both stores
   - Initializes a `redaction.Pipeline` with default patterns (secrets, API keys, tokens, etc.)
   - `BlobThreshold = 10000` (10KB) — content larger than this gets stored as a blob

4. **Starts HTTP server** (`internal/recorder/http.go`):
   - Listens on `127.0.0.1:<port>` (default 19476)
   - Two endpoints:
     - `POST /event` — receives and processes events
     - `GET /health` — health check

5. **Generates Claude Code hooks** (if client is `claude_code`):
   - Writes `.ctx/hooks.json` with curl commands that POST hook payloads:
     ```json
     {
       "hooks": {
         "UserPromptSubmit": {
           "command": "curl -s -X POST http://127.0.0.1:19476/event -H \"Content-Type: application/json\" -d \"$CLAUDE_HOOK_PAYLOAD\"",
           "timeout": 5
         },
         "Stop": {
           "command": "curl -s -X POST http://127.0.0.1:19476/event ...",
           "timeout": 5
         }
       }
     }
     ```

6. **Saves capture state** to `.ctx/capture_state.json`:
   ```json
   {
     "active": true,
     "port": 19476,
     "adapters": ["claude_code"],
     "pid": 12345
   }
   ```

7. **Writes PID file** to `.ctx/recorder.pid`

8. **Blocks on signal** (`SIGINT` / `SIGTERM`):
   ```go
   sigCh := make(chan os.Signal, 1)
   signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
   <-sigCh
   ```
   When a signal is received, it shuts down the server and cleans up.

---

## 7. Phase 5: AI Session Happens (Events Flow In)

### What the User Does
The user works normally with Claude Code, Codex, or VS Code. Events flow automatically via hooks.

### Behind the Curtain — Event Ingestion

When Claude Code triggers a hook (e.g., `UserPromptSubmit`), it POSTs a JSON payload to `http://127.0.0.1:19476/event`.

**Event Schema** (`internal/schema/session_event.go`):
```json
{
  "schema": "ctx.session_event.v1",
  "event_id": "01234567-89ab-cdef-0123-456789abcdef",
  "ts": "2024-01-15T10:30:00Z",
  "session": {
    "id": "session-uuid",
    "source": "claude_code",
    "repo": { "id": "my-project-123" }
  },
  "type": "user_message",
  "turn": 1,
  "actor": { "role": "user" },
  "content": {
    "text": "Fix the login bug in auth.go",
    "refs": [{ "path": "auth.go", "type": "file" }]
  }
}
```

**Processing pipeline** (`internal/recorder/recorder.go → IngestEvent()`):

```
POST /event  →  HTTP Handler  →  Recorder.IngestEvent()
                                      │
                              ┌───────┴───────┐
                              │  1. Redact     │
                              │  (pipeline)    │
                              └───────┬───────┘
                                      │
                              ┌───────┴───────┐
                              │  2. Blob?      │
                              │  (>10KB)       │
                              └───────┬───────┘
                                      │
                         ┌────────────┼────────────┐
                         │            │            │
                  ┌──────┴─────┐ ┌───┴────┐ ┌────┴─────┐
                  │ 3. JSONL   │ │4. SQLite│ │5. Session│
                  │ (append)   │ │(index)  │ │ (upsert) │
                  └────────────┘ └────────┘ └──────────┘
```

**Step 1 — Redaction** (`internal/redaction/pipeline.go`):
- Deep copies the event (original never mutated)
- Applies regex patterns to scrub: AWS keys, API tokens, passwords, connection strings, etc.
- Filters out file refs matching deny-listed paths (e.g., `.env`, `credentials.json`)

**Step 2 — Blob storage** (if `len(content.Text) > 10KB`):
- Content gets hashed and stored in BoltDB (`boltdbstore.Put()`)
- Text is replaced with `[blob:<hash>]` reference

**Step 3 — JSONL persistence**:
- Events are appended to `.ctx/sessions/<source>/<session-id>.jsonl`
- One file per session, one JSON line per event
- `jsonl.Writer.Append()` handles file creation and atomic writes

**Step 4 — SQLite indexing**:
- `db.InsertEvent()` writes to the `events` table with denormalized columns
- Enables fast queries by session, type, turn number

**Step 5 — Session upsert**:
- `db.IncrementSessionEventCount()` inserts or updates the `sessions` table
- Tracks: session ID, source, started_at timestamp, event count

---

## 8. Phase 6: Browse Captured Sessions

### User Action: List sessions
```bash
ctx session ls
```

### Output
```
ID                                     SOURCE          STARTED                  EVENTS
a1b2c3d4-5678-9abc-def0-123456789abc   claude_code     2024-01-15 10:30:00      47
e5f6g7h8-9012-3456-7890-abcdef012345   claude_code     2024-01-15 14:15:00      23
```

### Behind the Curtain (`internal/cli/session.go`)
1. Opens SQLite via `openDB(root)`
2. Calls `db.ListSessions(ctx, store.SessionFilter{})` which queries the `sessions` table
3. Results ordered by `started_at DESC`
4. Optional `--feature` flag filters by feature name

### User Action: Show session events
```bash
ctx session show a1b2c3d4-5678-9abc-def0-123456789abc

# Or show specific turn range
ctx session show a1b2c3d4-... --turns 5-10
```

### Output
```
Session: a1b2c3d4-... (source: claude_code)
Started: 2024-01-15 10:30:00

[Turn 1] user_message (user):
  Fix the login bug in auth.go

[Turn 2] assistant_message (assistant):
  I'll look at the auth.go file to find the login bug...

[Turn 3] tool_use (assistant):
  Reading file auth.go
```

### Behind the Curtain
1. Looks up session metadata from SQLite to get its `source`
2. Constructs JSONL path: `.ctx/sessions/claude_code/<session-id>.jsonl`
3. Uses `jsonl.NewReader(path)` to read events
4. If `--turns 5-10` specified, `reader.ReadRange(5, 10)` filters by turn number
5. Renders each event in `[Turn N] type (role): content` format

### User Action: Show last events
```bash
ctx session tail a1b2c3d4-...
```
Shows the last 10 events of a session (calls `reader.ReadAll()` then slices).

---

## 9. Phase 7: Create a Context Commit

A context commit is a structured summary of what happened in an AI session — the decisions made, evidence from sessions, and what to do next.

### User Action: Simple commit
```bash
ctx commit -m "Fix authentication bypass in login handler"
```

### User Action: Rich commit with all flags
```bash
ctx commit \
  -m "Implement rate limiting for login endpoint" \
  --feature auth \
  --summary "Added rate limiter middleware" \
  --summary "Configured 5 attempts per minute per IP" \
  --decision "Use token bucket:Simpler than sliding window for our scale" \
  --decision "Store in Redis:Need shared state across instances" \
  --next-step "Add rate limit headers to responses" \
  --next-step "Create admin endpoint to reset limits" \
  --author "Alice:claude-code" \
  --branch "feature/rate-limiting" \
  --from-session a1b2c3d4-... \
  --turns 15-30
```

### Output
```
Created context commit: 01957a4b-8e3f-7000-b000-123456789abc
  Title: Implement rate limiting for login endpoint
```

### Behind the Curtain (`internal/cli/commit.go`)

1. **Generates a UUID v7** commit ID (time-ordered): `uuid.Must(uuid.NewV7())`

2. **Builds the ContextCommit struct** (`internal/schema/commit.go`):
   ```go
   commit := &schema.ContextCommit{
       Schema:    "ctx.commit.v1",
       CommitID:  "01957a4b-...",
       Title:     "Implement rate limiting for login endpoint",
       Feature:   "auth",
       CreatedAt: time.Now().UTC(),
       Summary:   ["Added rate limiter middleware", "Configured 5 attempts per minute per IP"],
       NextSteps: ["Add rate limit headers to responses", "Create admin endpoint to reset limits"],
       Branch:    "feature/rate-limiting",
       Author:    AuthorInfo{Name: "Alice", Tool: "claude-code"},
       Decisions: [
           {Description: "Use token bucket", Rationale: "Simpler than sliding window for our scale"},
           {Description: "Store in Redis", Rationale: "Need shared state across instances"},
       ],
       Evidence: [
           {SessionID: "a1b2c3d4-...", FromTurn: 15, ToTurn: 30},
       ],
   }
   ```

3. **Parses author string**: `"Alice:claude-code"` → splits on `:` → `Author.Name = "Alice"`, `Author.Tool = "claude-code"`

4. **Parses decisions**: `"Use token bucket:Simpler than sliding window"` → splits on `:` → `Description` + `Rationale`

5. **Auto-selects session evidence** (if `--from-session` not specified):
   - Queries `db.ListSessions(ctx, SessionFilter{Limit: 1})`
   - Uses the most recent session as evidence

6. **Stores in SQLite**: `db.CreateCommit(ctx, commit)`
   - Inserts full JSON into `data_json` column
   - Denormalizes `commit_id`, `title`, `feature`, `summary` (via `SummaryText()`), `author` (via `AuthorName()`) into indexed columns
   - If commit has `Changes.Symbols`, inserts rows into `commit_symbols` table

7. **Writes JSON file** to `.ctx/commits/<commit-id>.json`:
   ```json
   {
     "schema": "ctx.commit.v1",
     "commit_id": "01957a4b-...",
     "title": "Implement rate limiting for login endpoint",
     "summary": [
       "Added rate limiter middleware",
       "Configured 5 attempts per minute per IP"
     ],
     "feature": "auth",
     "author": { "name": "Alice", "tool": "claude-code" },
     "decisions": [
       { "description": "Use token bucket", "rationale": "Simpler than sliding window for our scale" }
     ],
     "evidence": [
       { "session_id": "a1b2c3d4-...", "from_turn": 15, "to_turn": 30 }
     ],
     "next_steps": [
       "Add rate limit headers to responses",
       "Create admin endpoint to reset limits"
     ],
     "branch": "feature/rate-limiting",
     "created_at": "2024-01-15T10:45:00Z"
   }
   ```

---

## 10. Phase 8: Browse the Commit Log

### User Action: List all commits
```bash
ctx log
```

### Output
```
01957a4b Implement rate limiting for login endpoint [auth] (2024-01-15 10:45)
01957a3c Fix authentication bypass in login handler (2024-01-15 10:30)
```

### User Action: Filter by feature
```bash
ctx log --feature auth
```

### Behind the Curtain (`internal/cli/log.go`)
1. Calls `db.ListCommits(ctx, CommitFilter{Feature: "auth"})`
2. SQLite query filters by `feature` column
3. Displays first 8 chars of commit ID + title + feature tag + date

### User Action: Show full commit detail
```bash
ctx show 01957a4b
```

### Output (pretty-printed JSON)
```json
{
  "schema": "ctx.commit.v1",
  "commit_id": "01957a4b-8e3f-7000-b000-123456789abc",
  "title": "Implement rate limiting for login endpoint",
  "summary": ["Added rate limiter middleware", "Configured 5 attempts per minute per IP"],
  "feature": "auth",
  "author": { "name": "Alice", "tool": "claude-code" },
  ...
}
```

### Behind the Curtain (`internal/cli/show.go`)
1. Calls `db.GetCommit(ctx, commitID)` — looks up by commit_id in SQLite
2. Deserializes full JSON from `data_json` column
3. Pretty-prints with `json.MarshalIndent(commit, "", "  ")`

---

## 11. Phase 9: Link to Git

Link a context commit to a git SHA so you can trace AI context from git history.

### User Action: Link latest context commit to current git commit
```bash
# After running `git commit`
ctx link abc123def456
```

### Output
```
Linked git abc123de -> context commit 01957a4b
```

### User Action: Link a specific context commit
```bash
ctx link abc123def456 --commit 01957a3c-full-uuid-here
```

### Behind the Curtain (`internal/cli/link.go`)

1. If `--commit` flag is empty:
   - Queries `db.ListCommits(ctx, CommitFilter{Limit: 1})` to get the most recent
   - Uses that commit's ID
2. Calls `db.LinkGit(ctx, gitSHA, commitID)`:
   - Inserts into the `git_links` table: `(git_sha, commit_id, created_at)`
   - This creates a bidirectional link: git SHA → context commit

**Typical workflow:**
```bash
# 1. Work with AI, context is captured
# 2. Create a context commit
ctx commit -m "Implemented feature X" --feature auth
# 3. Make your git commit
git add . && git commit -m "feat: implement feature X"
# 4. Link them
ctx link $(git rev-parse HEAD)
```

---

## 12. Phase 10: Drill into Evidence

Open the AI session linked to a context commit's evidence.

### User Action
```bash
ctx open-session 01957a4b-full-uuid
```

### Output
```
Evidence: session a1b2c3d4-... (turns 15-30)

[Turn 15] user_message (user):
  Can you add rate limiting to the login endpoint?

[Turn 16] assistant_message (assistant):
  I'll implement rate limiting using a token bucket algorithm...

[Turn 17] tool_use (assistant):
  Reading file middleware/ratelimit.go
...
```

### Behind the Curtain (`internal/cli/opensession.go`)

1. Calls `db.GetCommit(ctx, commitID)` to get the commit
2. Returns error if commit not found
3. If `commit.Evidence` is empty → prints "No evidence sessions linked to this commit"
4. For each evidence entry:
   - Looks up the session in SQLite to get its `source` (e.g., "claude_code")
   - Falls back to `evidence.Source` field if session not in DB
   - Constructs JSONL path: `.ctx/sessions/<source>/<session-id>.jsonl`
   - Reads events with `reader.ReadRange(fromTurn, toTurn)`
   - Renders the conversation turns

---

## 13. Phase 11: Query Context at Multiple Resolutions

The `ctx context` command provides multi-resolution views of your project's AI context.

### User Action: Feature-level context
```bash
ctx context --feature auth
```

### Output
```
Feature: auth
Status: in_progress
Summary: Authentication and authorization system
Commits: 5

Recent Commits:
  01957a4b Implement rate limiting [auth] (2024-01-15)
  01957a3c Fix authentication bypass (2024-01-15)
  01957a2b Add session management (2024-01-14)
```

### Behind the Curtain (`internal/cli/context.go`)
1. `db.GetOverview(ctx, "", "auth")` — fetches the `FeatureOverview` from SQLite
2. `db.ListCommits(ctx, CommitFilter{Feature: "auth", Limit: 10})` — recent commits
3. Displays both the high-level summary and the commit list

### User Action: Activity log
```bash
ctx context --feature auth --log 20
```

### Output
```
Activity Log (last 20):
  [2024-01-15 10:45] commit: Implement rate limiting (Alice)
  [2024-01-15 10:30] commit: Fix authentication bypass (Alice)
  [2024-01-14 16:00] commit: Add session management (Bob)
```

### Behind the Curtain
- `db.ListActivity(ctx, "", "auth", 20)` queries the `activity_log` table
- Returns `ActivityEntry` structs with timestamp, type, summary, actor

### User Action: Metadata view
```bash
ctx context --metadata
```

### Output
```
Configuration:
{
  "version": 1,
  "recorder_port": 19476,
  "default_client": "claude_code",
  "redaction_level": "standard",
  "server_url": "https://ctxhub.example.com",
  "repo_id": "my-project-123"
}

Capture Status:
{
  "active": true,
  "port": 19476,
  "adapters": ["claude_code"],
  "pid": 12345
}
```

### User Action: Full feature overview (JSON)
```bash
ctx context --feature auth --full
```
Outputs the complete `FeatureOverview` struct as JSON.

---

## 14. Phase 12: Symbol Blame

Trace the AI context history for a specific code symbol.

### User Action
```bash
ctx blame "auth.go#LoginHandler"
```

### Output
```
Context blame for auth.go#LoginHandler:

  01957a4b Implement rate limiting (2024-01-15)
    Evidence: session a1b2c3d4-... turns 15-30
  01957a3c Fix authentication bypass (2024-01-15)
    Evidence: session e5f6g7h8-... turns 1-12
```

### Behind the Curtain (`internal/cli/blame.go`)

1. **Parses argument**: `symbols.ParseBlameArg("auth.go#LoginHandler")` splits on `#`:
   - file = `auth.go`
   - symbol = `LoginHandler`

2. **Encodes symbol key**: `symbols.EncodeSymbolKey("auth.go", "LoginHandler")` → `"auth.go::LoginHandler"`
   - Uses `::` as separator between file and symbol

3. **Queries SQLite**: `db.GetBySymbol(ctx, "auth.go::LoginHandler")`
   - Joins `commit_symbols` table with `commits` table
   - Returns all commits that touched this symbol

4. **Displays results** with commit info and evidence links

**How symbols get indexed:**
When a commit includes `Changes.Symbols` (e.g., `["auth.go::LoginHandler", "auth.go::AuthMiddleware"]`), `db.CreateCommit()` inserts rows into the `commit_symbols` table linking the commit ID to each symbol key.

---

## 15. Phase 13: Pause & Resume Capture

### User Action: Pause capture
```bash
ctx capture pause
```

### Output
```
Capture paused (events will be dropped)
```

### Behind the Curtain
1. Loads `.ctx/capture_state.json`
2. Sets `state.Paused = true`
3. Saves back to disk
4. The recorder daemon checks this flag — when paused, incoming events are silently dropped

### User Action: Resume capture
```bash
ctx capture resume
```

### Output
```
Capture resumed
```

### User Action: Check capture status
```bash
ctx capture status
```

### Output
```
Status: active
Port: 19476
Adapters: [claude_code]
```

### Behind the Curtain
- Reads `.ctx/capture_state.json` and formats the current state
- Possible statuses: `inactive`, `active`, `paused`

---

## 16. Phase 14: Stop Capture

### User Action
```bash
ctx capture off
```

### Output
```
Capture stopped
```

### Behind the Curtain
1. Sets `capture_state.json` → `{"active": false}`
2. Removes `.ctx/recorder.pid`
3. Note: the actual daemon process is stopped via SIGINT/SIGTERM to the `capture on` process

---

## 17. Phase 15: Push & Pull (Server Sync)

### User Action: Push to server
```bash
ctx push
```

### Output
```
Pushed 3 commits
```

### Behind the Curtain (`internal/cli/push.go`)

1. Loads credentials from `.ctx/credentials.json`
2. Loads config to get `RepoID` and `ServerURL`
3. Resolves server URL: credentials → config → error if neither set
4. Creates sync client: `sync.NewClient(serverURL, apiKey)`
5. Calls `db.GetUnsyncedCommits(ctx)` — queries `sync_log` table to find commits not yet pushed
6. For each unsynced commit:
   - `db.GetCommit(ctx, id)` — fetch full commit from SQLite
   - `client.PushCommit(repoID, commit)` — HTTP POST to `POST /v1/repos/{repoId}/commits`
   - `db.MarkSynced(ctx, "commit", id)` — records in `sync_log` that this commit was pushed
7. Reports count of successfully pushed commits

### User Action: Pull from server
```bash
ctx pull
```

### Output
```
Pulled 2 new commits
```

### Behind the Curtain (`internal/cli/pull.go`)

1. Same credential/config loading as push
2. `client.PullCommits(repoID)` — HTTP GET to `GET /v1/repos/{repoId}/commits`
3. For each received commit:
   - Checks if it already exists locally: `db.GetCommit(ctx, c.CommitID)`
   - If not, inserts it: `db.CreateCommit(ctx, c)`
4. Reports count of newly inserted commits

---

## 18. Phase 16: Check Project Status

### User Action
```bash
ctx status
```

### Output
```
Initialized: yes
Server: https://ctxhub.example.com
Remote: origin
Repo ID: my-project-123
Authenticated: yes
Capture: active (port 19476)
Sessions: 15
Commits: 8
```

### Behind the Curtain (`internal/cli/status.go`)

Aggregates data from multiple sources:
1. Checks if `.ctx/` directory exists → "Initialized: yes/no"
2. Reads `config.json` → server URL, remote name, repo ID
3. Reads `credentials.json` → auth status
4. Reads `capture_state.json` → capture status + port
5. Queries SQLite → session count, commit count

---

## 19. Phase 17: Run Codex Tasks

Wraps OpenAI Codex CLI execution and captures its output as a session.

### User Action: Dry run (preview)
```bash
ctx codex exec "Fix the login bug" --dry-run
```

### Output
```
Would run: codex --json "Fix the login bug"
```

### User Action: Actual execution
```bash
ctx codex exec "Fix the login bug"
```

### Output
```
Starting Codex session: 01957a4b-...
Task: Fix the login bug

[task_start] Starting task
[user_message] Fix the login bug
[assistant_message] I'll fix the login bug
[tool_use] Reading file auth.go

Session 01957a4b-...: 4 events captured (started: 2024-01-15T10:30:00Z)
```

### Behind the Curtain (`internal/cli/codexcmd.go`)

1. Generates a UUID v7 session ID
2. Executes `codex --json "<task>"` as a subprocess
3. Pipes stdout through a `bufio.Scanner`, parsing each JSON line:
   ```json
   {"type":"assistant_message","role":"assistant","content":"I'll fix the bug"}
   ```
4. Converts each line into a `SessionEvent`:
   - `Schema = "ctx.session_event.v1"`
   - `Session.Source = "codex"`
   - `Turn` incremented sequentially
   - `Content.Text` = content or message field
5. Events are printed to terminal in real-time

---

## 20. Phase 18: MCP Server for AI Tools

The MCP (Model Context Protocol) server allows AI tools like Claude Code to directly query your context database.

### User Action
```bash
ctx mcp
```

This starts a JSON-RPC 2.0 server over stdio (stdin/stdout).

### Configuring Claude Code to Use MCP

In your Claude Code MCP settings (`.mcp.json` or settings file):
```json
{
  "mcpServers": {
    "ctx": {
      "command": "ctx",
      "args": ["mcp"],
      "cwd": "/path/to/project"
    }
  }
}
```

### Behind the Curtain (`internal/cli/mcp.go`)

1. Opens SQLite database
2. Creates `mcp.NewServer(db, db, sessionsDir)` — the MCP server uses both `CommitStore` and `FeatureStore` interfaces (both implemented by SQLite)
3. Enters a read loop on stdin, parsing JSON-RPC requests
4. Routes requests by method:

**Supported Methods:**

| Method | Description |
|--------|-------------|
| `initialize` | Returns server capabilities |
| `resources/list` | Lists all 8 resource templates |
| `resources/read` | Reads a specific resource by URI |

### MCP Resources

The MCP server exposes 8 resource templates that AI tools can query:

| Resource URI | Description |
|---|---|
| `ctx://commits?feature={feature}` | List commits (optionally by feature) |
| `ctx://commits/{commitId}` | Full commit detail |
| `ctx://sessions/{sessionId}?from=&to=` | Turn-filtered session events |
| `ctx://features/{feature}` | Feature overview |
| `ctx://features/{feature}/activity?limit=` | Activity log |
| `ctx://features` | List all features |
| `ctx://context?level={level}&feature=&limit=` | Multi-resolution context |
| `ctx://blame/{symbolKey}` | Symbol blame |

### Example MCP Interaction

Claude Code sends:
```json
{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"ctx://context?level=feature&feature=auth"}}
```

Response:
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "contents": [{
      "uri": "ctx://context?level=feature&feature=auth",
      "mimeType": "application/json",
      "text": "{\"schema\":\"ctx.feature_overview.v1\",\"feature\":\"auth\",\"summary\":\"Authentication system\",...}"
    }]
  }
}
```

### MCP URI Routing (`internal/mcp/handlers.go`)

The `HandleResourceRead` method parses the URI and dispatches:
- `ctx://commits?...` → `ReadCommitList()`
- `ctx://commits/<id>` → `ReadCommitDetail()`
- `ctx://sessions/<id>` → `ReadSessionSlice()`
- `ctx://features` → `ReadFeatureList()`
- `ctx://features/<f>` → `ReadFeatureOverview()`
- `ctx://features/<f>/activity` → `ReadActivityLog()`
- `ctx://context?level=feature` → `ReadContextLevel()` → `ReadFeatureOverview()`
- `ctx://context?level=log` → `ReadContextLevel()` → lists commits
- `ctx://context?level=metadata` → `ReadContextLevel()` → returns repo metadata
- `ctx://blame/<symbolKey>` → `ReadSymbolBlame()`

---

## 21. Data Model Reference

### Context Commit (`ctx.commit.v1`)
```
┌────────────────────────────────────────┐
│ ContextCommit                          │
├────────────────────────────────────────┤
│ schema: "ctx.commit.v1"               │
│ commit_id: UUID v7                     │
│ title: string (required)              │
│ summary: []string                     │
│ feature: string                       │
│ created_at: timestamp                 │
│ author: {name, tool}                  │
│ decisions: [{description, rationale}] │
│ evidence: [{session_id, from, to}]    │
│ changes: {files: [], symbols: []}     │
│ tags: []string                        │
│ parent_id: string (commit chaining)   │
│ next_steps: []string                  │
│ branch: string                        │
│ repo_id: string                       │
└────────────────────────────────────────┘
```

### Session Event (`ctx.session_event.v1`)
```
┌────────────────────────────────────────┐
│ SessionEvent                           │
├────────────────────────────────────────┤
│ schema: "ctx.session_event.v1"        │
│ event_id: UUID                        │
│ ts: timestamp                         │
│ session: {id, source, repo}           │
│ type: string                          │
│ turn: int                             │
│ actor: {role, model, tool}            │
│ content: {text, refs: [{path, type}]} │
└────────────────────────────────────────┘
```

### Feature Overview (`ctx.feature_overview.v1`)
```
┌────────────────────────────────────────┐
│ FeatureOverview                        │
├────────────────────────────────────────┤
│ schema: "ctx.feature_overview.v1"     │
│ repo_id: string                       │
│ feature: string                       │
│ summary: string                       │
│ status: string                        │
│ commit_ids: []string                  │
│ updated_at: timestamp                 │
└────────────────────────────────────────┘
```

---

## 22. File Structure Reference

After a project is initialized and actively used:

```
myproject/
├── .ctx/
│   ├── config.json              ← Project configuration
│   ├── credentials.json         ← API keys (gitignored)
│   ├── index.sqlite             ← SQLite index database
│   ├── blobs.db                 ← BoltDB for large content
│   ├── capture_state.json       ← Recorder daemon state
│   ├── recorder.pid             ← PID of running recorder
│   ├── hooks.json               ← Generated client hooks
│   ├── sessions/
│   │   ├── claude_code/
│   │   │   ├── <session-id>.jsonl   ← Raw session events
│   │   │   └── ...
│   │   └── codex/
│   │       └── <session-id>.jsonl
│   ├── commits/
│   │   ├── <commit-id>.json     ← Commit JSON snapshots
│   │   └── ...
│   └── blobs/
│       └── <hash>               ← Content-addressed blobs
├── src/
│   └── ...
└── .gitignore                   ← Should include .ctx/credentials.json
```

---

## 23. Server API Reference

The CtxHub server (`ctxhub`) provides these REST API endpoints, all under `/v1/` and protected by API key authentication:

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/v1/repos/:repoId/commits` | Create a context commit |
| `GET` | `/v1/repos/:repoId/commits` | List commits |
| `GET` | `/v1/repos/:repoId/commits/:id` | Get commit by ID |
| `GET` | `/v1/repos/:repoId/features/:feature/commits` | List commits by feature |
| `GET` | `/v1/repos/:repoId/features` | List all features |
| `GET` | `/v1/repos/:repoId/features/:feature/overview` | Get feature overview |
| `PUT` | `/v1/repos/:repoId/features/:feature/overview` | Update feature overview |
| `GET` | `/v1/repos/:repoId/context?level=` | Multi-resolution context |
| `POST` | `/v1/repos/:repoId/git-links` | Create git-to-context link |
| `GET` | `/v1/repos/:repoId/git/:gitSha/related` | Get related commits by git SHA |
| `GET` | `/v1/repos/:repoId/symbols/:symbolKey/blame` | Symbol blame |
| `POST` | `/v1/repos/:repoId/sessions` | Create a session |
| `GET` | `/v1/repos/:repoId/sessions/:id` | Get session |
| `PUT` | `/v1/repos/:repoId/sessions/:id/chunks/:chunkId` | Upload session chunk |
| `GET` | `/v1/repos/:repoId/sessions/:id/slice` | Get session slice |

---

## 24. MCP Resource Reference

| URI Template | Name | Priority | Description |
|---|---|---|---|
| `ctx://commits?feature={feature}` | Context Commit List | 0.6 | List commits, filterable |
| `ctx://commits/{commitId}` | Context Commit Detail | 0.8 | Full commit JSON |
| `ctx://sessions/{sessionId}?from={from}&to={to}` | Session Slice | 0.4 | Turn-filtered events |
| `ctx://features/{feature}` | Feature Overview | 0.6 | Feature summary + status |
| `ctx://features/{feature}/activity?limit={limit}` | Activity Log | 0.4 | Recent activity |
| `ctx://features` | Feature List | 0.5 | All features |
| `ctx://context?level={level}&feature={f}&limit={n}` | Context Level | 0.7 | Multi-resolution context |
| `ctx://blame/{symbolKey}` | Symbol Blame | 0.6 | Symbol history |

**Context levels** for `ctx://context`:
- `level=feature` — Feature overview (or feature list if no feature specified)
- `level=log` — Commit activity log for a feature
- `level=metadata` — Repository metadata

---

## Quick Reference: Complete Command List

```bash
# Initialization
ctx init                          # Initialize .ctx directory

# Configuration
ctx config set <key> <value>      # Set a config value
ctx config get [key]              # Get config value(s)
ctx remote add <name> <url>       # Add remote server
ctx remote ls                     # List remotes
ctx auth login [--api-key KEY]    # Authenticate
ctx auth status                   # Check auth

# Capture Control
ctx capture on [--client NAME]    # Start recording (foreground daemon)
ctx capture off                   # Stop recording
ctx capture pause                 # Pause (drop events)
ctx capture resume                # Resume recording
ctx capture status                # Show capture state

# Session Browsing
ctx session ls [--feature NAME]   # List sessions
ctx session show <id> [--turns N-M]  # Show session events
ctx session tail <id>             # Show last 10 events

# Context Commits
ctx commit -m "title"             # Create commit
  [--feature NAME]                #   Feature tag
  [--summary "point"]             #   Summary bullet (repeatable)
  [--decision "desc:rationale"]   #   Decision (repeatable)
  [--next-step "todo"]            #   Next step (repeatable)
  [--author "name:tool"]          #   Author info
  [--branch "branch-name"]        #   Branch name
  [--from-session ID]             #   Evidence session
  [--turns N-M]                   #   Evidence turn range

# Browsing & Querying
ctx log [--feature NAME]          # List commits
ctx show <commit-id>              # Show commit detail
ctx link <git-sha> [--commit ID]  # Link to git
ctx open-session <commit-id>      # Drill into evidence
ctx blame <file#symbol>           # Symbol blame
ctx context --feature NAME        # Feature context
  [--log N]                       #   Activity log
  [--metadata]                    #   Repo metadata
  [--full]                        #   Full JSON

# Server Sync
ctx push                          # Push to server
ctx pull                          # Pull from server
ctx status                        # Project overview

# Integrations
ctx mcp                           # Start MCP server (stdio)
ctx codex exec "<task>"           # Run Codex task
  [--dry-run]                     #   Preview only
```
