# Contexo — Usage Walkthrough

> **Canonical docs live at <https://docs.contexo.live>.** That site is generated
> from the CLI's own command surface and re-captures real terminal output on
> every release, so it cannot drift. This walkthrough is hand-maintained and
> will lag it — prefer the site when the two disagree. The sample output below
> was recorded from ctx 0.7.0-rc.1 against a local server, with the project shown
> as `~/code/chompchat` (Dev A and Dev B each have their own checkout) and the
> server as `https://contexo.yourcompany.com`.

Concrete end-to-end flow from zero. Two roles:

- **Admin** (one-time setup): runs the Contexo server and invites developers (Google sign-in, invite keys, or — for simple instances — a legacy shared key).
- **Developer** (per dev, per project): installs the CLI, points it at the server, signs in, and wires it into their AI agent.

Then the daily flow: Dev A researches with their agent, Dev A shares to the team, Dev B picks up the work, Dev B's agent starts from Dev A's decisions and the options Dev A ruled out.

> Most people don't self-host. The hosted server lives at `https://api.contexo.live` with the dashboard at `https://app.contexo.live`, and `ctx login` targets it by default. If that's you, skip Part 1 and start at Part 2.

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
curl -s http://localhost:8080/health
# {"status":"ok","api":1,"version":"dev","capabilities":["pages.at","push.expect_absent","write.path_sha"]}
```

`version` is the server's build stamp (`dev` for a build without release ldflags, which includes the Docker build). `capabilities` must list `pages.at`, `push.expect_absent` and `write.path_sha` for ctx 0.7.0's safe sync (see §1.5).

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
curl -s http://localhost:8080/health
# {"status":"ok","api":1,"version":"dev","capabilities":["pages.at","push.expect_absent","write.path_sha"]}
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

**Upgrading for ctx 0.7.0: rebuild the server at v0.7.0.** (The hosted service is already updated; this is for self-hosters.) ctx 0.7.0's safe sync relies on three server capabilities that only a v0.7.0 `contexo-server` has: per-page shas on no-op writes (`write.path_sha`), reads at a past commit (`pages.at`, used by `ctx show --at <sha>` and `ctx repair`) and the create guard (`push.expect_absent`). The v0.7.0 server also announces them in response headers and records which client pushed (`X-Contexo-Client`). Rebuild (Docker: the **Update** steps in §1.2; from source: `go build -o bin/contexo-server ./cmd/contexo-server` at the v0.7.0 tag), restart, then check the headers through your proxy:

```
$ curl -sI https://contexo.yourcompany.com/health | grep -i '^x-contexo'
X-Contexo-Capabilities: pages.at,push.expect_absent,write.path_sha
X-Contexo-Server-Version: dev
```

Against an older server ctx 0.7.0 still works, with less safety: `ctx status` warns `server_outdated`, `ctx show --at <sha>` fails with `server_unsupported`, and creates are not guarded.

### 1.6 Invite developers

Two ways to onboard a developer onto a repo:

- **Google sign-in** (requires `GOOGLE_OAUTH_CLIENT_ID`): the developer opens the dashboard, signs in with Google, and mints a personal access token from the Settings page. That gives them an identity; they join a specific repo by redeeming an invite key (below).
- **Invite key**: an owner mints a repo invite key and shares it with the developer:

```
ctx invite mint --label "alice laptop"   # prints a ctxi_… key: send it to the developer
ctx invite list                          # the keys you minted (revoke one with `ctx invite revoke`)
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

Keep it current with `ctx update` (or `ctx update --check` to just look). After every update, **restart your agent sessions** (Claude Code, Cursor, Codex, VS Code, …): a running agent keeps the `ctx mcp` process it started with, and with it the old code.

### 2.2 Initialize the project

In your existing project directory (e.g. `~/code/chompchat`). This is Dev B's run; VS Code is installed on that machine, so `ctx init` wires it too:

```
$ cd ~/code/chompchat
$ ctx init
── SETUP
  Wired Claude Code (.mcp.json) — restart your agent to load the contexo server
  VS Code detected — wired .vscode/mcp.json
  .vscode/ is usually committed: teammates need ctx on PATH
  Created .gitignore with .contexo/
  Installed Contexo capture Stop-hook in .claude/settings.json
  Restart Claude Code (or open a new session) so the hook is picked up.

✔ Initialized .contexo in ~/code/chompchat

── AGENT INTEGRATIONS

     AGENT        MCP SERVER                    CAPTURE (STOP HOOK)
  ●  Claude Code  wired (.mcp.json)             installed
  ○  Cursor       not wired (.cursor/mcp.json)  not installed
  ●  VS Code      wired (.vscode/mcp.json)      -
  ○  Codex        not installed                 not installed

── ADD CONTEXO TO ANOTHER AGENT OR HARNESS
…
```

`ctx init` creates the `.contexo/` tree, wires the Contexo MCP server into Claude Code (`.mcp.json`) and — when they are detected — into Cursor (`.cursor/mcp.json`) and VS Code (`.vscode/mcp.json`; `--no-vscode` skips it), and installs the per-turn capture hook for Claude Code (and Codex/Cursor, when detected). If Codex is detected it also prints how to wire its MCP server (`ctx mcp install --tool=codex`, which writes the global `~/.codex/config.toml`). The rest of its output (trimmed above) is the per-agent setup guide that `ctx mcp guide` also prints.

```
.contexo/
├── config.json          ← server URL, repo_id, dashboard URL, last-pull marker
├── credentials.json     ← auth token + identity (written by `ctx login`; chmod 600)
├── index.md             ← always loaded into the AI's context
├── tags.md
├── raw/sessions/
├── wiki/
│   ├── analyses/
│   ├── concepts/
│   └── entities/
└── .sync/               ← base snapshots, recorded conflicts, lock, backups
                           (written by ctx on the first sync; ignores itself)
```

`credentials.json` holds your token and should never be committed — `ctx init` adds `.contexo/` to `.gitignore`, and `.contexo/.sync/` carries its own `.gitignore` (`*`) on top. To reverse everything `ctx init` did, run `ctx detach` (`--keep-knowledge` preserves `.contexo/`).

### 2.3 Point at the server and sign in

**Hosted Contexo** (`api.contexo.live`) is the default, so just sign in:

```
ctx login
```

`ctx login` opens the dashboard (`https://app.contexo.live`), you sign in with Google, and a freshly-minted personal access token (`ctxp_…`) is copied back to the CLI over a loopback redirect — no copy-pasting. Your name and email come from the Google session and become the git author on every push.

**Self-hosted server** — name the server with `--server`, and get the token from *that* server: either its dashboard (`--dashboard`, browser flow) or a token it issued (`--token`):

```
ctx login --server https://contexo.yourcompany.com --dashboard https://dash.yourcompany.com
ctx login --server https://contexo.yourcompany.com --token <pat-from-that-dashboard>
```

ctx sends a token only to the server it was saved for. If the project's config names a self-hosted server and you start the browser login without `--server` or a self-hosted `--dashboard`, ctx refuses (`server_unresolved`) rather than have the hosted dashboard mint a token that would then be sent there.

**No browser** (CI, headless, remote box) — paste a token instead:

```
ctx login --no-browser       # prompts for a PAT minted from the dashboard's Settings page
```

…or pass it directly with `ctx login --token <pat>`.

**Legacy shared key** (simple self-hosted instances) — still supported, deprecated:

```
$ ctx auth login --server https://contexo.yourcompany.com --api-key "$CONTEXO_API_KEY" --repo chompchat --name "Dev A" --email dev-a@example.com
warning: --api-key is deprecated; use --token instead (PATs are minted from the dashboard's Settings page).
✔ Authenticated (legacy API key) · server: https://contexo.yourcompany.com · repo: chompchat
```

Then select the repo (unless you passed `--repo`) — redeem an invite key, or pick one you already have access to:

```
ctx join ctxi_…                  # if an owner gave you an invite key
ctx remote set-repo chompchat    # — or — omit the id for an interactive picker
```

Verify:

```
$ ctx status
contexo  ● connected

── REMOTE
server     https://contexo.yourcompany.com  (from credentials)
repo       chompchat
user       Dev A <dev-a@example.com>
auth       authenticated (legacy)

── KNOWLEDGE
pages      0
last pull  (never)
captures   none pending

✔ in sync with the server
```

### 2.4 Wire it into your AI agent

`ctx init` wires the project-local agents for you: **Claude Code** (`.mcp.json`) always, and **Cursor** (`.cursor/mcp.json`) and **VS Code** (`.vscode/mcp.json`) when they are detected (VS Code: `~/.vscode` or `code` on PATH). The agent runs `ctx mcp` in the project directory and gets Contexo's **ten tools** and **five resources** over MCP.

Wire (or re-wire) any agent explicitly with `ctx mcp install`:

```
ctx mcp install --tool=cursor    # ./.cursor/mcp.json (project-local)
ctx mcp install --tool=vscode    # ./.vscode/mcp.json (project-local, key "servers")
ctx mcp install --tool=codex     # ~/.codex/config.toml (GLOBAL — prompts first)
ctx mcp install --tool=all       # claude + cursor (+ vscode if detected, codex if installed)
ctx mcp status                   # show what's wired
ctx mcp guide                    # how to wire Windsurf, OpenCode, Hermes, OpenClaw, ...
```

For VS Code:

```
$ ctx mcp install --tool=vscode
✔ Wired VS Code (.vscode/mcp.json)

$ cat .vscode/mcp.json
{
  "servers": {
    "contexo": {
      "args": [
        "mcp",
        "--root",
        "${workspaceFolder}"
      ],
      "command": "ctx",
      "type": "stdio"
    }
  }
}

$ ctx mcp status
── AGENT INTEGRATIONS

     AGENT        MCP SERVER                    CAPTURE (STOP HOOK)
  ●  Claude Code  wired (.mcp.json)             installed
  ○  Cursor       not wired (.cursor/mcp.json)  not installed
  ●  VS Code      wired (.vscode/mcp.json)      -
  ○  Codex        not installed                 not installed

Add another agent (Windsurf, OpenCode, Hermes, ...)?
try  ctx mcp guide
```

`.vscode/` is usually committed, so teammates need `ctx` on their PATH. VS Code also reads the `.mcp.json` written for Claude Code: with VS Code's default `chat.mcp.collisionBehavior: "disable"` the `.vscode/mcp.json` entry wins and you see one `contexo` server; with `"suffix"` you see two, so drop one with `ctx mcp uninstall --tool=vscode`. Opt out of the automatic wiring with `ctx init --no-vscode`.

`ctx init` prints the same integration table plus a per-tool setup guide at the
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

The ten tools the agent sees are `ctx_push`, `ctx_pull`, `ctx_status`, `ctx_write_page`, `ctx_history`, `ctx_evolution`, `ctx_diff`, `ctx_capture_session`, `ctx_migrate` and `ctx_generate`; the resources are `ctx://index`, `ctx://tags`, `ctx://wiki/{slug}`, `ctx://raw/{session-id}`, and `ctx://search?q=&type=&tag=`. (Full descriptions in the README's MCP section.) Restart the agent after wiring, and again after every `ctx update`.

### 2.5 Pull what's already there

If teammates have already pushed knowledge for this project, grab it:

```
$ ctx pull
Pulled 1 page(s), adopted 0; HEAD=0d0d1282

$ ctx pull
Already up to date
```

Now `.contexo/wiki/...` is populated and `.contexo/index.md` is regenerated. Open the project in your IDE — your AI agent has everything the team knows. `ctx pull` never overwrites a page you edited locally; such pages are listed as skipped (see Part 4).

---

## Bulk onboarding an existing project (`ctx migrate`)

Already have knowledge written down — an llm-wiki, a `docs/` tree, ADRs, a
`CLAUDE.md`, or a dedicated knowledge repo? Import it in bulk instead of
re-deriving it session by session.

Two ways to run it, same result (your coding agent does the reformatting —
Contexo never runs its own model):

**From your agent (recommended).** Just ask:

> "migrate my llm-wiki into contexo"

The agent calls the `ctx_migrate` tool, which returns a numbered catalog of
everything it found (metadata + a one-line preview only). You pick which
items to import in chat; the agent reads those files, reformats each into a
Contexo page, and pushes — recording where every page came from in a
migration `source` page.

Import a knowledge base that lives elsewhere with
`from="<path-or-git-url>"`, e.g. a dedicated GitHub wiki repo.

**From the terminal.**

```bash
ctx migrate                     # scan this project, pick interactively, stage
ctx migrate --list              # just show what would be migrated
ctx migrate --from ../llm-wiki  # a local knowledge base outside this repo
ctx migrate --from https://github.com/me/kb.git
ctx migrate --type entity       # only items suggested as entities
ctx migrate --detector docs     # only items from one detector (kbtree|docs|agentfiles)
ctx migrate --all --yes         # non-interactive: select all + skip the confirm (scripts/CI)
```

Add `--all` to select every discovered item and `-y`/`--yes` to skip the confirmation prompt — handy for scripting.

The CLI stages your selection and tells you to ask your agent to
"finish the contexo migration". Your agent then imports exactly those items and
calls `ctx_migrate(done=true)` to clear the staged manifest.

What it detects today: llm-wiki-style trees and Obsidian vaults; `docs/`,
root Markdown, README(s), ADRs, and CHANGELOG; and agent-instruction files
(`CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, `.cursor/rules`).

---

## Generating a KB from code (`ctx generate`)

No docs to migrate — just a codebase? `ctx generate` bootstraps a starter
knowledge base *from the code*. It runs [Graphify](https://github.com/Graphify-Labs/graphify)
(an MIT code-graph tool you install separately) to map your codebase into
subsystems and key components, then hands those to your agent to turn into
verified Contexo pages.

**One-time:** Graphify is an external Python package (`graphifyy`). You don't
have to install it by hand — the first time you run `ctx generate` without it,
Contexo **offers to install it for you** (with your explicit OK) using whichever
of `uv` / `pipx` / `pip` you already have. Prefer to do it yourself? Run
`uv tool install graphifyy` (or `pipx install graphifyy`). Code analysis is
fully local and needs no API key.

> Just installed it via the prompt and got "not on PATH yet"? On a brand-new
> `uv`/`pipx` setup the tools directory may not be on the current shell's PATH.
> Open a new terminal and re-run `ctx generate` — it'll be found.

**From your agent (recommended):**

> "we have no docs — generate contexo knowledge from this codebase"

The agent calls `ctx_generate`, which runs Graphify and returns a numbered
catalog of subsystems (→ concept pages) and core components (→ entity pages),
plus an overview. You pick which to import; the agent reads each slice **and the
real source it cites**, verifies it (auto-extracted edges tagged `INFERRED` are
guesses), writes accurate pages, and pushes — then cleans up with
`ctx_generate(done=true)`.

If Graphify isn't installed, `ctx_generate` returns a `<GENERATE_NEEDS_GRAPHIFY>`
directive; the agent asks your permission and, once you agree, re-invokes
`ctx_generate(install=true)` to install it and continue. (After a first-ever
install you may need to restart the agent / MCP server so it sees the new PATH.)

**From the terminal:**

```bash
ctx generate                 # run Graphify on this project, pick interactively, stage
ctx generate --list          # just show what would be generated
ctx generate --from ../svc   # analyze a different codebase directory
ctx generate --type entity   # only the component (entity) candidates
ctx generate --all --yes     # non-interactive: stage everything (also auto-approves a Graphify install)
ctx generate --backend gemini  # optional: use an LLM for richer subsystem names (needs a key)
```

Then ask your agent to "finish the contexo generation." Everything Graphify
produces lives under `.contexo/.generate-cache/` (never pushed) and is removed
when the agent finishes.

Note: `ctx generate` produces a *scaffold* from static analysis — your agent
verifies it against the source before anything is written, so the resulting
pages reflect the real code, not just the graph.

---

## Part 3 — The daily flow

The scenario: you (Dev A) spend a Tuesday afternoon researching how to model Stripe subscription billing for ChompChat. On Wednesday morning Dev B is going to do the implementation. Without Contexo, Dev B's Claude would re-do the research from scratch, possibly badly. With Contexo, Dev B's Claude inherits your decisions and the options you ruled out.

### 3.1 Dev A — research session with the agent

You work normally with Claude Code in `~/code/chompchat/`. You discuss Stripe billing approaches, evaluate Connect vs. Billing, dig into the docs, reject some options. The conversation runs for an hour.

### 3.2 Dev A — share with the team (natural language)

Near the end of the session, you tell Claude:

> *"Distill this session into a Contexo concept page and sync it to contexo. Include a Rationale section: the options we considered and why we rejected them."*

Claude invokes `ctx_write_page` with the slug `stripe-subscription`, type `concept`, your distilled body (Decision + Rationale + Current State + Open Questions), tags `[stripe, billing]`, and a `reasoning_summary` like *"Rejected Connect (negative-balance ownership); chose Billing + metered usage"*.

Then Claude invokes `ctx_push` with `feature: stripe`. Server returns the new commit sha. (If the session's capture buffer has not been distilled yet, `ctx_push` first asks the agent for a `source` page — the decision record of the session — and then pushes.)

Or do the push manually if you prefer not to trust the agent with it:

```
$ ctx status
contexo  ● connected

── REMOTE
server     https://contexo.yourcompany.com  (from credentials)
repo       chompchat
user       Dev A <dev-a@example.com>
auth       authenticated (legacy)

── KNOWLEDGE
pages      1 · 1 new
last pull  (never)
captures   none pending

── TO PUSH
    wiki/concepts/stripe-subscription.md   new
run  ctx push

$ ctx push --dry-run
About to push 1 page(s) to chompchat:
  [NEW]   wiki/concepts/stripe-subscription.md
(dry-run; no changes sent)

$ ctx push -m "Stripe subscription research"
About to push 1 page(s) to chompchat:
  [NEW]   wiki/concepts/stripe-subscription.md
Pushed 1 page(s); HEAD=0d0d1282
```

`ctx push` sends only the pages you changed or created since the last sync (here the one new page); name paths (`ctx push wiki/concepts/stripe-subscription.md`) to send just those. While an un-distilled capture session is pending, a manual push of concept or analysis pages is refused with `distill_required` and prints a prompt to paste into your agent — share through the agent instead (see Part 5).

You can verify:

```
$ ctx log
  COMMIT    WHEN              WHO    MESSAGE
  0d0d1282  2026-10-22 20:05  Dev A  Stripe subscription research
```

(or `ctx activity` for the fuller push/pull timeline).

### 3.3 Dev B — start the implementation

Wednesday morning. Dev B opens `~/code/chompchat/` for the first time today. Their `.contexo/` is from a few days ago. They tell their Claude:

> *"I'm picking up the Stripe subscription implementation. What does the team already know?"*

Claude invokes `ctx_pull` (which it knows to do from the tool's description — *"Call this at the start of a session when picking up work on a topic"*). The new page lands in Dev B's `.contexo/wiki/concepts/stripe-subscription.md`. The index regenerates.

Or Dev B just runs:

```
$ ctx pull
Pulled 1 page(s), adopted 0; HEAD=0d0d1282
```

### 3.4 Dev B's agent reads the context

Claude reads `ctx://index` (always loaded), sees the new entry:

```
## Concepts
- [Stripe Subscription](wiki/concepts/stripe-subscription.md) — Rejected Connect (negative-balance ownership); chose Billing + metered usage | tags: stripe,billing | Dev A 2026-10-22
```

Then reads `ctx://wiki/stripe-subscription`, sees (frontmatter trimmed):

```markdown
…
## Decision

ChompChat bills each restaurant through Stripe Billing: one subscription
with a flat monthly base price plus a metered price per order placed
through the chat widget. Order counts are reported to Stripe as usage
records on the metered subscription item.

## Rationale

- **Stripe Connect (rejected).** Connect would let us pay restaurants out
  directly, but with Express or Custom accounts the platform owns negative
  balances: refunds and disputes a restaurant cannot cover fall back on
  ChompChat. We never hold restaurant money, so we would carry that risk
  for nothing.
- **Flat per-location plans (rejected).** Easy to invoice, but a food truck
  and a 40-table restaurant would pay the same, and the price would not
  follow the per-order cost we actually incur.
- **Billing + metered usage (chosen).** The price follows order volume,
  Stripe computes the invoice and handles dunning, and no money moves
  through ChompChat beyond the subscription itself.

## Open Questions

- Report usage per order in real time, or batch it nightly?
- What happens to metered usage when a restaurant upgrades mid-period?
```

Dev B's Claude now starts implementation from this baseline. No re-deriving. No accidentally pivoting back to Connect. If it has questions about why Connect was rejected, the answer is right there.

### 3.5 Dev B contributes back

As Dev B implements, they learn something new — say, that Stripe rate-limits the `usage_record_summaries` endpoint, so the billing page should read it through a cache. They tell Claude:

> *"Update the stripe-subscription page with what we learned about usage_record_summaries caching."*

Claude reads the page, edits the body, and calls `ctx_push`. The server returns a new commit attributed to Dev B. Dev A pulls later and sees Dev B's addition.

---

## Part 4 — Conflict resolution

Nobody's work is silently overwritten. If a teammate pushes a change to a page you have also edited since your last sync, the page becomes a **conflict** on your machine and stays one until you settle it. The sync state behind this (base snapshots under `.contexo/.sync/`, the nine page states) is described in the README's [Safe sync](../README.md#safe-sync) section.

Here Dev A added a caching note to the Decision section and pushed. Dev B, without pulling first, answered one of the open questions in the same page. `ctx status` on Dev B's machine shows the conflict and both ways out:

```
$ ctx status
contexo  ● connected

── REMOTE
server     https://contexo.yourcompany.com  (from credentials)
repo       chompchat
user       Dev B <dev-b@example.com>
auth       authenticated (legacy)

── KNOWLEDGE
pages      1 · 1 conflict
last pull  0d0d1282
captures   none pending

── CONFLICTS
    wiki/concepts/stripe-subscription.md   both_modified
keep yours   ctx resolve wiki/concepts/stripe-subscription.md --ours
take theirs  ctx resolve wiki/concepts/stripe-subscription.md --theirs
```

While it is a conflict, neither sync command touches the page: `ctx push` leaves it out, and `ctx pull` brings in everything else and lists it as skipped (both still exit 0):

```
$ ctx push
Nothing to push.
Skipped 1 page(s):
  wiki/concepts/stripe-subscription.md  (conflict)

$ ctx pull
Pulled 0 page(s), adopted 0; HEAD=f3420bd8
Skipped 1 page(s):
  wiki/concepts/stripe-subscription.md: conflicts with the server version; run ctx resolve wiki/concepts/stripe-subscription.md --theirs|--ours
```

**1. Look at the teammate's version.** `ctx show <path> --at server` prints the server's current page as raw bytes; `--at base` prints the version you both started from, and `--at <sha>` any version from `ctx log` (a short sha works). Here the server copy carries Dev A's two new lines:

```
$ ctx show wiki/concepts/stripe-subscription.md --at server
…
## Decision

ChompChat bills each restaurant through Stripe Billing: one subscription
with a flat monthly base price plus a metered price per order placed
through the chat widget. Order counts are reported to Stripe as usage
records on the metered subscription item.
The billing page reads `usage_record_summaries` through a 15-minute cache;
Stripe rate-limits that endpoint.
…
```

**2. Merge into your file, then keep it with `--ours`.** Copy the teammate's change into your working file (or ask your agent: *"Compare the server version of the stripe-subscription page with mine and merge them into my file — keep both authors' changes."*). `--ours` keeps your file and re-bases it onto the server version, so it becomes an ordinary `modified` page:

```
$ ctx resolve wiki/concepts/stripe-subscription.md --ours
✔ Kept your version of wiki/concepts/stripe-subscription.md on top of server f3420bd; now run ctx push

$ ctx status
…
── KNOWLEDGE
pages      1 · 1 modified
last pull  f3420bd8
captures   none pending

── TO PUSH
    wiki/concepts/stripe-subscription.md   modified
run  ctx push
```

**3. Push.** On a terminal the preview asks before editing a page on the server:

```
$ ctx push -m "Merge Dev A's caching note"
About to push 1 page(s) to chompchat:
  [EDIT]  wiki/concepts/stripe-subscription.md  (frontmatter, ~ Open Questions)
Proceed? [y/N]: y
Pushed 1 page(s); HEAD=1dba0b86
```

**Or take theirs.** `ctx resolve <path> --theirs` replaces your file with the server version. If your file differs, it is saved first under `.contexo/.sync/backup/` (`backup_file` is relative to `.contexo/`). From a later round of the same scenario:

```
$ ctx resolve wiki/concepts/stripe-subscription.md --theirs --json
{
  "schema": "ctx.resolve.v1",
  "path": "wiki/concepts/stripe-subscription.md",
  "strategy": "theirs",
  "previous_state": "conflict",
  "state": "unchanged",
  "base_sha": "99b5de66931e72149d3fbe7b7e2a9b5a8e7515cc",
  "file_written": true,
  "backup_file": ".sync/backup/wiki/concepts/stripe-subscription.md.20261022T200513Z",
  "warnings": []
}

$ ls -R .contexo/.sync/backup
wiki

.contexo/.sync/backup/wiki:
concepts

.contexo/.sync/backup/wiki/concepts:
stripe-subscription.md.20261022T200513Z
```

Scripts (and the editor extension) can pass `--if-server-sha <sha>` — the server sha they showed you, `.pages[].remote.sha` in `ctx status --json` — so the resolve fails with `stale` (exit 6) if a teammate pushed again in between. The `--json` documents, the error envelope and every exit code are in the README's [Machine output](../README.md#machine-output---json) section.

**When the agent drives the push**, `ctx_push` never overwrites the teammate's page either. It returns a `<MERGE_REQUIRED>` directive with the common ancestor, your version and the server's current page, plus the sections both sides changed; the local sync state is not changed, so the page stays a conflict. The agent writes a reconciled page with `ctx_write_page` that keeps both sides' changes, then calls `ctx_push` with `resolved=["wiki/concepts/stripe-subscription.md"]`. That keeps the merged content, re-bases it onto the server version the agent was shown and pushes it; if the server moved again meanwhile, the agent gets a fresh `<MERGE_REQUIRED>` and nothing is overwritten. `ctx_pull` is safe in the same way: it never overwrites a page you edited, and pages changed on both sides come back as `<MERGE_REQUIRED>`. For markdown prose, an agent that understands each section's *intent* merges better than git's line-based diff.

> Heads-up before you even get here: when the agent reads `ctx://wiki/<slug>` and the page changed on the server since it was last synced, the response is prefixed with a `<DRIFT_NOTICE>` summarizing what's new — so it usually learns about divergence *before* editing (`CONTEXO_DRIFT_DISABLE=1` turns the check off). `ctx status` shows such pages as `incoming`, or `conflict` once you have edited them too.

---

## Part 5 — Troubleshooting

With `--json`, every failure below also ends stderr with a one-line `ctx.error.v1` envelope and exits with the code shown; without `--json` the process exits 1. The full table is in the README's [Machine output](../README.md#machine-output---json) section.

| Symptom | Fix |
|---|---|
| Agent shows `contexo` with no tools (or `mcp: open store …`) | You're outside a Contexo project — `ctx mcp` runs dormant there. Run `ctx init` in the project root. |
| Agent tools still behave like the ctx you had before `ctx update` | A stale MCP process: each agent session keeps the `ctx mcp` it started with. Restart your agent sessions (Claude Code, Cursor, Codex, VS Code, …) after every update. |
| `Not initialized (run 'ctx init')` (`not_initialized`, exit 5) | No `.contexo/` here. Run `ctx init` in the project root, or pass `--root <dir>`. |
| `push: not authenticated` (`not_authenticated`, exit 4) | `ctx login` (or `ctx login --token <pat>`). |
| `push: repo not configured` (`repo_not_configured`, exit 5) | `ctx remote set-repo <id>`, or `ctx join <invite-key>`. |
| `push: server URL unresolved: the credentials have no server_url` (`server_unresolved`, exit 5) | Your global credentials don't say which server they belong to, so ctx won't send them anywhere. Run `ctx auth login --server <url>`. |
| `ctx status` warns that the config's server differs from your credentials' server (`server_url_mismatch`) | ctx uses the credentials' server, never the config's. Log in to the server you want with `ctx auth login --server <url>`, or point the config back with `ctx remote set <url>`. |
| `push: refusing to alter existing pages in non-interactive mode; pass --yes to confirm` | The push edits existing pages and there is no terminal to ask. Re-run with `--yes` (or `--json`, which never prompts), or from a terminal. |
| `Share via your agent: Push my pending Contexo changes with the ctx_push tool (…)` (`distill_required`, exit 9) | A fresh capture session is pending and the push contains concept or analysis pages. Paste that prompt into your agent: its `ctx_push` writes the session's decision record (a `source` page) first, then pushes. `ctx push --no-distill` bypasses the guard; use it only deliberately. |
| `push: another ctx process is syncing (push, pid 99249 on dev-laptop.local, since 2026-10-22T20:05:17Z)` (`busy`, exit 12) | Another ctx command or agent holds `.contexo/.sync/lock`. Retry when it finishes (ctx waits 10 s first; `CONTEXO_LOCK_TIMEOUT` changes that). A lock older than 10 minutes, or one on this host whose process has died, is reclaimed automatically. |
| A page stays `unverified` (warning `base_repair_skipped`), or `ctx show --at base` fails with `base_unavailable` (exit 8) | The page has no usable base snapshot — typical right after upgrading from an older ctx. Run `ctx repair --dry-run` to see what it would do, then `ctx repair`. |
| `ctx status` warns `server_outdated`, or `ctx show --at <sha>` fails with `server_unsupported` (exit 10) | The server predates v0.7.0. Ask the admin to rebuild `contexo-server` at v0.7.0 (§1.5). |
| A page shows `conflict` and push and pull skip it | Compare with `ctx show <path> --at server`, then settle it with `ctx resolve <path> --ours` or `--theirs` (Part 4). |
| Server returns 401 (`auth_rejected`) | Token expired or invalid — run `ctx login` again. (Legacy path: wrong `CONTEXO_API_KEY`, or the server isn't running with it set.) |
| Server returns 403 on invite/members (`forbidden`) | You're not an owner — only owners mint invite keys or remove members. |
| Server returns 404 on push | Probably fine — push auto-creates the repo. On pull, it means the repo has no commits yet. |
| Agent says it can't find `ctx_push` | Check the agent's MCP config (`.mcp.json` / `.cursor/mcp.json` / `.vscode/mcp.json` / Codex) and that `ctx` is on PATH for the agent's environment. `ctx mcp status` shows what's wired. |
| VS Code lists two `contexo` servers | VS Code reads both `.mcp.json` and `.vscode/mcp.json`; with `chat.mcp.collisionBehavior: "suffix"` you see both. Drop one with `ctx mcp uninstall --tool=vscode`. |
| `git: command not found` (server side) | Install git on the server machine. Contexo shells out to it. |

---

## Part 6 — What this is NOT

Things you might expect but won't find (or won't find the way you'd expect):

- **Silent capture.** Capture is *assisted*, not automatic: a Claude Code Stop hook can buffer each session's turns (`ctx capture`), and `ctx_capture_session` hands the agent that buffer plus a page template — but the agent still has to distill and write the page. There's no daemon archiving full conversation transcripts.
- **A full authoring Web UI.** The hosted **dashboard** lets you browse repos, members, and activity and mint tokens / invite keys — but you don't author knowledge pages in it. The local markdown files + your AI agent remain the authoring surface; `git log` / `git show` in the server's `<repo>` dir also work for raw inspection.
- **Per-page access control.** Access is per *repo* — you're a member (with an owner/member role) or you're not. There are no per-page or per-directory ACLs.
- **Full-text search ranking.** `ctx://search?q=...` does substring + tag/type filtering. No relevance ranking. The agent + `grep` is fine until proven otherwise.
- **Cross-project knowledge.** Contexo is per-project. Cross-project knowledge (Anthropic API, general patterns) stays in your personal `llm-wiki` or equivalent.
- **Delete sync.** A page you delete locally shows as `deleted_local` and is never pushed, and pull never brings it back.
