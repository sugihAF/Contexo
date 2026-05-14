# Getting Started: How to Build, Run, and Use CtxHub

A practical step-by-step guide to get CtxHub running on your machine.

---

## Prerequisites

| Tool | Version | Why |
|------|---------|-----|
| **Go** | 1.23+ | Compiles the CLI and server |
| **GCC / C compiler** | Any | Required by `modernc.org/sqlite` (CGO) |
| **Docker** + **Docker Compose** | Latest | For running the server with PostgreSQL and MinIO |
| **Git** | Any | For version control integration |
| **curl** | Any | Used by capture hooks to send events |

### Installing Go (if not installed)

**Windows:**
```powershell
# Download from https://go.dev/dl/
# Or with winget:
winget install GoLang.Go
```

**macOS:**
```bash
brew install go
```

**Linux:**
```bash
# Ubuntu/Debian
sudo apt install golang-go

# Or download from https://go.dev/dl/
wget https://go.dev/dl/go1.23.6.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.23.6.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

### Installing GCC (Windows-specific)

On Windows, CGO needs a C compiler. Install one of:

```powershell
# Option 1: MSYS2 (recommended)
winget install MSYS2.MSYS2
# Then in MSYS2 terminal:
pacman -S mingw-w64-x86_64-gcc

# Option 2: TDM-GCC
# Download from https://jmeubank.github.io/tdm-gcc/

# Add to PATH (for MSYS2):
# C:\msys64\mingw64\bin
```

Verify: `gcc --version` should work from your terminal.

---

## Step 1: Build

```bash
cd /path/to/Contexo

# Download dependencies
go mod tidy

# Build both binaries
make build

# This produces:
#   bin/ctx       <- CLI tool
#   bin/ctxhub    <- Server
```

If `make` isn't available (Windows without Make):
```bash
go build -o bin/ctx ./cmd/ctx
go build -o bin/ctxhub ./cmd/ctxhub
```

**On Windows**, output will be `bin/ctx.exe` and `bin/ctxhub.exe`:
```powershell
go build -o bin\ctx.exe ./cmd/ctx
go build -o bin\ctxhub.exe ./cmd/ctxhub
```

Add to your PATH:
```bash
# Linux/macOS - add to ~/.bashrc or ~/.zshrc
export PATH="/path/to/Contexo/bin:$PATH"

# Windows PowerShell - add to profile
$env:PATH = "D:\Codes\Contexo\bin;$env:PATH"
```

Verify:
```bash
ctx --help
```

Expected output:
```
ctx captures, stores, and retrieves AI-assisted development context.

Usage:
  ctx [command]

Available Commands:
  init        Initialize .ctx directory structure
  capture     Control event capture
  session     Browse captured sessions
  commit      Create a context commit
  log         List context commits
  show        Show context commit detail
  link        Link a context commit to a git SHA
  context     Show multi-resolution context
  blame       Show context history for a symbol
  push        Push unsynced commits to server
  pull        Pull new commits from server
  mcp         Run MCP server over stdio
  remote      Manage remote servers
  auth        Authentication management
  status      Show project context status overview
  config      Manage project configuration
  open-session Open the evidence session for a context commit
  codex       Codex integration commands
  ...
```

---

## Step 2: Run Tests

```bash
# Run all tests
make test

# Run a specific story
make test-story-22

# Or directly with go test
go test ./tests/ -v
go test ./tests/ -run TestStory22 -v
go test ./... -v
```

---

## Step 3: Initialize a Project

Navigate to any project you want to track:

```bash
cd ~/my-project    # or any project directory
ctx init
```

Output:
```
Initialized .ctx in /home/user/my-project
```

This creates:
```
my-project/
└── .ctx/
    ├── config.json       # {"version":1,"recorder_port":19476,"default_client":"claude_code","redaction_level":"standard"}
    ├── index.sqlite      # SQLite database (sessions, commits, events tables)
    ├── blobs.db          # BoltDB for large content
    ├── sessions/         # JSONL event files will go here
    ├── commits/          # Commit JSON snapshots
    └── blobs/            # Content-addressed blob files
```

Check it worked:
```bash
ctx status
```

Output:
```
Initialized: yes
Authenticated: no
Capture: inactive
Sessions: 0
Commits: 0
```

---

## Step 4: Start Capturing AI Sessions

### Option A: Capture Claude Code sessions

**Terminal 1** — Start the recorder daemon:
```bash
cd ~/my-project
ctx capture on --client claude-code
```

Output:
```
Capture started on port 19476 (client: claude_code)
Listening at http://127.0.0.1:19476
```

This stays running in the foreground. It:
- Starts an HTTP server on port 19476
- Generates `.ctx/hooks.json` for Claude Code
- Waits for events

**Terminal 2** — Configure Claude Code to use the hooks:

Copy the generated hooks into your Claude Code settings. The hooks file at `.ctx/hooks.json` looks like:
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

To use it, copy this content into your Claude Code hooks configuration file (usually `~/.claude/hooks.json` or per-project `.claude/hooks.json`).

Now use Claude Code normally — every prompt and response gets captured automatically.

### Option B: Test with manual events

You can manually POST events to verify capture is working:

```bash
curl -X POST http://127.0.0.1:19476/event \
  -H "Content-Type: application/json" \
  -d '{
    "schema": "ctx.session_event.v1",
    "event_id": "test-001",
    "ts": "2024-01-15T10:00:00Z",
    "session": {"id": "test-session-1", "source": "manual"},
    "type": "user_message",
    "turn": 1,
    "actor": {"role": "user"},
    "content": {"text": "Hello, fix the login bug"}
  }'
```

Response: `{"status":"ok"}`

Send a second event (assistant response):
```bash
curl -X POST http://127.0.0.1:19476/event \
  -H "Content-Type: application/json" \
  -d '{
    "schema": "ctx.session_event.v1",
    "event_id": "test-002",
    "ts": "2024-01-15T10:01:00Z",
    "session": {"id": "test-session-1", "source": "manual"},
    "type": "assistant_message",
    "turn": 2,
    "actor": {"role": "assistant", "model": "claude-3.5-sonnet"},
    "content": {"text": "I will look at the login handler and fix the bug."}
  }'
```

Check capture status:
```bash
ctx capture status
```

Output:
```
Status: active
Port: 19476
Adapters: [claude-code]
```

---

## Step 5: Browse Captured Sessions

```bash
# List all sessions
ctx session ls
```

Output:
```
ID                                     SOURCE          STARTED                  EVENTS
test-session-1                         manual          2024-01-15 10:00:00      2
```

```bash
# Show full session conversation
ctx session show test-session-1
```

Output:
```
Session: test-session-1 (source: manual)
Started: 2024-01-15 10:00:00

[Turn 1] user_message (user):
  Hello, fix the login bug

[Turn 2] assistant_message (assistant):
  I will look at the login handler and fix the bug.
```

```bash
# Show only specific turns
ctx session show test-session-1 --turns 1-1

# Show last 10 events
ctx session tail test-session-1
```

---

## Step 6: Create a Context Commit

After an AI session, create a structured commit summarizing what happened:

```bash
# Simple commit
ctx commit -m "Fixed login authentication bypass"

# Rich commit with full metadata
ctx commit \
  -m "Fixed login authentication bypass" \
  --feature auth \
  --summary "Found SQL injection in login query" \
  --summary "Replaced string concat with parameterized query" \
  --decision "Use parameterized queries:Prevents all SQL injection variants" \
  --next-step "Add input validation layer" \
  --next-step "Write regression tests" \
  --author "Alice:claude-code" \
  --branch "fix/login-bypass" \
  --from-session test-session-1 \
  --turns 1-2
```

Output:
```
Created context commit: 01957a4b-8e3f-7000-b000-123456789abc
  Title: Fixed login authentication bypass
```

---

## Step 7: Browse Commits

```bash
# List all commits
ctx log
```

Output:
```
01957a4b Fixed login authentication bypass [auth] (2024-01-15 10:05)
```

```bash
# Filter by feature
ctx log --feature auth

# Show full commit detail
ctx show 01957a4b-8e3f-7000-b000-123456789abc
```

---

## Step 8: Link Context to Git

After making a regular git commit:

```bash
git add -A
git commit -m "fix: login authentication bypass"

# Link the git commit to the most recent context commit
ctx link $(git rev-parse HEAD)
```

Output:
```
Linked git a1b2c3d4 -> context commit 01957a4b
```

Or link to a specific context commit:
```bash
ctx link $(git rev-parse HEAD) --commit 01957a4b-full-uuid-here
```

---

## Step 9: Drill Into Evidence

See the original AI conversation that led to a commit:

```bash
ctx open-session 01957a4b-8e3f-7000-b000-123456789abc
```

Output:
```
Evidence: session test-session-1 (turns 1-2)

[Turn 1] user_message (user):
  Hello, fix the login bug

[Turn 2] assistant_message (assistant):
  I will look at the login handler and fix the bug.
```

---

## Step 10: Query Context

```bash
# Feature-level overview
ctx context --feature auth

# With activity log
ctx context --feature auth --log 10

# Repo metadata
ctx context --metadata

# Full JSON output
ctx context --feature auth --full
```

---

## Step 11: Symbol Blame

If your commits track changed symbols:

```bash
ctx blame "auth.go#LoginHandler"
```

---

## Step 12: Stop Capture

```bash
# In the terminal running the recorder, press Ctrl+C
# Or from another terminal:
ctx capture off
```

---

## Running the Server (Team Sharing)

The server enables team-wide context sharing. There are two ways to run it:

### Option A: Docker Compose (Recommended)

```bash
cd docker/
docker compose up -d
```

This starts:
- **PostgreSQL** on port 5432 (user: `ctxhub`, pass: `ctxhub`)
- **MinIO** (S3-compatible) on port 9000 (console: 9001)
- **CtxHub server** on port 8080

The default API key is `dev-key`.

Verify:
```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

### Option B: Run server directly

```bash
# Set environment variables
export PORT=8080
export CTXHUB_API_KEY=my-secret-key

# Run (uses in-memory store for MVP)
./bin/ctxhub
```

Output:
```
[GIN-debug] Listening and serving HTTP on :8080
```

### Connect CLI to Server

```bash
cd ~/my-project

# Add the server as a remote
ctx remote add origin http://localhost:8080

# Authenticate
ctx auth login --api-key dev-key

# Set repo ID
ctx config set repo_id my-project

# Check connection
ctx auth status
```

Output:
```
Authenticated: yes
Server: http://localhost:8080
API Key: dev-...key
```

### Push and Pull

```bash
# Push local commits to server
ctx push

# Pull commits from server (e.g., teammate's commits)
ctx pull
```

### Server API Examples

```bash
# List commits via API
curl -H "Authorization: Bearer dev-key" \
  http://localhost:8080/v1/repos/my-project/commits

# Get a specific commit
curl -H "Authorization: Bearer dev-key" \
  http://localhost:8080/v1/repos/my-project/commits/01957a4b-...

# List features
curl -H "Authorization: Bearer dev-key" \
  http://localhost:8080/v1/repos/my-project/features

# Create a git link
curl -X POST -H "Authorization: Bearer dev-key" \
  -H "Content-Type: application/json" \
  -d '{"git_sha":"abc123","commit_id":"01957a4b-..."}' \
  http://localhost:8080/v1/repos/my-project/git-links

# Look up context by git SHA
curl -H "Authorization: Bearer dev-key" \
  http://localhost:8080/v1/repos/my-project/git/abc123/related

# Symbol blame
curl -H "Authorization: Bearer dev-key" \
  http://localhost:8080/v1/repos/my-project/symbols/auth.go::LoginHandler/blame

# Multi-resolution context
curl -H "Authorization: Bearer dev-key" \
  "http://localhost:8080/v1/repos/my-project/context?level=feature&feature=auth"
```

---

## Setting Up MCP for Claude Code

The MCP server lets Claude Code directly query your context database during conversations.

### Configure MCP

Create or edit `.mcp.json` in your project root (or in Claude Code settings):

```json
{
  "mcpServers": {
    "ctx": {
      "command": "ctx",
      "args": ["mcp"],
      "cwd": "/path/to/my-project"
    }
  }
}
```

Or if `ctx` is not on PATH, use the full path:
```json
{
  "mcpServers": {
    "ctx": {
      "command": "/path/to/Contexo/bin/ctx",
      "args": ["mcp"],
      "cwd": "/path/to/my-project"
    }
  }
}
```

### What Claude Code Can Now Do

With MCP configured, Claude Code can read these resources:

```
ctx://commits                          # List all context commits
ctx://commits?feature=auth             # Commits for a feature
ctx://commits/01957a4b-...             # Full commit detail
ctx://sessions/test-session-1?from=1&to=5  # Session events
ctx://features                         # List all features
ctx://features/auth                    # Feature overview
ctx://features/auth/activity?limit=10  # Activity log
ctx://context?level=feature&feature=auth  # Multi-resolution context
ctx://context?level=log&feature=auth   # Commit log
ctx://blame/auth.go::LoginHandler      # Symbol blame
```

This means Claude Code can automatically look up "what decisions were made about auth" or "what happened in the last session" without you having to explain it.

---

## Typical Daily Workflow

```bash
# Morning: Start capture
ctx capture on --client claude-code &

# Work with Claude Code on a feature...
# Events are captured automatically via hooks

# After a productive session: Commit the context
ctx commit \
  -m "Implemented user registration API" \
  --feature user-mgmt \
  --summary "Created POST /api/register endpoint" \
  --summary "Added email validation and password hashing" \
  --decision "bcrypt for passwords:Industry standard, configurable cost" \
  --next-step "Add email verification flow" \
  --author "$(whoami):claude-code"

# Make the git commit
git add -A && git commit -m "feat: user registration API"

# Link them
ctx link $(git rev-parse HEAD)

# Push context to team server
ctx push

# Check overall status
ctx status

# End of day: Stop capture
ctx capture off
```

---

## Troubleshooting

### Build fails with "gcc not found"
Install a C compiler. On Windows use MSYS2 or TDM-GCC. On macOS: `xcode-select --install`. On Linux: `apt install build-essential`.

### "ctx: command not found"
Add `bin/` to your PATH: `export PATH="/path/to/Contexo/bin:$PATH"`

### Capture not receiving events
1. Check the recorder is running: `ctx capture status`
2. Test with curl: `curl http://127.0.0.1:19476/health` should return `{"status":"healthy"}`
3. Verify hooks.json was generated: `cat .ctx/hooks.json`
4. Make sure Claude Code is configured to use the hooks

### "push: no credentials"
Run `ctx auth login --api-key <your-key>` first.

### "push: no server URL configured"
Either:
- `ctx remote add origin http://localhost:8080`
- Or `ctx config set server_url http://localhost:8080`

### Port already in use
Change the recorder port: `ctx config set recorder_port 19477`

### SQLite "database is locked"
Only one process should write to SQLite at a time. Make sure you don't have two `ctx capture on` instances running.
