# Contexo — MVP Build Sequence

A vertical-slice ordering. The goal is to hit the **acceptance criteria** below by end of day 2 with a working two-developer workflow, then iterate.

Each step has a single visible checkpoint that proves it works before the next step starts. No "build the whole thing, then test" — incremental, end-to-end at every stage.

---

## Acceptance criteria (the MVP target)

A teammate (Developer B) on a different machine can read a concept page that Developer A wrote and synced, including the `## Agent Reasoning` section, via Developer B's Claude session. Concretely:

1. Dev A: `ctx init` in `chompchat/`, AI writes `wiki/concepts/stripe-subscription.md`, `ctx push --feature stripe`.
2. Dev B: separate `chompchat/` checkout, `ctx remote add <url>` + `ctx auth login`, `ctx pull --feature stripe`. File appears locally.
3. Dev B's Claude: lists `ctx://index` → sees the new page → reads `ctx://wiki/stripe-subscription` → sees the reasoning section.
4. Server's `git log` for `wiki/concepts/stripe-subscription.md` shows Dev A as author with a real timestamp.
5. If Dev B edits the same page and pushes while Dev A pushes a different change first, Dev B's push returns 409 with both versions.

Everything beyond this is post-MVP polish.

---

## Day 1 — local end-to-end (no server yet)

Build the local half: schema, file I/O, MCP serving from local `.contexo/`. By end of day, an agent can read a page it wrote via MCP. No sync, no server.

### Step 1.1 — Schema (1 hour)

**Build:**
- `internal/schema/page.go` — `Page` struct
- `internal/schema/frontmatter.go` — parse/serialize YAML frontmatter + markdown body

**Checkpoint:** roundtrip test. Read this fixture, serialize it back, byte-equal:
```markdown
---
schema: ctx.page.v1
slug: stripe-subscription
type: concept
author: sugihAF
agent: claude-opus-4-7
created: 2026-05-14T10:00:00Z
updated: 2026-05-14T10:00:00Z
parent_sha: ""
sources: []
related: []
tags: [stripe, billing]
reasoning_summary: "Test"
---
# Body
Hello.
```

### Step 1.2 — Local pagestore (1 hour)

**Build:**
- `internal/store/pagestore/local.go` — `Write(page)`, `Read(slug)`, `List(filter)`, `Walk(.contexo/)`

**Checkpoint:** write a page to a temp dir, read it back, confirm frontmatter and body match.

### Step 1.3 — `ctx init` (30 min)

**Refactor:** `internal/cli/init.go` to create:
```
.contexo/
  config.json
  index.md            (empty index with header)
  tags.md             (empty tags with header)
  wiki/concepts/      (empty)
  wiki/entities/      (empty)
  raw/sessions/       (empty)
```

**Checkpoint:** `ctx init` in an empty dir creates the tree; running it again is idempotent.

### Step 1.4 — Indexer (1 hour)

**Build:** `internal/indexer/indexer.go` — walks `.contexo/wiki/` and `.contexo/raw/`, regenerates `index.md` and `tags.md`.

**Checkpoint:** hand-write two pages into `wiki/concepts/`, run the indexer, see them in `index.md` with correct one-line summaries and in `tags.md` under each tag.

### Step 1.5 — MCP server reads local pages (2 hours)

**Refactor:** `internal/mcp/server.go` + `handlers.go`:
- `ctx://index` → returns contents of `index.md`
- `ctx://wiki/{slug}` → returns the page
- `ctx://raw/{session-id}` → returns the session
- `ctx://search?q=` → grep over the corpus

**Refactor:** `internal/cli/mcp.go` to start the MCP server pointed at the current `.contexo/`.

**Checkpoint:** start the MCP server, hit it with a test client (or `curl` if we use HTTP transport), confirm reading the index and a specific page works.

### Step 1.6 — Agent writes a page end-to-end (30 min, no code)

In a Claude Code session, instruct the agent: *"Look at the .contexo/ structure. Now write a concept page for 'Stripe Subscription' at `.contexo/wiki/concepts/stripe-subscription.md` with proper frontmatter including the Agent Reasoning section."*

**Checkpoint:** the agent produces a well-formed page. Run the indexer; the page appears in `index.md`. Read via MCP works.

---

## Day 2 — server + sync

Build the server-side half: git-backed page CRUD, push/pull, conflict detection. By end of day, two developers can share a page.

### Step 2.1 — Git-backed store (2 hours)

**Build:** `internal/server/gitstore/gitstore.go`:
- `Init(repoID)` — `git init` a new repo dir
- `Write(repoID, path, content, author, parent_sha) (newSha, error)` — writes the file, `git commit` with author, returns new HEAD. If `parent_sha` doesn't match current HEAD of that file, returns a typed `ErrConflict` with current content.
- `Read(repoID, path) (content, currentSha, error)`
- `Log(repoID, since?) ([]Commit, error)` — `git log`
- `LogPath(repoID, path) ([]Commit, error)` — `git log -- path`

Use `os/exec` for simplicity. Switch to `go-git` later if needed.

**Checkpoint:** unit tests on a temp dir: init, write, read, write-with-correct-parent (succeeds), write-with-stale-parent (returns conflict).

### Step 2.2 — Server endpoints (2 hours)

**Refactor:** `internal/server/router.go` + `handler/pages.go`:
```
POST   /v1/repos/:id
GET    /v1/repos/:id/pages
GET    /v1/repos/:id/pages/*path
POST   /v1/repos/:id/sync/push
GET    /v1/repos/:id/sync/pull?since=<sha>
GET    /v1/repos/:id/timeline?limit=50
```

**Refactor:** `cmd/contexo-server/main.go` to wire `gitstore` (not memstore), with a configurable data root (`/var/contexo/repos/` or env var).

**Checkpoint:** start the server, `curl` create a repo, push a page, pull it back, see it in `/timeline`.

### Step 2.3 — `ctx push` and `ctx pull` (2 hours)

**Refactor:**
- `internal/sync/client.go` — new `PushPages` / `PullPages` shapes carrying `parent_sha`
- `internal/cli/push.go` — walks `.contexo/`, filters by `--feature`/`--tag`/`--glob`, sends, handles conflicts (for MVP: print conflict and abort; AI-assisted merge comes later)
- `internal/cli/pull.go` — fetches changed pages since `last_pull_sha`, writes to disk, re-runs indexer, updates `last_pull_sha` in config

**Checkpoint:** two local clones (e.g. `/tmp/devA/chompchat/`, `/tmp/devB/chompchat/`), both pointed at the same local server. Push from A, pull from B, see the page appear.

### Step 2.4 — `ctx status` and `ctx log` (1 hour)

**Refactor:** `internal/cli/status.go` and `internal/cli/log.go` to hit the new endpoints.

**Checkpoint:** `ctx status` shows local files modified since last push; `ctx log` shows the server's timeline with authors.

### Step 2.5 — MCP push/pull tools (1.5 hours)

**Build:** `internal/mcp/tools.go`:
- `ctx_push(feature?, tags?, glob?, dry_run?)`
- `ctx_pull(feature?, since?)`
- `ctx_status()`

Wire these in the MCP server alongside the resources.

**Checkpoint:** from a Claude session, say *"push my stripe knowledge to contexo"*. Confirm the agent calls `ctx_push(feature="stripe")` and the file appears on the server.

### Step 2.6 — Acceptance test (1.5 hours)

Run the full vertical-slice scenario from the **Acceptance criteria** section above. Two separate working directories, two MCP servers, one Contexo server. Walk through all 5 criteria. Fix anything that breaks.

**Checkpoint:** all 5 acceptance criteria pass.

---

## Day 3+ — defer until acceptance passes

These are all valuable but none of them block the MVP target. Tackle in order of demand once the basic flow is real.

| Item | Why deferred |
|---|---|
| AI-assisted conflict merge | Manual conflict resolution is fine while corpus is small |
| `ctx blame <file#symbol>` | Grep works; formalize only if heavily used |
| Page history (`ctx history <slug>`) | `git log` on the server side already works; CLI exposure can wait |
| Postgres-backed metadata (vs in-server-memory) | Filesystem + git is enough for single-server MVP |
| Encryption at rest | API-key auth + HTTPS is the MVP boundary |
| Search UI | Agent + grep is the UI |
| Web dashboard | Out of scope |
| Branch/experiment management on pages | Out of scope |
| Recorder daemon resurrection (for "auto-capture" safety net) | Only if writing-by-agent proves unreliable in practice |

---

## Concrete first commit

Before any of the steps above, commit the current state of the repository so we have a baseline. The repo has a single "Initial commit" with everything else untracked — that's a bad starting point for a refactor.

```
git add docs/vision.md docs/migration.md docs/mvp-build-sequence.md
git commit -m "docs: vision, migration plan, MVP build sequence"

# Then a second commit with the existing MVP-1 work as the starting baseline,
# so the upcoming deletions show up in diffs clearly:
git add cmd/ internal/ tests/ migrations/ docker/ go.mod go.sum Makefile e2e_test/ ctxhub_docs/ ralph-loop-implementation.md docs/getting-started.md docs/end-to-end-usage-guide.md
git commit -m "checkpoint: MVP-1 (JSONL recorder + SQLite commit store) before refactor"
```

After this, every refactor step is a clean, reviewable diff.

---

## Verification approach

No big-bang test suite rewrite up front. Each step has its checkpoint above; pass the checkpoint, move on. Once the acceptance scenario runs end-to-end, *then* write durable Go tests for the pieces that are stable enough to be worth testing:

- Schema roundtrip
- Pagestore read/write
- Gitstore conflict detection
- HTTP push/pull contract

Skip writing tests for the CLI plumbing — those are better verified by re-running the acceptance scenario.

---

## Definition of done for this whole effort

- Acceptance criteria pass on two real machines (not just two dirs on one machine)
- One end-to-end "shared with a teammate" knowledge flow proven on a real project (e.g. share a Stripe page from one of my own working sessions to a colleague's checkout)
- `docs/vision.md`, `docs/migration.md`, this file all committed and current
- A short README update at the repo root pointing to the new docs and giving a 30-second pitch
