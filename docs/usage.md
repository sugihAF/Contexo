# Contexo — Usage Walkthrough

Concrete end-to-end flow from zero. Two roles:

- **Admin** (one-time setup): runs the Contexo server and invites developers (Google sign-in, invite keys, or — for simple instances — a legacy shared key).
- **Developer** (per dev, per project): installs the CLI, points it at the server, signs in, and wires it into their AI agent.

Then the daily flow: Dev A researches with their agent, Dev A shares to the team, Dev B picks up the work, Dev B's agent sees Dev A's reasoning.

> Most people don't self-host. The hosted server lives at `https://api.contexo.live` with the dashboard at `https://contexo-web.pages.dev`, and `ctx login` targets it by default. If that's you, skip Part 1 and start at Part 2.

---

## Part 1 — Server setup (one-time, by admin)

Only needed if you're self-hosting. Using hosted Contexo? Skip to Part 2.

### 1.1 Get the binaries

The **CLI** (`ctx`) installs as a prebuilt binary — no Go toolchain (see the README's Install section):

```
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.sh | sh
# Windows (PowerShell)
iwr -useb https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.ps1 | iex
```

The **server** (`contexo-server`) runs two ways: with Docker (**§1.2**, recommended) or from a binary you build yourself (**§1.3**). To build from source:

```
git clone https://github.com/sugihAF/contexo
cd contexo
go build -o bin/contexo-server ./cmd/contexo-server
go build -o bin/ctx ./cmd/ctx        # optional — the install script is easier
```

Pure Go, no CGO. The server shells out to `git` for the per-repo storage, so `git` must be on PATH (the Docker image bundles it).

### 1.2 Run the server with Docker (recommended)

The repo ships a single-container setup in [`docker/`](../docker): a multi-stage build (Go → Alpine) that bundles `git`, runs as a non-root user, and ships a `/health` healthcheck.

```
cd docker
cp .env.example .env
# edit .env — at minimum set CONTEXO_API_KEY to a real secret
docker compose up -d
docker compose logs -f
```

Health check:

```
curl http://localhost:8080/health
# {"status":"ok"}
```

`docker compose` reads these from `.env` (only `CONTEXO_API_KEY` is required — compose refuses to start without it):

| `.env` key | Purpose | Default |
|---|---|---|
| `CONTEXO_API_KEY` | **Required.** Legacy shared key; rotate away from `dev-key`. | — |
| `CONTEXO_SESSION_SECRET` | Persistent session-signing secret (`openssl rand -hex 32`). Unset → ephemeral per boot. | unset |
| `GOOGLE_OAUTH_CLIENT_ID` | Enables Google sign-in from the dashboard. | unset |
| `CONTEXO_CORS_ORIGINS` | Comma-separated dashboard origins allowed by CORS. | `http://localhost:5173,http://localhost:3000` |
| `CTXHUB_PORT` | Host port to publish (the container always listens on 8080). | `8080` |

Knowledge and metadata persist in the named volume `contexo_data` (mounted at `/data` — i.e. `CONTEXO_DATA_ROOT` inside the container); it survives rebuilds.

**Update** to a newer version:

```
git pull
docker compose build --no-cache
docker compose up -d        # the /data volume persists
```

The [`docker/README.md`](../docker/README.md) has the volume back-up recipe and a production nginx/TLS server block.

### 1.3 Run the server from a binary

Minimal:

```
mkdir -p /var/contexo/data
CONTEXO_DATA_ROOT=/var/contexo/data \
PORT=8080 \
./bin/contexo-server
```

Health check:

```
curl http://localhost:8080/health
# {"status":"ok"}
```

The server reads these environment variables:

| Variable | Purpose | Default |
|---|---|---|
| `CONTEXO_DATA_ROOT` | Where the per-project git repos and the SQLite DB live | `./contexo-data` |
| `PORT` | HTTP listen port | `8080` |
| `CONTEXO_SESSION_SECRET` | HMAC secret that signs session tokens. **Set this in production** — when unset the server generates a new random secret each boot, so sessions don't survive a restart. | random per boot |
| `GOOGLE_OAUTH_CLIENT_ID` | Enables Google sign-in (`POST /v1/auth/google`). Unset → that endpoint returns 503 and only token / legacy-key auth works. | unset |
| `CONTEXO_API_KEY` | Legacy shared API key (back-compat). Anyone presenting it authenticates as a single `legacy:admin` identity — no per-user attribution. | `dev-key` |
| `CONTEXO_LISTEN_ADDR` | Explicit bind address. Set `127.0.0.1:8080` to bind loopback behind a reverse proxy. | `:$PORT` |
| `CONTEXO_CORS_ORIGINS` | Comma-separated dashboard origins allowed by CORS (browser clients only). | `http://localhost:5173,http://localhost:3000` |

For a quick private/test instance, `CONTEXO_DATA_ROOT` + `PORT` + a strong `CONTEXO_API_KEY` is enough — developers authenticate with that one key (the "legacy" path in 2.3). For real per-user identity and attribution, set `CONTEXO_SESSION_SECRET` and `GOOGLE_OAUTH_CLIENT_ID`, and point a dashboard at this server.

### 1.4 What state lives where

- `CONTEXO_DATA_ROOT/<repo_id>/` — one git repository per project. Real `git log` works in there; the git history is the source of truth for **knowledge pages**.
- `CONTEXO_DATA_ROOT/contexo.db` — a SQLite database holding everything that *isn't* a knowledge page: **users**, **personal access tokens**, **repo members + roles**, **invite keys**, and the **activity feed**.

Back-ups: snapshot the whole data root (`rsync -a /var/contexo/data/ <backup-target>/`) — that captures both the git repos and `contexo.db` in one shot. Under Docker the data root is the `contexo_data` volume (mounted at `/data`); back it up with the `docker run … tar` recipe in [`docker/README.md`](../docker/README.md).

### 1.5 Production deployment

Put the server behind a reverse proxy that terminates HTTPS (Caddy or nginx), bind the app to loopback, and set a persistent session secret:

```
CONTEXO_DATA_ROOT=/var/contexo/data \
CONTEXO_LISTEN_ADDR=127.0.0.1:8080 \
CONTEXO_SESSION_SECRET=$(openssl rand -hex 32) \
GOOGLE_OAUTH_CLIENT_ID=<your-oauth-client-id> \
./bin/contexo-server
```

nginx sketch (Caddy is similar and auto-issues the cert):

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $remote_addr;
}
```

A systemd unit, a small VM (1 CPU, 2 GB RAM is plenty for hundreds of pages and dozens of devs), and you're done.

### 1.6 Invite developers

Two ways to onboard a developer onto a repo:

- **Google sign-in** (requires `GOOGLE_OAUTH_CLIENT_ID`): the developer opens the dashboard, signs in with Google, and mints a personal access token from the Settings page. That gives them an identity; they join a specific repo by redeeming an invite key (below).
- **Invite key**: an owner mints a repo invite key and shares it with the developer:

```
$ ctx invite mint --label "alice laptop"
ctxi_8f2c…           # send this to the developer (revocable; see `ctx invite list` / `revoke`)
```

  The developer redeems it with `ctx join ctxi_…` (see 2.3).

Either way, tell each developer the **server URL** (`https://contexo.yourcompany.com`) and the **repo ID** for each project (e.g. `chompchat`, `acme-api`).

For a simple self-hosted or test instance you can skip identity entirely and hand out the **legacy `CONTEXO_API_KEY`** — everyone then shares one `legacy:admin` identity. Fine for a personal server; not for per-author attribution across a team.

---

## Part 2 — Developer onboarding (per developer, per project)

### 2.1 Get the CLI

Prebuilt binary, no Go required:

```
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.sh | sh
# Windows (PowerShell)
iwr -useb https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.ps1 | iex
```

(Already have the Go toolchain? `go install github.com/sugihAF/contexo/cmd/ctx@latest` works too — but a `go install` build reports its version as `dev` and can't self-update.)

Keep it current with `ctx update` (or `ctx update --check` to just look).

### 2.2 Initialize the project

In your existing project directory (e.g. `~/code/chompchat`):

```
$ cd ~/code/chompchat
$ ctx init
Initialized .contexo in /home/sugih/code/chompchat
```

`ctx init` creates the `.contexo/` tree, wires the Contexo MCP server into Claude Code (`.mcp.json`) and — when Cursor is detected — into Cursor (`.cursor/mcp.json`), and installs the per-turn capture hook for Claude Code (and Codex/Cursor, when detected). If Codex is detected it also prints how to wire its MCP server (`ctx mcp install --tool=codex`, which writes the global `~/.codex/config.toml`):

```
.contexo/
├── config.json          ← server URL, repo_id, dashboard URL, last-pull marker
├── credentials.json     ← auth token + identity (written by `ctx login`; chmod 600, git-ignored)
├── index.md             ← always loaded into the AI's context
├── tags.md
├── raw/sessions/
└── wiki/
    ├── analyses/
    ├── concepts/
    └── entities/
```

`credentials.json` holds your token and should never be committed — `ctx init` adds it to `.gitignore`. To reverse everything `ctx init` did, run `ctx detach` (`--keep-knowledge` preserves `.contexo/`).

### 2.3 Point at the server and sign in

**Hosted Contexo** (`api.contexo.live`) is the default, so just sign in:

```
$ ctx login
Opening https://contexo-web.pages.dev in your browser…
Signed in as sugihAF <sugih@yourcompany.com>
```

`ctx login` opens the dashboard, you sign in with Google, and a freshly-minted personal access token (`ctxp_…`) is copied back to the CLI over a loopback redirect — no copy-pasting. Your name and email come from the Google session and become the git author on every push.

**Self-hosted server** — point at it first (or pass `--server` / `--dashboard` to `ctx login`):

```
$ ctx remote set https://contexo.yourcompany.com
$ ctx login --dashboard https://dash.yourcompany.com
```

**No browser** (CI, headless, remote box) — paste a token instead:

```
$ ctx login --no-browser
Token: ctxp_…              # a PAT minted from the dashboard's Settings page
```

…or pass it directly with `ctx login --token ctxp_…`.

**Legacy shared key** (simple self-hosted instances) — still supported, deprecated:

```
$ ctx login --server https://contexo.yourcompany.com \
    --api-key "$CONTEXO_API_KEY" --name "sugihAF" --email "sugih@yourcompany.com"
```

Then select the repo — redeem an invite key, or pick one you already have access to:

```
$ ctx join ctxi_8f2c…       # if an owner gave you an invite key
Joined repo chompchat (role: member)

# — or —
$ ctx remote set-repo chompchat    # omit the id for an interactive picker
```

Verify:

```
$ ctx status
Initialized: yes
Server: https://api.contexo.live
Repo: chompchat
Authenticated: yes
User: sugihAF <sugih@yourcompany.com>
Local pages: 0
Last pull: (never)
Pages never pushed: 0
```

### 2.4 Wire it into your AI agent

`ctx init` wires the project-local agents for you: **Claude Code** (`.mcp.json`) always, and **Cursor** (`.cursor/mcp.json`) when Cursor is detected. The agent runs `ctx mcp` in the project directory and gets Contexo's **eight tools** and **five resources** over MCP.

Wire (or re-wire) any agent explicitly with `ctx mcp install`:

```
ctx mcp install --tool=cursor    # ./.cursor/mcp.json (project-local)
ctx mcp install --tool=codex     # ~/.codex/config.toml (GLOBAL — prompts first)
ctx mcp install --tool=all       # claude + cursor (+ codex if installed)
ctx mcp status                   # show what's wired
ctx mcp guide                    # how to wire Windsurf, OpenCode, Hermes, OpenClaw, ...
```

`ctx init` prints this same integration table plus a per-tool setup guide at the
end; `ctx mcp guide` reprints the guide any time.

**Codex** uses a global config, so it's never wired silently: `ctx mcp install --tool=codex` runs `codex mcp add contexo -- ctx mcp` after a confirmation prompt. Because that entry is global, `ctx mcp` launched outside a Contexo project serves a dormant, zero-tool server instead of erroring.

To register by hand instead (e.g. Windsurf, or `~/.claude.json`), add an `mcpServers` entry that runs `ctx mcp`:

```json
{
  "mcpServers": {
    "contexo": { "command": "ctx", "args": ["mcp"] }
  }
}
```

The tools the agent sees are `ctx_write_page`, `ctx_push`, `ctx_pull`, `ctx_status`, `ctx_history`, `ctx_diff`, `ctx_evolution`, and `ctx_capture_session`; the resources are `ctx://index`, `ctx://tags`, `ctx://wiki/{slug}`, `ctx://raw/{session-id}`, and `ctx://search?q=&type=&tag=`. (Full descriptions in the README's MCP section.)

### 2.5 Pull what's already there

If teammates have already pushed knowledge for this project, grab it:

```
$ ctx pull
Pulled 12 page(s); HEAD=a4b2c1d3
```

Now `.contexo/wiki/...` is populated and `.contexo/index.md` is regenerated. Open the project in your IDE — your AI agent has everything the team knows.

---

## Part 3 — The daily flow

The scenario: you (Dev A) spend a Tuesday afternoon researching how to model Stripe subscription billing for ChompChat. On Wednesday morning Dev B is going to do the implementation. Without Contexo, Dev B's Claude would re-do the research from scratch, possibly badly. With Contexo, Dev B's Claude inherits your reasoning.

### 3.1 Dev A — research session with the agent

You work normally with Claude Code in `~/code/chompchat/`. You discuss Stripe billing approaches, evaluate Connect vs. Billing, dig into the docs, reject some options. The conversation runs for an hour.

### 3.2 Dev A — share with the team (natural language)

Near the end of the session, you tell Claude:

> *"Distill this session into a Contexo concept page and sync it to contexo. Include an Agent Reasoning section explaining what we considered and rejected."*

Claude invokes `ctx_write_page` with the slug `stripe-subscription`, type `concept`, your distilled body (Decision + Agent Reasoning + Current State + Open Questions), tags `[stripe, billing, subscription]`, and a `reasoning_summary` like *"Rejected Connect (negative-balance ownership); chose Billing + metered usage"*.

Then Claude invokes `ctx_push` with `feature: stripe`. Server returns the new commit sha.

You can verify:

```
$ ctx log
1a320e1d  2026-05-14 16:42  sugihAF — agent push (1 pages)
```

(or `ctx activity` for the fuller push/pull timeline). Or do the push manually if you prefer not to trust the agent with it:

```
$ ctx push --feature stripe --dry-run
Would push 1 page(s):
  wiki/concepts/stripe-subscription.md  (parent=)

$ ctx push --feature stripe -m "Stripe subscription research"
Pushed 1 page(s); HEAD=1a320e1d
```

### 3.3 Dev B — start the implementation

Wednesday morning. Dev B opens `~/code/chompchat/` for the first time today. Their `.contexo/` is from a few days ago. They tell their Claude:

> *"I'm picking up the Stripe subscription implementation. What does the team already know?"*

Claude invokes `ctx_pull` (which it knows to do from the tool's description — *"Call this at the start of a session when picking up work on a topic"*). The new page lands in Dev B's `.contexo/wiki/concepts/stripe-subscription.md`. The index regenerates.

Or Dev B just runs:

```
$ ctx pull
Pulled 1 page(s); HEAD=1a320e1d
```

### 3.4 Dev B's agent reads the context

Claude reads `ctx://index` (always loaded), sees the new entry:

```
## Concepts
- [Stripe Subscription](wiki/concepts/stripe-subscription.md) —
  Rejected Connect (negative-balance ownership); chose Billing + metered usage
  | tags: stripe,billing,subscription | sugihAF 2026-05-14
```

Then reads `ctx://wiki/stripe-subscription`, sees:

```markdown
## Decision
Stripe Billing with metered voice minutes. Not Connect.

## Agent Reasoning
- Considered Stripe Connect with destination charges → rejected: gives
  restaurants the negative balance
- Path of inquiry: Connect docs → fee-split confusion → realized model
  mismatch → pivoted to Billing
- What I didn't try: Lago, Orb — flagged in Open Questions

## Open Questions
- Idempotency keys for usage records?
```

Dev B's Claude now starts implementation from this baseline. No re-deriving. No accidentally pivoting back to Connect. If it has questions about why Connect was rejected, the answer is right there.

### 3.5 Dev B contributes back

As Dev B implements, they learn something new — say, that Stripe's `usage_record_summaries` endpoint has a 1-hour cache that affects how often they should pull metrics. They tell Claude:

> *"Update the stripe-subscription page with what we learned about usage_record_summaries caching."*

Claude reads the page, edits the body, and calls `ctx_push`. The server returns a new commit attributed to Dev B. Dev A pulls later and sees Dev B's addition.

---

## Part 4 — Conflict resolution

If two people edit the same page from the same starting version and both push, the second push is rejected — the server never silently overwrites the first:

```
$ ctx push --feature stripe
1 conflict(s):
  wiki/concepts/stripe-subscription.md: current=af84a79f expected_parent=1a320e1d
Resolve by running 'ctx pull', merging the conflicting pages, then 'ctx push' again.
```

When the **agent** drives the push, the `ctx_push` tool instead returns a `<MERGE_REQUIRED>` directive carrying three versions — the common ancestor, your local edit, and the server's current page — plus the list of conflicting sections. The agent writes a reconciled page with `ctx_write_page` and re-invokes `ctx_push`; the local sync state is updated so the re-push won't 409 for the same reason.

By hand it's the same shape:

```
$ ctx pull --full
```

…brings down the server's version, then you reconcile the two — or ask your agent to:

> *"The pulled stripe-subscription page diverged from mine. Read both versions and merge them coherently — keep both authors' insights."*

…then `ctx push` again. For markdown prose, an agent that understands each section's *intent* merges better than git's line-based diff.

> Heads-up before you even get here: when the agent reads `ctx://wiki/<slug>` and the page changed on the server since your last pull, the response is prefixed with a `<DRIFT_NOTICE>` summarizing what's new — so it usually learns about divergence *before* editing. `ctx status` lists drifted pages too (`CONTEXO_DRIFT_DISABLE=1` turns the check off).

---

## Part 5 — Troubleshooting

| Symptom | Fix |
|---|---|
| Agent shows `contexo` with no tools (or `mcp: open store …`) | You're outside a Contexo project — `ctx mcp` runs dormant there. Run `ctx init` in the project root. |
| `push: no credentials, run 'ctx login' first` | `ctx login` (or `ctx login --token ctxp_…`). |
| `push: no server URL configured` | `ctx remote set <url>` (hosted users can skip — it defaults to `api.contexo.live`). |
| `push: no repo_id configured` | `ctx remote set-repo <id>`, or `ctx join <invite-key>`. |
| Server returns 401 | Token expired or invalid — run `ctx login` again. (Legacy path: wrong `CONTEXO_API_KEY`, or the server isn't running with it set.) |
| Server returns 403 on invite/members | You're not an owner — only owners mint invite keys or remove members. |
| Server returns 404 on push | Probably fine — push auto-creates the repo. On pull, it means the repo has no commits yet. |
| Agent says it can't find `ctx_push` | Check the agent's MCP config (`.mcp.json` / `.cursor/mcp.json` / Codex) and that `ctx` is on PATH for the agent's environment. `ctx mcp status` shows what's wired. |
| `git: command not found` (server side) | Install git on the server machine. Contexo shells out to it. |

---

## Part 6 — What this is NOT

Things you might expect but won't find (or won't find the way you'd expect):

- **Silent capture.** Capture is *assisted*, not automatic: a Claude Code Stop hook can buffer each session's turns (`ctx capture`), and `ctx_capture_session` hands the agent that buffer plus a page template — but the agent still has to distill and write the page. There's no daemon archiving full conversation transcripts.
- **A full authoring Web UI.** The hosted **dashboard** lets you browse repos, members, and activity and mint tokens / invite keys — but you don't author knowledge pages in it. The local markdown files + your AI agent remain the authoring surface; `git log` / `git show` in the server's `<repo>` dir also work for raw inspection.
- **Per-page access control.** Access is per *repo* — you're a member (with an owner/member role) or you're not. There are no per-page or per-directory ACLs.
- **Full-text search ranking.** `ctx://search?q=...` does substring + tag/type filtering. No relevance ranking. The agent + `grep` is fine until proven otherwise.
- **Cross-project knowledge.** Contexo is per-project. Cross-project knowledge (Anthropic API, general patterns) stays in your personal `llm-wiki` or equivalent.
