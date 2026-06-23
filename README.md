<p align="center">
  <img src="https://raw.githubusercontent.com/sugihAF/Contexo/main/.github/assets/contexo-hero.svg" alt="Contexo — git-like AI Agent context versioning and synchronization" width="100%">
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green" alt="License: MIT"></a>
  <a href="https://github.com/sugihAF/Contexo/releases/latest"><img src="https://img.shields.io/github/v/release/sugihAF/Contexo?color=blue" alt="Latest release"></a>
  <a href="https://contexo.live"><img src="https://img.shields.io/badge/website-contexo.live-FF7A00" alt="Website: contexo.live"></a>
  <a href="https://x.com/Contexo_live"><img src="https://img.shields.io/badge/follow-%40Contexo__live-000000?logo=x&logoColor=white" alt="Follow @Contexo_live on X"></a>
</p>

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
                [--type=...] [--json] [--blame]versions (defaults to parent..head)
ctx diff <slug> --local                        Diff your local copy vs server HEAD
                                               (what `ctx push` would change)
ctx evolution <slug> [--limit=N] [--show-diff] Full trajectory in one call: every
                     [--type=...] [--json]     commit + its per-commit diff
                     [--blame]
```

`--blame` annotates each section with the commit that originally introduced
its heading (works on both `ctx diff` and `ctx evolution`) — useful for
"who wrote this section?" questions.

**Agent integration**

```
ctx mcp                        Start MCP server for the local agent
ctx hooks install|uninstall|status   Manage the Claude Code Stop hook
ctx capture status             Show pending capture buffers
```

**Maintenance**

```
ctx update [--check]           Self-update to the latest release (--check: report only)
ctx version [--short]          Print the installed version
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
- `ctx_evolution` — full trajectory of a page (every commit + diff) in one call,
  for when you want the whole story before making any edit

When the agent reads `ctx://wiki/<slug>` and the page has changed on the server
since the last `ctx pull`, the response is prefixed with a `<DRIFT_NOTICE>`
block summarizing what's new — the agent learns about drift before it edits
without having to remember to check. Set `CONTEXO_DRIFT_DISABLE=1` to turn
the check off; `ctx status` also lists drifted pages (pass `--no-drift` to skip).

If `ctx_push` hits a 409 (someone else pushed first), the tool returns a
`<MERGE_REQUIRED>` directive carrying the ancestor + your + server versions
and a list of conflicting sections. The agent writes a reconciled version
via `ctx_write_page` and re-invokes `ctx_push` — the local sync state is
auto-updated so the re-push won't 409 for the same reason.

## Install

Prebuilt binaries for Linux, macOS, and Windows (amd64 + arm64). **No Go toolchain required.**

**Easiest (one line, handles PATH for you):**

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.sh | sh

# Windows (PowerShell)
iwr -useb https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.ps1 | iex
```

These download a checksum-verified binary from the latest [GitHub Release](https://github.com/sugihAF/Contexo/releases), drop it in a user-writable dir (`~/.local/bin`, or `%LOCALAPPDATA%\Programs\contexo` on Windows), and add that dir to your PATH if needed. Idempotent — safe to re-run.

**Manual (download a binary yourself):** grab the archive for your platform from the [Releases page](https://github.com/sugihAF/Contexo/releases/latest), verify it against `checksums.txt`, extract `ctx`, and put it on your PATH.

**With Go (if you already have the toolchain):**

```bash
go install github.com/sugihAF/contexo/cmd/ctx@latest
```

> A `go install` build reports its version as `dev` and can't self-update — reinstall with the script (or `go install` again) to upgrade.

## Updating

```bash
ctx update           # download + install the latest release in place
ctx update --check   # just report whether a newer version exists
ctx version          # show the installed version
```

`ctx update` verifies the new binary's checksum before swapping it in. On interactive commands, `ctx` also prints a one-line note when a newer version is available — set `CONTEXO_NO_UPDATE_CHECK=1` to silence it.

**Build from source (server + CLI):**

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

**The CLI itself** — delete the binary the installer placed:

```bash
# macOS / Linux
rm ~/.local/bin/ctx

# Windows (PowerShell)
Remove-Item "$env:LOCALAPPDATA\Programs\contexo\ctx.exe"

# installed with `go install` instead? remove it from there:
rm "$(go env GOPATH)/bin/ctx"      # ctx.exe on Windows
```

## Documentation

- [`docs/usage.md`](docs/usage.md) — **start here** — admin setup + developer onboarding + daily flow
- [`docs/vision.md`](docs/vision.md) — what Contexo is and the page format
- [`docs/migration.md`](docs/migration.md) — file-by-file map from the previous MVP-1 architecture
- [`docs/mvp-build-sequence.md`](docs/mvp-build-sequence.md) — 11-step build sequence with acceptance criteria
