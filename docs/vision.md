# Contexo — Vision

Contexo is **GitHub for AI agent knowledge, scoped per project**.

When a developer researches a topic deeply with an AI agent (e.g. "how should we model the Stripe subscription"), the resulting context — decisions, reasoning, rejected alternatives, open questions — lives only in that one developer's session. Contexo lets the team share that distilled knowledge so every teammate's AI agent starts from the same baseline, without having to re-derive it (and often deriving it worse).

The format is the same as the personal `llm-wiki` knowledge base — markdown pages with frontmatter, layered into index/concept/raw — adapted for multi-developer use: server-backed, versioned, attributable.

---

## The flow it enables

1. Developer A spends a Claude session researching Stripe subscription billing for ChompChat.
2. Claude writes the distilled outcome into `chompchat/.contexo/wiki/concepts/stripe-subscription.md` — what was decided, what was rejected and why, what's next.
3. Developer A says *"sync my Stripe knowledge to contexthub"* OR runs `ctx push --feature stripe`. The page (and the raw session it came from) is uploaded to the project's Contexo server.
4. Developer B starts work on the implementation. Their Claude pulls (`ctx pull`, or the MCP `ctx_pull` tool invoked by the agent itself).
5. Developer B's Claude now sees the same `stripe-subscription` page on first access — including the reasoning section that explains *why* Connect was rejected. It doesn't re-research from scratch and doesn't repeat dead-ends.

---

## Architecture

```
chompchat/                            ← any project repo
└── .contexo/                          ← per-project local knowledge (mirrors llm-wiki)
    ├── config.json                   ← server URL, repo_id, dashboard URL (token lives in credentials.json)
    ├── index.md                      ← always pre-loaded into AI context
    ├── tags.md                       ← tag → page lookup
    ├── raw/sessions/
    │   └── 2026-05-14-stripe-research.md
    └── wiki/
        ├── concepts/
        │   └── stripe-subscription.md
        ├── entities/
        │   └── stripe.md
        └── analyses/

Contexo server (single instance, many projects)
└── $CONTEXO_DATA_ROOT/
    ├── chompchat/                    ← git repo per project
    ├── ralph-loop/
    └── ...
```

Each project's server-side store is a real git repository — history, authorship, timeline, and conflict resolution come for free from git. Identity, membership, invite keys, and the activity feed live in a small SQLite database (`contexo.db`) next to the repos under `$CONTEXO_DATA_ROOT`; git stays the source of truth for the knowledge pages themselves.

---

## Page format

Every markdown file in `.contexo/` carries frontmatter that supports both the local read path and server-side sync:

```yaml
---
schema: ctx.page.v1
slug: stripe-subscription
type: concept          # one of: concept, entity, source, analysis
author: sugihAF        # human who owns this knowledge
agent: claude-opus-4-8 # model that wrote / last-edited the page
created: 2026-05-14T10:00:00Z
updated: 2026-05-14T15:30:00Z
parent_sha: ""         # git sha this revision was built on; "" on first write
sources: [2026-05-14-stripe-research]
related: [chompchat-saas-subscription, stripe-connect-charge-types]
tags: [stripe, billing, subscription]
reasoning_summary: "Rejected Connect (negative-balance ownership); chose Billing + metered"
---
```

`parent_sha` is the key field for safe concurrent edits: when a client pushes a page, it tells the server "I edited the version whose git sha was X." The server accepts only if no one else changed it since X; otherwise returns 409 with both versions for the client (or its AI) to merge.

---

## Layered reading model

The three layers exist to keep token cost bounded. An agent should not pull the whole corpus into context — it should triage from the index down.

| Layer | What | When loaded | Approx size |
|---|---|---|---|
| **Index** | `index.md` + `tags.md` | Always pre-loaded into every session | ≤ 10 KB total |
| **Concept** | `wiki/concepts/*.md`, `wiki/entities/*.md` | On demand, via MCP fetch | 1–3 KB each |
| **Raw** | `raw/sessions/*.md` | Only when a concept page references it via `sources:` and deeper context is needed | 5–20 KB each |

Agents are instructed: read index → identify relevant concepts → read those → drill to raw only if a concept points to one and the question requires the full session.

---

## Agent reasoning — the new layer beyond `llm-wiki`

`llm-wiki` already captures decisions. Contexo adds an explicit **`## Agent Reasoning`** section in concept pages, capturing the *metacognitive trail* — not just what was decided, but the path of inquiry and what was ruled out.

```markdown
## Agent Reasoning
- **Considered** Stripe Connect with destination charges → **rejected**: gives restaurants the negative balance
- **Considered** custom billing → **rejected**: not differentiating, Stripe handles dunning/retry/tax
- **Path of inquiry**: Connect docs → fee-split confusion → read [[stripe-connect-charge-types]] → recognized model mismatch → pivoted to Billing
- **What I didn't try**: third-party billing (Lago, Orb) — flagged in Open Questions
```

This section is what stops Developer B's agent from re-deriving the same dead-ends. Without it, the page would only say "use Billing" and Developer B's agent might re-evaluate, get confused, and waste hours.

---

## Sync model

Sync is **explicit, never automatic**. The developer (or the agent acting on natural-language instruction) decides when knowledge crosses the boundary from "local working memory" to "team knowledge."

Three trigger paths:

1. **CLI**: `ctx push --feature stripe` (or `--tag billing`, `--glob "wiki/concepts/stripe-*"`)
2. **Natural language to agent**: *"sync my Stripe knowledge to contexthub"* → agent invokes the `ctx_push` MCP tool
3. **AI-driven pull at session start**: agent invokes `ctx_pull(feature=...)` when it sees the user start work on a topic, to grab the latest team knowledge before doing anything

`ctx pull` is symmetric on the read side: it brings down only what's changed since the last pull, and it merges into the local `.contexo/` tree without clobbering local edits.

---

## Server-side management

The server is built on git. Every requirement falls out naturally:

| Requirement | Mechanism |
|---|---|
| Latest knowledge | `HEAD` of the server-side repo |
| Timeline | `git log` |
| Author per change | Commit author resolved from the signed-in user (PAT / Google identity) on push |
| Conflict resolution | `git merge` — server returns 409 + both versions if push can't fast-forward |
| Diff / history per page | `git log -- wiki/concepts/<slug>.md` |
| Rollback | `git revert` |

On conflict, the client doesn't have to do the merge manually — the AI itself can help merge prose intelligently (*"here are both versions of the Stripe page; reconcile them"*), which is a much better fit for markdown than line-based diff.

---

## What Contexo is NOT

- **Not a chat transcript archive.** Raw sessions are distilled human-readable summaries (same format as `llm-wiki/raw/sessions/`), not full JSONL conversation logs. If a session wasn't worth summarizing, it doesn't need to be in Contexo.
- **Not automatic.** The system never decides for the developer what to share. Every push is explicit.
- **Not a global knowledge base.** Cross-project knowledge (e.g. "how Anthropic prompt caching works") stays in the personal `llm-wiki`. Contexo is per-project.
- **Not a code-blame tool.** Knowing which commit introduced a bug is `git blame`'s job. Contexo answers a different question: *"what does the team know about this topic?"*

---

## Out of scope (deferred indefinitely)

- Real-time multi-user editing (Google-Docs style). Push/pull is enough.
- A web UI for **authoring** knowledge. A hosted dashboard now browses repos, members, and activity and mints tokens / invite keys — but pages are still authored as local markdown through your agent.
- Full-text search with relevance ranking. `grep` over the corpus is sufficient until proven otherwise.
- Encryption at rest. Per-user token / OAuth auth + HTTPS is the security boundary; the data root and `contexo.db` aren't encrypted.
- Branch/experiment management on knowledge pages. Pages don't fork; they evolve.
