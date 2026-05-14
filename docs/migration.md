# Contexo — Migration From Current Code

This is the file-by-file map from the current MVP-1 codebase (JSONL recorder + SQLite commit store) to the vision in [vision.md](./vision.md): per-project `.contexo/` directories with markdown pages, git-backed server, layered MCP reads, agent-triggered sync.

Roughly half the current Go code goes away — the recorder, redaction, blob, and JSONL adapter layers exist to handle raw transcripts that the new design doesn't capture. What remains is the CLI scaffolding, the HTTP sync skeleton, auth, and the MCP server — all of which gets refactored to handle markdown pages instead of JSON commits.

---

## Summary buckets

| Bucket | Approximate file count | Notes |
|---|---|---|
| **Delete** | ~12 files | Recorder daemon, redaction pipeline, JSONL/boltdb stores, capture adapters |
| **Refactor heavily** | ~20 files | Schema, CLI commands, MCP server, sync client, server router |
| **Keep mostly as-is** | ~5 files | Auth, config, CLI root scaffolding, cmd entrypoints |
| **Add new** | ~8 files | Page schema, frontmatter parser, git-backed store, MCP tool handlers |

---

## Per-directory plan

### `internal/recorder/` — **DELETE entire directory**

| File | Action |
|---|---|
| `http.go` | DELETE |
| `recorder.go` | DELETE |

Rationale: raw transcript capture is no longer in scope. AI writes distilled markdown directly to `.contexo/` at session end (same instruction model as the global `llm-wiki`).

---

### `internal/redaction/` — **DELETE entire directory**

| File | Action |
|---|---|
| `denylist.go` | DELETE |
| `patterns.go` | DELETE |
| `pipeline.go` | DELETE |

Rationale: redaction was needed because we captured raw conversations including potential secrets. With distilled markdown written by the agent, secrets never enter the corpus in the first place — the agent is instructed not to write them.

---

### `internal/adapter/` — **DELETE most**

| Path | Action |
|---|---|
| `claudecode/hooks.go` | DELETE (no Claude Code hooks; agent writes markdown directly) |
| `claudecode/normalize.go` | DELETE |
| `codex/wrapper.go` | DELETE |
| `codex/normalize.go` | DELETE |
| `vscode/importer.go` | DELETE |

Rationale: adapters existed to normalize raw events from different IDE/CLI sources. Distilled markdown is source-agnostic; the agent writes it the same way regardless of which IDE it runs in.

---

### `internal/store/` — **MIXED**

| Path | Action | Notes |
|---|---|---|
| `interfaces.go` | REFACTOR | Replace `CommitStore` / `EventStore` / `BlobStore` with `PageStore` interface |
| `sqlite/` | REFACTOR | Becomes a local-side metadata index over `.contexo/` files (slug → path, last-modified, tags) for fast lookup; no longer the source of truth |
| `postgres/` | REFACTOR | Server-side: tracks page metadata, authorship, and API-key → user mapping. The pages themselves live in git. |
| `s3/` | KEEP, defer | Useful eventually for large attachments referenced by pages. Not needed for MVP. |
| `boltdb/` | DELETE | Blob storage not needed |
| `jsonl/` | DELETE | No JSONL transcripts |

---

### `internal/schema/` — **REFACTOR**

| File | Action | Notes |
|---|---|---|
| `commit.go` | DELETE | Replaced by `page.go` |
| `session_event.go` | DELETE | No event stream |
| `feature.go` | REFACTOR | Becomes part of `page.go` (a feature is just a tag-filter view over pages) |
| `policy.go` | DEFER | Drop until ACL features come back |
| `validate.go` | REFACTOR | Validates frontmatter against `ctx.page.v1` schema |

**Add:**

| File | Purpose |
|---|---|
| `page.go` | `Page` struct with frontmatter fields (schema, slug, type, author, agent, created, updated, parent_sha, sources, related, tags, reasoning_summary) plus markdown body |
| `frontmatter.go` | YAML frontmatter parse/serialize with body separation |

---

### `internal/cli/` — **REFACTOR most, DELETE some**

| File | Action | Notes |
|---|---|---|
| `root.go` | REFACTOR | Trim down to new command set (below) |
| `init.go` | REFACTOR | Creates `.contexo/` with seed `index.md`, `tags.md`, `wiki/`, `raw/sessions/` |
| `push.go` | REFACTOR | Walks `.contexo/`, filters by `--feature`/`--tag`/`--glob`, uploads pages with `parent_sha`, handles 409 conflicts |
| `pull.go` | REFACTOR | Fetches pages changed since last pull, writes into `.contexo/`, updates local index |
| `remote.go` | KEEP | Already good |
| `auth.go` | KEEP | Already good |
| `status.go` | REFACTOR | Shows local pages modified since last push, server pages newer than local |
| `configcmd.go` | KEEP | Already good |
| `mcp.go` | REFACTOR | Starts MCP server with new resource layout (index/page/raw) + new tools (push/pull) |
| `log.go` | REFACTOR | Shows server timeline (`git log`) of who changed what when |
| `show.go` | REFACTOR | Shows a single page (`ctx show stripe-subscription`) |
| `context.go` | REFACTOR | Returns layered view: index always, then concepts matching feature/tag |
| `commit.go` | DELETE | Replaced by `ctx capture` (writes a session page) — or removed entirely if AI writes directly |
| `capture.go` | REFACTOR | Optionally: `ctx capture` creates a `raw/sessions/<date>-<topic>.md` skeleton for the agent to fill |
| `session.go` | DELETE | No session listing — sessions are just markdown files visible via `ls` |
| `blame.go` | REFACTOR | Grep across markdown pages for symbol references; report which pages mention `file.go#Symbol` |
| `link.go` | DELETE | Git SHA linking handled in page frontmatter (`related:` or `tags: [commit-abc123]`) |
| `codexcmd.go` | DELETE | Codex wrapper not needed |
| `opensession.go` | REFACTOR or DELETE | If kept, opens the `raw/sessions/<id>.md` linked from a concept's `sources:` field |

**Final CLI surface:**
```
ctx init                  Initialize .contexo/ in current project
ctx remote add <url>      Configure Contexo server
ctx auth login            Authenticate with API key
ctx status                Show local vs server delta
ctx push [filters]        Upload pages to server
ctx pull [filters]        Download pages from server
ctx show <slug>           Print a page
ctx log [--limit N]       Server timeline
ctx blame <file#symbol>   Find pages mentioning a symbol
ctx mcp                   Start MCP server for local agent
ctx config get|set
```

---

### `internal/mcp/` — **REFACTOR**

| File | Action |
|---|---|
| `server.go` | REFACTOR resource templates to new layout (see below) |
| `handlers.go` | REFACTOR — read from `.contexo/` filesystem instead of stores |

**New resource templates:**
```
ctx://index                          Always-loaded index (replaces feature-list)
ctx://wiki/{slug}                    Concept or entity page by slug
ctx://raw/{session-id}               Raw session markdown
ctx://search?q=...&type=...&tag=...  Grep across pages
ctx://history/{slug}                 Edit history (server-fetched on demand)
```

**Add: MCP tools** (new file `internal/mcp/tools.go`)

Tools are invokable by the agent, not just readable resources:

```
ctx_push(feature?, tags?, glob?, dry_run?)   Push subset of local pages
ctx_pull(feature?, since?)                    Pull changed pages
ctx_status()                                  What's local-unpushed vs server-newer
ctx_write_page(slug, type, content)           Agent writes a page directly
```

This is the genuinely new code that makes "sync my Stripe knowledge to contexo" work via natural language.

---

### `internal/server/` — **REFACTOR**

| Path | Action |
|---|---|
| `router.go` | REFACTOR — new route surface (below) |
| `handler/repos.go` | KEEP, light refactor — manages repo creation (which is also `git init` server-side) |
| `handler/commits.go` | DELETE |
| `handler/sessions.go` | DELETE |
| `handler/features.go` | REFACTOR → `pages.go` for page CRUD |
| `handler/gitlinks.go` | DELETE |
| `handler/blame.go` | REFACTOR or DELETE |
| `service/memstore.go` | DELETE |
| `service/service.go` | REFACTOR — wraps the git-backed store |
| `middleware/` | KEEP |

**New server endpoints:**
```
POST   /v1/repos/:id                        Create repo (git init server-side)
GET    /v1/repos/:id/pages                  List pages (with filters)
GET    /v1/repos/:id/pages/*path            Read a page (and its current sha)
POST   /v1/repos/:id/sync/push              Bulk push pages with parent_sha check
GET    /v1/repos/:id/sync/pull?since=<sha>  Pull changes since a sha
GET    /v1/repos/:id/timeline?limit=N       git log across all pages
GET    /v1/repos/:id/pages/*path/history    git log -- <path>
```

**Add: server-side git driver** (new file `internal/server/gitstore/gitstore.go`)

Wraps `os/exec` calls to `git` (or uses `go-git` if we want pure Go) to:
- Init bare-ish repo per `repo_id`
- Read file content at HEAD
- Write file + commit with author + return new sha
- Detect non-fast-forward push (parent_sha != HEAD when page changed)
- `git log` for timeline

---

### `cmd/` — **REFACTOR ENTRYPOINTS**

| File | Action |
|---|---|
| `ctx/main.go` | KEEP — just calls `cli.NewRootCmd().Execute()` |
| `cmd/contexo-server/main.go` | REFACTOR — wire git-backed store + postgres metadata, drop `service.NewMemStore()` |

---

### `internal/auth/` — **KEEP**

Bearer-key middleware is fine. Add one thing: API key → user identity (name + email) lookup, so server-side commits get attributed correctly.

---

### `internal/sync/` — **REFACTOR**

| File | Action |
|---|---|
| `client.go` | REFACTOR — new HTTP shape: `PushPages(repoID, []Page) (newSha, []Conflict, error)`, `PullPages(repoID, sinceSha) ([]Page, newSha, error)` |

---

### `internal/symbols/` — **DEFER**

`ctx blame` becomes a grep over markdown pages. The current symbol package can be deleted; reimplement as a simple text walker in `internal/cli/blame.go` when needed.

---

### `internal/config/` — **KEEP, small refactor**

`.ctx/config.json` becomes `.contexo/config.json`. Add fields: `repo_id`, `server_url`, `last_pull_sha`.

---

### `migrations/` — **REPLACE**

`001_initial.sql` had the commit/session/blob tables. New migration creates the server-side metadata tables: `repos`, `api_keys`, `users`, `repo_users`. (Page content itself lives in git, not Postgres.)

---

### `tests/` — **REWRITE**

All 28 story tests target the old schema. They go away. New tests organized around the new acceptance criteria (see [mvp-build-sequence.md](./mvp-build-sequence.md)).

---

### `docker/` — **KEEP, small refactor**

`docker-compose.yml` needs the server's git data volume mounted somewhere persistent. Drop the MinIO service (not needed for MVP).

---

## What stays exactly as-is

- `internal/auth/` — bearer-key middleware
- `internal/config/` — config + credentials loading (rename `.ctx/` → `.contexo/`)
- `cmd/ctx/main.go` and `cmd/contexo-server/main.go` shells (their bodies change)
- The general Cobra CLI scaffolding pattern in `internal/cli/root.go`
- Gin router pattern in `internal/server/router.go`

---

## Code that needs to be NEW (not in current repo)

| File | Purpose |
|---|---|
| `internal/schema/page.go` | The `Page` struct |
| `internal/schema/frontmatter.go` | YAML frontmatter parse/serialize |
| `internal/store/pagestore/local.go` | Read/write `.contexo/` filesystem |
| `internal/server/gitstore/gitstore.go` | Server-side git operations |
| `internal/mcp/tools.go` | MCP tool handlers (push/pull/status/write_page) |
| `internal/indexer/indexer.go` | Builds/maintains `index.md` and `tags.md` from page frontmatter |

---

## Risk notes

- **Git as a server-side store has scale limits.** Fine for projects with hundreds-to-thousands of pages. If a repo gets to 100k pages we'd want to revisit. Not an MVP concern.
- **YAML frontmatter parsing edge cases.** Use a well-tested library (`gopkg.in/yaml.v3`) and validate strictly on read — bad frontmatter rejects with a clear error rather than corrupting the corpus.
- **The CLI / MCP surface stays small intentionally.** Every command added has to justify itself against "could the agent just read the markdown files?" — that bar kills most feature requests.
