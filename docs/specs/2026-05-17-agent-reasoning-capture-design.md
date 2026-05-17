# Agent Reasoning Capture — Design

Status: approved 2026-05-17
Owner: sugihAF

## Problem

When Dev A researches an approach (e.g. "use Stripe Billing, not Connect") and Dev B picks up the implementation, Dev B's agent often re-derives the reasoning from scratch — and sometimes reverses validated decisions. Today's Contexo concept pages capture the *conclusion* but rarely the *causal trail* that justifies it. The "raw" reasoning is missing.

We also see this in the dashboard: only `concept` and `entity` pages render. `source` (the page type designed for distilled session writeups) and `analysis` are silently dropped. So even if reasoning trails existed, no one would see them.

## Goal

Make it cheap and natural for the agent to capture a structured **reasoning trail** that travels with every concept page it pushes — so the next dev's agent reads not just "we chose A" but "we chose A because we ruled out B for reason X, and dead-end Y cost us 30 minutes."

## Non-goals

- **No raw transcript archiving.** Contexo continues to store distilled markdown, not JSONL conversation dumps. Token cost on consumption matters.
- **No new Anthropic API dependency** in the Contexo CLI or server. The agent that just held the conversation does the distillation in its own context.
- **No "every dev needs their own API key"** friction.
- **No web-side authoring.** Dashboard stays read-only.

## Approach (chosen)

**Agent-as-distiller**, plus a cheap append-only local buffer that gives the distiller raw material to work from.

```
Stop hook → ctx capture-turn → append to .contexo/raw/sessions/_pending/<sid>.jsonl
                                (no LLM, < 50 ms)

ctx_push (via MCP) → if buffer + concept pages → return PUSH_PAUSED directive
                     → agent writes source page via ctx_write_page(type=source)
                     → agent re-invokes ctx_push(distill_done=true)
                     → push patches sources: frontmatter, archives buffer, commits
```

The distillation LLM call is the agent's *own* turn — no separate API key, no Contexo server cost, no new dependency.

## Source page shape

Structured by template (delivered to the agent in the `PUSH_PAUSED` directive):

```markdown
## Decision
## Why this approach
## Rejected alternatives
## Path of inquiry
## Dead-ends
## Open questions
## Sources
```

Target size: 3–5 KB. Linked from concept pages via the existing `sources:` frontmatter field.

## Architecture

### New packages / files (in this repo)

| Path | Role |
|---|---|
| `internal/capture/buffer.go` | Append-only JSONL writer; bounded (4KB/2KB per-turn truncation, 500-turn cap, mtime-based prune of 30+ day pending files); list/archive helpers. |
| `internal/capture/transcript.go` | Reads Claude Code's transcript JSONL fixture and extracts the most recent `(user, assistant, tools)` tuple. |
| `internal/cli/capture.go` | `ctx capture-turn --session ID --transcript PATH [--cwd DIR]` — Stop-hook target. Silently no-ops outside `.contexo/` projects. |
| `internal/cli/hooks.go` | `ctx hooks install` / `ctx hooks uninstall` — manages `.claude/settings.json` Stop entry. Idempotent. Preserves other hooks. |
| `internal/mcp/tools.go` (mods) | `ctx_push` gains the `PUSH_PAUSED` handshake + `distill_done` round-trip. New tool `ctx_capture_session`. |
| `internal/sync/server_distill.go` | Stub client for the future server-side fallback. Returns `"not implemented"` for v1. |
| `internal/server/handler/handler.go` (mods) | New endpoint `POST /v1/repos/:id/sync/distill` → `501 Not Implemented`. |
| `internal/cli/init.go` (mods) | `ctx init` appends `raw/sessions/_pending/` to `.contexo/.gitignore`. |

### Schema

No changes. `source` page type and `sources: []` field already exist in `internal/schema/page.go`.

### Env vars (CLI side)

| Var | Effect |
|---|---|
| `CONTEXO_CAPTURE_DISABLE=1` | `ctx capture-turn` no-ops. No buffer written. |
| `CONTEXO_DISTILL_DISABLE=1` | Capture continues; push handshake never fires. |

### CLI flags

| Flag | Effect |
|---|---|
| `ctx push --no-distill` | One-time skip of handshake. |
| `ctx push --fallback-server` | Routes distillation to server endpoint. Currently errors "not implemented" (Phase 4). |

## Data flow

### Stop hook (per assistant turn)
```
Claude Code → ~/.claude/settings.json Stop hook → ctx capture-turn
  → walk-up to .contexo/, no-op if absent
  → tail Claude Code transcript JSONL, extract latest (user, assistant, tools)
  → truncate (4KB assistant, 2KB user)
  → append one JSONL line to _pending/<session-id>.jsonl
  → mtime-prune _pending/*.jsonl older than 30 days
  → exit 0
```

### Push handshake
```
agent → tools/call ctx_push(feature=X)
  → if buffer (most recent _pending/*.jsonl within 6h) non-empty AND
    push batch includes any concept|analysis pages AND
    CONTEXO_DISTILL_DISABLE not set AND
    --no-distill not set AND
    distill_done arg not true:
      return ToolResult with text:
        "<PUSH_PAUSED reason=distill_required>
         Before pushing, write a source page (template + buffer inlined).
         Then re-invoke ctx_push(..., distill_done=true).</PUSH_PAUSED>"

agent → ctx_write_page(type=source, slug=YYYY-MM-DD-<topic>, body=...)
agent → ctx_push(feature=X, distill_done=true)
  → find newest source page in raw/sessions/ created in last 6h
  → if missing: error "distill_done set but no source page authored — write one first"
  → for each concept|analysis page in batch: append source slug to sources: frontmatter
  → move _pending/<session-id>.jsonl → _pending/_archive/<session-id>.jsonl
  → POST /v1/repos/:id/sync/push with both concept + source pages in one commit
  → return "Pushed N+1 pages; HEAD=..."
```

### ctx_capture_session (explicit, no push)
Same template + buffer payload as the handshake, but doesn't push. For mid-session checkpoints.

## Edge cases (summary; full table in design conversation)

- Hook fires outside `.contexo/` → exit 0 silently.
- Buffer write fails → stderr log, exit 1, don't fail user's turn.
- Double-fire on same turn → dedupe by computed turn index.
- Multiple sessions on same project → unique session-ids, separate buffers.
- Agent ignores `PUSH_PAUSED` → buffer stays pending, next push retries.
- `distill_done=true` without source page → error preventing loop.
- Network failure mid-push → existing retry-safe behavior unchanged.
- Stale buffer > 6h → not auto-distilled, requires explicit invocation.
- Secrets in buffer → template instructs agent to redact; buffer never sent over wire.

## Testing

- Unit: buffer (append/list/archive/prune/truncate/dedupe), transcript parser, capture-turn CLI, hooks install/uninstall, push handshake (3 cases: normal, paused, completion), capture_session tool.
- Integration: end-to-end MCP driver — seed buffer + concept page → drive ctx_push → assert PUSH_PAUSED → simulate ctx_write_page → re-drive ctx_push(distill_done=true) → assert outbound HTTP body has both pages + patched sources:.
- Manual: dogfood in this repo after Phase 1 lands.

## Rollout (phased)

| Phase | Scope | Repo |
|---|---|---|
| 0 | Capture foundation (buffer, capture-turn, hooks) | Contexo |
| 1 | Push handshake + ctx_capture_session | Contexo |
| 2 | Server fallback stub (501) | Contexo |
| 3 | FE render source/analysis + sources: link surfacing | contexo-web |
| 4 | Real server-side distillation (deferred) | Contexo |

Phase 0 must ship before Phase 1. Phase 3 can ship in parallel.

## Out of scope (this spec)

- Real server-side distillation (Phase 4).
- Regex secret redactor at capture time (v2).
- Cross-platform Stop-hook integration tests beyond CI matrix coverage of the Go code itself.
- Distillation quality benchmarks.
- Non-Claude-Code MCP client hook helpers (Cursor, Windsurf).
