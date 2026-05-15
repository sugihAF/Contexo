# Contexo

**GitHub for AI agent knowledge, scoped per project.**

When one developer researches a topic deeply with their AI agent, the resulting decisions, rejected alternatives, and dead-ends live only in their session. Contexo lets the team share that distilled knowledge so every teammate's agent starts from the same baseline — without re-deriving it (or deriving it worse).

## How it works

Each project gets a `.contexo/` directory containing markdown pages (the same layered structure as `llm-wiki`: `raw/sessions/`, `wiki/concepts/`, `wiki/entities/`, `wiki/analyses/`, plus an always-loaded `index.md`). Pages have YAML frontmatter with author, agent, tags, and a `reasoning_summary`.

`ctx push` uploads selected pages to a Contexo server. `ctx pull` brings them down. The server is a git repository under the hood — every push is a real commit with author attribution, you get history and timeline for free, and concurrent writes that diverge return a 409 with both versions for merge.

Agents drive sync from natural language via MCP tools: when the user says *"sync my Stripe knowledge to contexthub"*, the agent invokes `ctx_push(feature="stripe")` directly.

## The flow

```
Dev A's Claude  →  writes wiki/concepts/stripe-subscription.md
                   (including "## Agent Reasoning" — what was considered & rejected)
                ↘
                  ctx push --feature stripe   →   Contexo server (git-backed)
                                                  Dev A authored sha abc123
                                                ↗
Dev B's Claude  ←  ctx pull                ←
                ↓
                  MCP: reads ctx://index, then ctx://wiki/stripe-subscription
                  Sees Dev A's reasoning. Doesn't repeat the dead-ends.
```

## CLI

```
ctx init                  Create .contexo/ in the current project
ctx remote set <url>      Point at a Contexo server
ctx remote set-repo <id>  Set the repo ID on that server
ctx auth login --api-key K --name "..." --email "..."
ctx push [--feature X] [--tag Y] [--type concept|entity|source|analysis] [--dry-run]
ctx pull [--full]
ctx status                Local vs server delta
ctx log                   Server timeline (who changed what when)
ctx mcp                   Start MCP server for the local agent
```

## MCP

Resources (read-only):
- `ctx://index` — always-loaded knowledge index
- `ctx://tags` — tag → page mapping
- `ctx://wiki/{slug}` — a concept/entity/analysis page
- `ctx://raw/{session-id}` — a raw session under `raw/sessions/`
- `ctx://search?q=&type=&tag=` — substring + tag/type filter

Tools (agent-invokable):
- `ctx_write_page` — write a knowledge page with frontmatter
- `ctx_push`, `ctx_pull`, `ctx_status` — sync against the team server

## Install

Pure Go, no CGO. Works on Linux, macOS, and Windows.

**Easiest (any platform):**

```bash
go install github.com/sugihAF/Contexo/cmd/ctx@latest
# binary goes to $GOPATH/bin (typically ~/go/bin on Linux/macOS, %USERPROFILE%\go\bin on Windows)
# make sure that directory is on PATH
```

**From source, into a local `bin/` dir:**

```bash
# Linux / macOS
go build -o bin/ctx ./cmd/ctx
go build -o bin/contexo-server ./cmd/contexo-server

# Windows (PowerShell)
go build -o bin\ctx.exe .\cmd\ctx
go build -o bin\contexo-server.exe .\cmd\contexo-server
```

The `ctx` CLI runs anywhere. The `contexo-server` binary is what the team's Contexo instance runs — typically via the Docker setup in `docker/`.

**Run the server (host install):**

```bash
# Linux / macOS
CONTEXO_DATA_ROOT=/var/contexo/repos PORT=8080 ./bin/contexo-server

# Windows (PowerShell)
$env:CONTEXO_DATA_ROOT="C:\contexo\repos"; $env:PORT="8080"; .\bin\contexo-server.exe
```

The server shells out to `git`, so `git` must be on PATH. The Docker image (`docker/Dockerfile`) bundles it.

## Documentation

- [`docs/usage.md`](docs/usage.md) — **start here** — admin setup + developer onboarding + daily flow
- [`docs/vision.md`](docs/vision.md) — what Contexo is and the page format
- [`docs/migration.md`](docs/migration.md) — file-by-file map from the previous MVP-1 architecture
- [`docs/mvp-build-sequence.md`](docs/mvp-build-sequence.md) — 11-step build sequence with acceptance criteria
