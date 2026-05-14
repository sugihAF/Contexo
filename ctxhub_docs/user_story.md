# CtxHub User Stories & Product Flow (user_story.md)

This document describes **how users use the product end-to-end** (like GitHub + Git + an AI “flight recorder”), with clear flows and what happens behind the scenes.

---

## 0. Glossary (user-facing)

- **Session**: one continuous conversation run with an AI tool (Claude Code session, Codex task, Cursor chat tab, VS Code chat session).
- **Recording**: the captured prompts + responses (and optionally tool calls) stored as a session log.
- **Context Commit**: a small “commit-like” summary that describes what happened / what changed / why (as explicit decisions), plus pointers into session recordings as evidence.
- **Feature stream**: grouping label for sessions/commits (e.g., `onboarding`, `payments`).
- **Feature Overview / Roadmap**: a small “living” summary per feature stream (purpose, objectives, milestones, key decisions, current status). Inspired by GCC’s `main.md` + per-branch `summary.md`.
- **Recent activity log**: a condensed rolling view of the last N meaningful events (Observation → Decision → Action → Result), derived from session events. Inspired by GCC’s OTA log (`log.md`).
- **Proactive commits**: optional setting that auto-suggests creating context commits after coherent milestones (tests pass, big diffs, long sessions). Inspired by GCC’s `proactive_commits`.
- **Synthesis commit**: an auto-generated context commit created during merge/PR merge that summarizes what was tried, what was integrated, and what was abandoned.
- **CtxHub**: hosted server (like GitHub) storing context history.
- **`ctx` CLI**: local CLI (like Git).
- **`ctxd`**: local recorder daemon (captures events, writes logs, syncs).

---

## 1. Personas

### 1.1 Developer (primary)
- Uses an AI assistant daily (Claude Code, Codex CLI, Cursor, VS Code chat)
- Needs fast handoff and reproducible reasoning artifacts

### 1.2 Tech Lead / Reviewer
- Wants to understand intent behind changes during PR review
- Wants lightweight audit trail without reading full transcripts

### 1.3 Org Admin / Security
- Needs controls: permissions, retention, redaction, encryption
- Wants safe defaults and compliance-friendly settings

---

## 2. Product mental model (the “GitHub-like” view)

### 2.1 What the user expects
- Like Git: `log`, `show`, `blame`, `push`, `pull`
- Like GitHub: a server UI to browse history and share it across the team
- Like a “flight recorder”: every prompt + response is captured automatically

### 2.2 What the AI experiences
In addition, both humans and agents should have a one-shot **“where were we?”** command (`ctx context`) that returns context at different levels (feature summary, recent log entries, repo metadata, or full roadmap). This mirrors the “multi-resolution context” pattern from Git Context Controller (GCC).

- It does **not** ingest all recordings at once.
- It reads a **commit list** (titles + summaries).
- It opens a **commit** if relevant.
- It opens a **session slice** only if it needs details.

---

## 3. Core user flows (with behind-the-scenes behavior)

> Each flow includes:
> - **User action**
> - **What the system does**
> - **Acceptance criteria**

---

### Flow A — First-time setup in a repo

#### A1) Initialize local context store
**User action**
```bash
ctx init
ctx remote add origin https://ctxhub.company.com/org/repo
ctx auth login
```

**System behavior (behind the scenes)**
1. Creates `.ctx/` directories:
   - `.ctx/commits/`
   - `.ctx/sessions/`
   - `.ctx/blobs/`
2. Creates `.ctx/config.json` with:
   - remote URL
   - repo identity mapping
   - redaction defaults
   - capture adapter settings
3. Starts `ctxd` (local recorder) or configures it to start on demand.
4. Stores auth token (secure storage if available; otherwise encrypted file).

**Acceptance criteria**
- Running `ctx status` shows repo initialized and remote configured.
- No server data is required to start recording locally.

---

### Flow B — Always-on capture in Claude Code (best fidelity)

> Claude Code supports lifecycle hook events such as `UserPromptSubmit` (includes prompt text) and `Stop` (includes last assistant message), enabling reliable capture of every turn.

#### B1) Enable capture for Claude Code
**User action**
```bash
ctx capture on --client claude-code
```

**System behavior**
1. Installs/updates Claude Code hooks configuration (or generates a snippet to paste).
2. Hooks run async and send events to `ctxd`:
   - On `UserPromptSubmit`: append a `user_message` event.
   - On `Stop`: append an `assistant_message` event.
3. For each captured event, `ctxd`:
   - runs local redaction rules,
   - writes a JSON line into `.ctx/sessions/claude_code/<session_id>.jsonl`,
   - updates local SQLite index.

**Acceptance criteria**
- After a few prompts, `ctx session ls` shows an active session with growing event count.
- Logs are append-only; no prompts are lost.

---

### Flow C — Always-on capture in Codex CLI (wrapper)

> Codex CLI supports machine-readable JSON output via `--json` for automation, which can be piped into the recorder.

#### C1) Run Codex through `ctx` wrapper
**User action**
```bash
ctx codex exec "Implement onboarding resend button"
```

**System behavior**
1. `ctx` executes:
   - `codex exec --json "Implement onboarding resend button"`
2. Reads Codex JSON stream from stdout and normalizes into ctx session events:
   - `user_message`, `assistant_message`, `tool_call`, etc. (best-effort based on Codex event types)
3. Appends to `.ctx/sessions/codex_cli/<session_id>.jsonl`.
4. Updates local index (by repo/branch/feature).

**Acceptance criteria**
- Every Codex prompt and response appears in session log.
- Errors are still captured (non-zero exit produces `error` event).

---

### Flow D — Best-effort capture in Cursor (import/watch)

> Cursor chat history is stored in SQLite `state.vscdb` files per workspace. You can import or watch and incrementally ingest new messages.

#### D1) One-time import
**User action**
```bash
ctx cursor import --workspace .
```

**System behavior**
1. Locates Cursor workspace storage DB (platform-specific).
2. Reads `state.vscdb`, extracts chat tabs/messages.
3. Converts them into session event logs:
   - `.ctx/sessions/cursor/<session_id>.jsonl`

**Acceptance criteria**
- Imported sessions are visible in `ctx session ls`.
- Imported messages preserve original timestamps/order.

#### D2) Continuous watch
**User action**
```bash
ctx cursor watch --workspace .
```

**System behavior**
- Polls DB for new rows / updated chat tab content.
- Appends new events to the correct session log.
- Records a cursor/bookmark so it doesn’t duplicate.

**Acceptance criteria**
- New Cursor messages appear within a bounded delay (e.g., seconds to minutes).

---

### Flow E — Create a Context Commit (Git-like summary)

#### E1) Create a commit after finishing a unit of work
**User action**
```bash
ctx commit -m "Onboarding: email verification flow"
git commit -am "Email verification flow"
ctx link HEAD
```

**System behavior**
1. `ctx commit` selects the relevant evidence window:
   - Most recent active session (or asks user to choose).
   - Default: last N turns or last X minutes.
2. Generates a **structured commit summary**:
   - summary bullets
   - explicit decisions + why (not hidden chain-of-thought)
   - next steps checklist
   - touched files/symbols (from git diff + tool traces)
3. Writes `.ctx/commits/<ctxCommitId>.json` and optional `.md`.
4. Updates local index: feature → commits, symbol → commits, file → commits.
5. `ctx link HEAD` binds ctx commit ↔ git SHA.

**Acceptance criteria**
- `ctx log onboarding` shows the new commit with title + 1–2 line summary.
- `ctx show <id>` shows decisions, next steps, and evidence pointers.

---

### Flow F — Push/pull like Git (team sharing)

#### F1) Push context to server
**User action**
```bash
ctx push
```

**System behavior**
1. Uploads new commits (JSON) to server metadata store.
2. Uploads referenced session logs/blobs to object storage:
   - may chunk by turn ranges
   - may compress (gzip/zstd)
3. Server enforces:
   - auth/permissions
   - retention policy
   - secret scanning/redaction (secondary)
4. Updates search/symbol indexes.

**Acceptance criteria**
- Another developer can run `ctx pull` and see the same commit history.

#### F2) Pull context from server
**User action**
```bash
ctx pull
```

**System behavior**
- Downloads commit metadata + index deltas.
- Session logs remain remote until explicitly opened, unless configured otherwise.

**Acceptance criteria**
- `ctx log` works offline for fetched history.

---

### Flow G — Handoff between developers using different AIs (the key scenario)

#### G1) Jhon worked on onboarding with Claude Code (sessions A, B, C)
He created commits:
- `C-001`: Step 1 UI skeleton
- `C-002`: Email verification flow
- `C-003`: Error handling + tests

#### G2) You continue using Codex or Cursor

**User action**
```bash
git checkout feature/onboarding
ctx pull
ctx log onboarding --oneline
```

**System behavior**
- Shows titles + summaries (cheap).
- AI is not overloaded; your agent can read this list first.

**Acceptance criteria**
- Commit list loads fast even if sessions are large.

#### G3) You inspect a likely relevant commit
**User action**
```bash
ctx show C-002
```

**System behavior**
- Loads commit JSON and renders:
  - decisions
  - touched symbols/files
  - next steps
  - evidence pointers (session id + turn range)

**Acceptance criteria**
- You can understand “why” from explicit decisions without opening the full transcript.

#### G4) You drill down only if needed
**User action**
```bash
ctx open-session C-002 --select turns
# or
ctx open-session S-B --turns 40..65
```

**System behavior**
- Fetches only the referenced session slice (local or remote).
- Renders a readable transcript view.
- Optionally highlights file/symbol refs.

**Acceptance criteria**
- The AI can “continue in the same style” because it can read the exact conversation that introduced the function.

#### G5) You implement new work and record it
**User action**
```bash
ctx codex exec "Add resend verification UI button with cooldown"
ctx commit -m "Onboarding: resend verification UI + cooldown"
ctx push
```

**Acceptance criteria**
- Future devs (or Jhon) can repeat the same handoff process.

---

### Flow H — “Context blame” for a function/symbol

#### H1) Find context history for a specific symbol
**User action**
```bash
ctx blame src/onboarding/verify.ts#sendVerificationEmail
```

**System behavior**
1. Resolves `symbolKey`.
2. Queries local index (or server) for commits that touched the symbol.
3. Returns ordered commits with summaries + evidence links.
4. Offers quick jump:
   - `ctx show <commit>`
   - `ctx open-session <session> --turns ...`

**Acceptance criteria**
- You can quickly answer: “Why is this function implemented this way?”

---

### Flow I — Use inside an AI tool via MCP (selective reading)

> MCP Resources allow AI tools to browse and read context resources by URI. Clients decide how and when to include them (application-controlled).

#### I1) AI reads commit list via MCP
**User action**
- In Cursor/Codex/Claude Desktop, user asks: “Continue onboarding resend logic.”

**System behavior**
- AI client:
  1) lists resources under `ctx://repo/<id>/features/onboarding/commits`
  2) reads the commit list (small JSON)
  3) reads `ctx://repo/<id>/commit/C-002` (small JSON)
  4) optionally reads `ctx://repo/<id>/session/S-B/turns/40-65` (slice)

**Acceptance criteria**
- The AI never receives the entire org history, only the pieces it requests.

---

## 4. Safety and privacy UX stories (must-have)

### S1) Pause capture when discussing sensitive data
**User action**
```bash
ctx capture pause
# have sensitive discussion
ctx capture resume
```

**Acceptance criteria**
- No events are written while paused.
- Resume continues in the same session (with a “pause gap” marker).

### S2) Redaction and denylist
**User action**
```bash
ctx config set redaction.patterns+=AWS_SECRET_ACCESS_KEY
ctx config set capture.deny_paths+=.env
```

**Acceptance criteria**
- Matches are masked locally before sync.
- Denylisted paths are never attached as refs in events/commits.

---

## 5. References (for product behavior inspiration)

- Claude Code hook events and fields (prompt capture, stop events, transcript_path): https://code.claude.com/docs/en/hooks
- Codex CLI reference (`--json` for automation): https://developers.openai.com/codex/cli/reference
- VS Code chat sessions and export: https://code.visualstudio.com/docs/copilot/chat/chat-sessions
- Cursor chat export based on `state.vscdb`: https://github.com/somogyijanos/cursor-chat-export
- MCP resources (URIs, list/read, templates, annotations): https://modelcontextprotocol.io/specification/2025-06-18/server/resources
- Cursor docs for MCP setup: https://cursor.com/docs/context/mcp
- Supermemory MCP remote config & OAuth discovery pattern: https://supermemory.ai/docs/supermemory-mcp/setup
- OpenContext’s stdio MCP bridge pattern: https://deepwiki.com/0xranx/OpenContext/5.2-mcp-server-integration

---

### Flow J — Fast context recovery with `ctx context` (multi-resolution, GCC-inspired)

**Goal:** A developer (or their agent) can quickly answer: “where were we?” without opening full transcripts.

#### J1) Default: feature-level context
**User action**
```bash
ctx context --feature onboarding
```

**System behavior (behind the scenes)**
1. CLI reads current repo + branch and resolves default feature stream (`onboarding`) using `.ctx/config.json` mapping rules (branch name prefixes, folder heuristics, or explicit `ctx feature set` if you support it).
2. CLI fetches (local cache first; remote if needed):
   - **Feature Overview** (purpose, objectives, milestones, status, key decisions)
   - last N **Context Commits** for the feature (default: 10)
   - pointers to top evidence slices (session ids + turn ranges)
3. CLI prints a compact “resume view”:
   - Feature status + next steps
   - recent commits with titles + summaries
   - “open evidence” commands for drilldown

**Acceptance criteria**
- Output is < ~2 screens by default (readable).
- No full transcripts are loaded unless explicitly requested.

#### J2) Recent activity log (fast resume / debug)
**User action**
```bash
ctx context --log 20
```

**System behavior (behind the scenes)**
1. CLI queries the local SQLite index for the **derived activity log** entries for the active feature/branch.
2. If missing, it calls server `GET /repos/:repoId/context/log?feature=...&limit=20`.
3. The system returns *condensed* log entries (not full conversation):
   - observation: what changed / what was discovered
   - decision: key choice (if any)
   - action: what was done (tests run, files touched)
   - result: pass/fail, next step
   Each entry includes links back to session+turn range.

**Acceptance criteria**
- It’s fast (sub-second from local cache; a few seconds remote).
- Each entry is traceable to a session slice (“source of truth”) but doesn’t require reading it.

#### J3) Repo/feature metadata view (structure + policies)
**User action**
```bash
ctx context --metadata
```

**System behavior (behind the scenes)**
Returns a concise snapshot:
- repo id, org, permissions
- active feature streams
- mapping rules
- capture status (which adapters are enabled)
- retention / redaction policy
- known “branches/experiments” and status (`active/merged/abandoned`)

**Acceptance criteria**
- A developer can diagnose “why aren’t my sessions being captured?” from this output.

#### J4) Full roadmap view (handoff / onboarding)
**User action**
```bash
ctx context --full
```

**System behavior (behind the scenes)**
1. Server composes a “roadmap view” from Feature Overviews + merge synthesis commits:
   - objectives
   - milestones completed
   - active experiments/branches
   - major architectural decisions
2. CLI prints a structured roadmap suitable for onboarding a new dev.

**Acceptance criteria**
- A new developer can understand the big picture without opening any transcripts.

---

### Flow K — Experiments/branches + merge synthesis (GCC-inspired)

**Goal:** Try alternative approaches without polluting the main feature stream, then integrate results with a synthesis commit (what was tried, what was learned, what was merged).

#### K1) Start an experiment branch (two supported ways)

**User action (Git-native)**
```bash
git checkout -b experiment/onboarding-cache
ctx feature summary onboarding --set-status "experiment: onboarding-cache"
```

**OR user action (ctx-native alias)**
```bash
ctx branch experiment/onboarding-cache --feature onboarding \
  --purpose "Reduce onboarding latency via caching" \
  --hypothesis "Cache profile + org lookup for 60s reduces p95 latency"
```

**System behavior (behind the scenes)**
1. Creates/updates a **Branch/Experiment Summary** record:
   - parent branch
   - purpose / hypothesis
   - evaluation criteria (what tests/metrics prove success)
   - status = active
2. Routes subsequent ctx commits on this branch to:
   - same repo, but tagged `branch=experiment/onboarding-cache`
   - (optional) separate “experiment stream” for filtering

**Acceptance criteria**
- `ctx log --feature onboarding` can hide experiments by default, but can include them via `--include-experiments`.

#### K2) Merge the experiment (create synthesis commit)
**User action**
```bash
# after PR merge, or explicitly
ctx merge experiment/onboarding-cache
```

**System behavior (behind the scenes)**
1. Server reads:
   - experiment branch summary
   - ctx commits on that branch
   - linked git commits/PR metadata
2. Generates a **synthesis context commit** on the main feature stream containing:
   - what was tried
   - what worked / didn’t
   - what was integrated (files/symbols, or PR link)
   - what was abandoned and why
   - updated next steps
3. Updates Feature Overview milestones and marks experiment status:
   - `merged` or `abandoned`

**Acceptance criteria**
- A future developer can learn “why we chose approach A over B” without digging through all experiment transcripts.

---

### Flow L — Proactive commit suggestions (“auto-suggest”, GCC-inspired)

**Goal:** Reduce “I forgot to commit context” failure mode.

#### L1) Enable/disable
**User action**
```bash
ctx config set proactive_commits true
```

**System behavior (behind the scenes)**
- Stores config locally (`.ctx/config.json`) and pushes repo policy to server (optional).
- Adapters start emitting “milestone events”:
  - tests passed
  - git commit created
  - long idle after large diff
  - session ended after N turns

#### L2) Suggest and accept
**User action**
User sees:
> “Suggestion: create a ctx commit for session S-B (email verification milestone).”

Then runs:
```bash
ctx commit -m "Onboarding: email verification milestone" --from-session S-B
```

**System behavior (behind the scenes)**
- CLI generates a context commit + evidence slice pointers, links to current git HEAD if available.
- Suggestion engine records this acceptance for future tuning.

**Acceptance criteria**
- Suggestions are helpful, not spammy (configurable thresholds).
- User can dismiss suggestions and pause capture.

---

### Flow M — Optional GCC import/export compatibility

**Goal:** Allow teams already using `.GCC/` to interoperate with CtxHub, and allow CtxHub users to export a “local GCC view” for agent-friendly files.

#### M1) Export a `.GCC/` view
**User action**
```bash
ctx export gcc --feature onboarding --out .GCC
```

**System behavior (behind the scenes)**
- Generates:
  - `main.md` from Feature Overview + merged synthesis commits
  - `commit.md` from context commits (flattened)
  - `log.md` from condensed activity log (last N)
  - `metadata.yaml` from repo/feature metadata
  - per-branch `branches/<name>/summary.md` for experiments

#### M2) Import from `.GCC/`
**User action**
```bash
ctx import gcc .GCC
```

**System behavior (behind the scenes)**
- Parses `.GCC/` files and converts:
  - roadmap → Feature Overview
  - commit.md entries → Context Commits (no transcripts, but still useful “why” artifacts)
  - log.md OTA entries → condensed activity log entries
  - metadata.yaml → repo/feature metadata

**Acceptance criteria**
- Import does not require exact 1:1 parity; it must be “best effort” and safe.
- Imported data is clearly marked as “imported from GCC” with source references.



---

## References

- Git Context Controller (GCC): https://github.com/faugustdev/git-context-controller
- GCC file formats reference: https://raw.githubusercontent.com/faugustdev/git-context-controller/dev/references/file_formats.md
