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

**Setup**

```
ctx init                       Create .contexo/, write .mcp.json, install Stop hook
ctx detach [--keep-knowledge]  Reverse `ctx init` (purges .contexo/ by default)
ctx login                      Browser flow: sign in once, mint a token, done
                               (alias for `ctx auth login`; --no-browser to paste)
ctx join <invite-key>          Join an existing repo with a ctxi_… invite key
ctx remote set <url>           Point at a Contexo server
ctx remote set-repo [<id>]     Set the repo ID (interactive picker if omitted)
ctx remote get                 Show current server + repo
```

**Sync**

```
ctx push [--feature X] [--tag Y] [--type concept|entity|source|analysis] [--dry-run]
         [--yes] [--show-diff] [--no-preview]
ctx pull [--full]
ctx status                     Local vs server delta
ctx log                        Server timeline (who changed what when)
```

Before pushing, `ctx push` previews each file as `[NEW]`, `[EDIT]`, or `[SAME]`
and shows a per-section summary of what your push will change on the server.
If any `[EDIT]` rows appear, it asks for confirmation. Pass `--yes` to skip
the prompt (required for non-interactive use), `--show-diff` to see the full
per-section diff inline, or `--no-preview` to skip the round-trip entirely.

**Inspect a page's evolution**

```
ctx history <slug> [--type=...] [--limit=N]    Commit timeline for one page
ctx diff <slug> [--from=<sha>] [--to=<sha>]    Section-aware diff between two
                [--type=...] [--json]          versions (defaults to parent..head)
```

**Agent integration**

```
ctx mcp                        Start MCP server for the local agent
ctx hooks install|uninstall|status   Manage the Claude Code Stop hook
ctx capture status             Show pending capture buffers
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
- `ctx_history`, `ctx_diff` — see how a page evolved before editing it
  (structured, section-aware diff rather than line-based git diff)

## Install

Pure Go, no CGO. Works on Linux, macOS, and Windows.

**Easiest (one line, handles PATH for you):**

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.sh | sh

# Windows (PowerShell)
iwr -useb https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.ps1 | iex
```

Both scripts require **Go 1.25+** already installed. They run `go install`, detect where the binary landed (`$GOPATH/bin` or `$GOBIN`), and append that directory to your shell's PATH if it's not already there. Idempotent — safe to re-run.

**Manual (control PATH yourself):**

```bash
go install github.com/sugihAF/Contexo/cmd/ctx@latest
# binary goes to $GOPATH/bin (typically ~/go/bin on Linux/macOS, %USERPROFILE%\go\bin on Windows)
# add that directory to PATH yourself, then open a new terminal
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

## Uninstall

**Per project** — remove Contexo from a specific project:

```bash
ctx detach                   # prompts before purging .contexo/, .mcp.json entry, .gitignore line, Stop hook
ctx detach --keep-knowledge  # remove the integration but preserve .contexo/
ctx detach -y                # skip the confirmation
```

`ctx detach` warns if local pages haven't been pushed yet so you can `ctx push` first.

**The CLI itself:**

```bash
# Linux / macOS / Windows
rm "$(go env GOPATH)/bin/ctx"      # ctx.exe on Windows
```

## Documentation

- [`docs/usage.md`](docs/usage.md) — **start here** — admin setup + developer onboarding + daily flow
- [`docs/vision.md`](docs/vision.md) — what Contexo is and the page format
- [`docs/migration.md`](docs/migration.md) — file-by-file map from the previous MVP-1 architecture
- [`docs/mvp-build-sequence.md`](docs/mvp-build-sequence.md) — 11-step build sequence with acceptance criteria
