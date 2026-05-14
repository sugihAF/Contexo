# CtxHub — Usage Walkthrough

Concrete end-to-end flow from zero. Two roles:

- **Admin** (one-time setup): runs the CtxHub server, hands out API keys.
- **Developer** (per dev, per project): installs the CLI, points it at the server, wires it into their AI agent.

Then the daily flow: Dev A researches with their agent, Dev A shares to the team, Dev B picks up the work, Dev B's agent sees Dev A's reasoning.

---

## Part 1 — Server setup (one-time, by admin)

### 1.1 Build the binaries

```
git clone <this repo>
cd contexo
go build -o bin/ctx ./cmd/ctx
go build -o bin/ctxhub ./cmd/ctxhub
```

Pure Go, no CGO. The server needs `git` on PATH (used internally for the per-repo storage).

### 1.2 Run the server

```
mkdir -p /var/ctxhub/repos
CTXHUB_DATA_ROOT=/var/ctxhub/repos \
CTXHUB_API_KEY=team-secret-key-here \
PORT=8080 \
./bin/ctxhub
```

That's it. The server is now listening on `:8080`. Health check:

```
curl http://localhost:8080/health
# {"status":"ok"}
```

### 1.3 What state lives where

- `/var/ctxhub/repos/<repo_id>/` — one git working repository per project. Real `git log` works in there; you can inspect history with normal git tools.
- `CTXHUB_API_KEY` — single shared key for the team (MVP). Multi-user keys with per-user identities are a deferred feature.
- No database. The git repos are the source of truth.

Back-ups: `rsync /var/ctxhub/repos/ <backup-target>/`. That's the whole thing.

### 1.4 Production deployment

For real use, put it behind nginx with HTTPS and rotate `CTXHUB_API_KEY` away from `dev-key`:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_addr;
}
```

A systemd unit, a small VM (1 CPU, 2 GB RAM is plenty for hundreds of pages and dozens of devs), and you're done.

### 1.5 Hand out credentials

Tell each developer:
- Server URL: `https://ctxhub.yourcompany.com` (or `http://localhost:8080` for local testing)
- API key: `team-secret-key-here`
- Repo ID for each project: e.g. `chompchat`, `acme-api`, etc.

---

## Part 2 — Developer onboarding (per developer, per project)

### 2.1 Get the CLI

```
go install github.com/sugihAF/contexo/cmd/ctx@latest
# or copy bin/ctx from the build above
```

### 2.2 Initialize the project

In your existing project directory (e.g. `~/code/chompchat`):

```
$ cd ~/code/chompchat
$ ctx init
Initialized .ctxhub in /home/sugih/code/chompchat
```

You now have a `.ctxhub/` tree:

```
.ctxhub/
├── config.json
├── index.md            ← always loaded into the AI's context
├── tags.md
├── raw/sessions/
└── wiki/
    ├── analyses/
    ├── concepts/
    └── entities/
```

Add `.ctxhub/.sync/` to `.gitignore` if you want — it's local sync metadata, not knowledge.

### 2.3 Point at the server and authenticate

```
$ ctx remote set https://ctxhub.yourcompany.com
Server: https://ctxhub.yourcompany.com

$ ctx remote set-repo chompchat
Repo: chompchat

$ ctx auth login \
    --api-key team-secret-key-here \
    --name "sugihAF" \
    --email "sugih@yourcompany.com"
Authenticated (server: https://ctxhub.yourcompany.com) (repo: chompchat)
```

`--name` and `--email` become the git commit author on every push, so teammates can see who contributed each piece of knowledge.

Verify:

```
$ ctx status
Initialized: yes
Server: https://ctxhub.yourcompany.com
Repo: chompchat
Authenticated: yes
User: sugihAF <sugih@yourcompany.com>
Local pages: 0
Last pull: (never)
Pages never pushed: 0
```

### 2.4 Wire it into your AI agent

CtxHub exposes both **resources** (read-only knowledge) and **tools** (`ctx_push`, `ctx_pull`, `ctx_status`, `ctx_write_page`) over MCP. The agent reaches it by running `ctx mcp` in the project directory.

**Claude Code** — create `.mcp.json` in the project root (or add to `~/.claude.json`):

```json
{
  "mcpServers": {
    "ctxhub": {
      "command": "ctx",
      "args": ["mcp"]
    }
  }
}
```

Or via the CLI:

```
claude mcp add ctxhub -- ctx mcp
```

**Cursor / Windsurf / other MCP clients**: same idea — register a server named `ctxhub` whose command is `ctx mcp` and whose working directory is the project root.

The first time the agent starts in this project, it will see the four `ctx_*` tools and the five `ctx://...` resources available.

### 2.5 Pull what's already there

If teammates have already pushed knowledge for this project, grab it:

```
$ ctx pull
Pulled 12 page(s); HEAD=a4b2c1d3
```

Now `.ctxhub/wiki/...` is populated and `.ctxhub/index.md` is regenerated. Open the project in your IDE — your AI agent has everything the team knows.

---

## Part 3 — The daily flow

The scenario: you (Dev A) spend a Tuesday afternoon researching how to model Stripe subscription billing for ChompChat. On Wednesday morning Dev B is going to do the implementation. Without CtxHub, Dev B's Claude would re-do the research from scratch, possibly badly. With CtxHub, Dev B's Claude inherits your reasoning.

### 3.1 Dev A — research session with the agent

You work normally with Claude Code in `~/code/chompchat/`. You discuss Stripe billing approaches, evaluate Connect vs. Billing, dig into the docs, reject some options. The conversation runs for an hour.

### 3.2 Dev A — share with the team (natural language)

Near the end of the session, you tell Claude:

> *"Distill this session into a CtxHub concept page and sync it to contexthub. Include an Agent Reasoning section explaining what we considered and rejected."*

Claude invokes `ctx_write_page` with the slug `stripe-subscription`, type `concept`, your distilled body (Decision + Agent Reasoning + Current State + Open Questions), tags `[stripe, billing, subscription]`, and a `reasoning_summary` like *"Rejected Connect (negative-balance ownership); chose Billing + metered usage"*.

Then Claude invokes `ctx_push` with `feature: stripe`. Server returns the new commit sha.

You can verify:

```
$ ctx log
1a320e1d  2026-05-14 16:42  sugihAF — agent push (1 pages)
```

Or do it manually if you prefer not to trust the agent with the push:

```
$ ctx push --feature stripe --dry-run
Would push 1 page(s):
  wiki/concepts/stripe-subscription.md  (parent=)

$ ctx push --feature stripe -m "Stripe subscription research"
Pushed 1 page(s); HEAD=1a320e1d
```

### 3.3 Dev B — start the implementation

Wednesday morning. Dev B opens `~/code/chompchat/` for the first time today. Their `.ctxhub/` is from a few days ago. They tell their Claude:

> *"I'm picking up the Stripe subscription implementation. What does the team already know?"*

Claude invokes `ctx_pull` (which it knows to do from the tool's description — *"Call this at the start of a session when picking up work on a topic"*). The new page lands in Dev B's `.ctxhub/wiki/concepts/stripe-subscription.md`. The index regenerates.

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

Claude reads the page, edits the body, and calls `ctx_push`. The server returns 200 with a new commit attributed to Dev B. Dev A pulls later and sees Dev B's addition.

---

## Part 4 — Conflict resolution

If Dev A and Dev B both edit the same page locally before either pushes, whoever pushes second gets a conflict:

```
$ ctx push --feature stripe
1 conflict(s):
  wiki/concepts/stripe-subscription.md: current=af84a79f expected_parent=1a320e1d
Resolve by running 'ctx pull', merging the conflicting pages, then 'ctx push' again.
Error: push: 1 conflict(s) remain
```

The fix:

```
$ ctx pull --full
```

Now both versions exist locally — the server's version overwrites yours since `parent_sha` was stale. **Read both versions** (your `git diff` or just your memory of what you wrote vs. the pulled content), merge them by hand or by asking Claude to merge them intelligently:

> *"The pulled stripe-subscription page diverged from my version. Read both versions and merge them coherently — keep both authors' insights."*

Claude rewrites the page with the merged content. Then `ctx push` again.

For markdown prose this works better than git's line-based merge because the agent can understand the *intent* of each section.

---

## Part 5 — Troubleshooting

| Symptom | Fix |
|---|---|
| `mcp: open store: no such file or directory` | Run `ctx init` in the project root first. |
| `push: no credentials, run 'ctx auth login' first` | `ctx auth login --api-key ...` |
| `push: no server URL configured` | `ctx remote set <url>` |
| `push: no repo_id configured` | `ctx remote set-repo <id>` |
| Server returns 401 | Wrong API key, or server isn't running with `CTXHUB_API_KEY` set. |
| Server returns 404 on push | Probably fine — push auto-creates the repo. If on pull, the repo has no commits yet. |
| Agent says it can't find `ctx_push` | Check `.mcp.json` is registered and the `ctx` binary is on PATH for the agent's environment. |
| `git: command not found` (server side) | Install git on the server machine. CtxHub shells out to it. |

---

## Part 6 — What this is NOT

Things you might expect but won't find:

- **Automatic capture.** The agent has to write pages explicitly (via `ctx_write_page` or by editing files). There's no daemon recording every conversation.
- **Multi-user access control.** Single shared API key per server. Per-user roles are deferred.
- **Web UI.** The local markdown files + your AI agent are the UI. If you want to browse server-side, `git log` and `git show` in `/var/ctxhub/repos/<repo>/` work.
- **Full-text search ranking.** `ctx://search?q=...` does substring + tag/type filtering. No relevance ranking. The agent + `grep` is fine until proven otherwise.
- **Cross-project knowledge.** CtxHub is per-project. Cross-project knowledge (Anthropic API, general patterns) stays in your personal `llm-wiki` or equivalent.
