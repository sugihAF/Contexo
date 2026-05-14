# CtxHub MVP-1 Gap Analysis

**Date:** 2026-02-28
**Compared against:** ctxhub_docs/user_story.md, ctxhub_docs/technical_requirements.md, ctxhub_docs/plan.md

---

## Coverage Summary

| Category | Implemented | Partial | Missing | Notes |
|----------|:-----------:|:-------:|:-------:|-------|
| CLI Core Commands | 14/20 | 3 | 3 | Missing: remote, auth login, status, codex exec, open-session, config set |
| Data Schemas | 3/6 | 1 | 2 | Missing: branch_summary, repo_policy. Commit schema simplified |
| Storage Layer | 5/5 | 0 | 0 | SQLite, BoltDB, JSONL, Postgres, S3 all implemented |
| Capture Adapters | 3/4 | 0 | 1 | Missing: Cursor (MVP-2) |
| Recorder/ctxd | 1/1 | 0 | 0 | Fully working embedded recorder |
| Redaction | 1/1 | 0 | 0 | Working with AWS key/secret, generic tokens, bearer, github, private key |
| Server API | 8/20+ | 0 | 12+ | Missing: feature overview, context levels, branches, policy, git-links, etc. |
| MCP Resources | 6/12+ | 0 | 6+ | Missing: context levels, symbol blame, search, feature overview MCP |
| Security | 1/5 | 0 | 4 | API keys only. Missing: encryption, ACL, retention, session visibility |
| GCC Features | 0/4 | 0 | 4 | All GCC-inspired features are future scope |

**Overall: ~60% of MVP-1 core scope is implemented. The foundation is solid but several features need enhancement.**

---

## Detailed Gaps

### 1. Context Commit Schema (CRITICAL - Partial)

**Current:** Commits store only: schema, commit_id, title, feature, created_at, evidence[{session_id}]

**Missing from ctx.commit.v1 spec:**
- `summary` (array of bullet strings)
- `decisions` (array of {decision, why})
- `next_steps` (array of strings)
- `changes.git` (linked_commits, pr)
- `changes.files_touched` (path, symbols)
- `changes.tests` (cmd, result, ts)
- `author` (name, tool)
- `branch`, `branch_purpose`, `previous_progress`
- Evidence should include `source` and `turns` range

**Impact:** Without decisions/summary/next_steps, the commit is just a title with a session pointer — far less useful for handoffs.

### 2. CLI Commands Missing (IMPORTANT)

| Command | Status | Priority |
|---------|--------|----------|
| `ctx remote add <url>` | Missing | High (needed for push/pull to work with real server) |
| `ctx auth login` | Missing | High (needed for server auth) |
| `ctx status` | Missing | Medium (shows init state, remote, capture status) |
| `ctx codex exec "<task>"` | Missing | Medium (wrapper for Codex CLI) |
| `ctx open-session <commit-id>` | Missing | Medium (drill down from commit to session) |
| `ctx config set <key> <value>` | Missing | Medium (configure redaction, proactive commits) |
| `ctx context --full` | Missing | Low (full roadmap view) |
| `ctx feature summary` | Missing | Low (view/update feature overview) |
| `ctx session tail` | Missing | Low (live tail of current session) |

### 3. Commit Creation Flags Missing (IMPORTANT)

**Current:** `ctx commit -m "title" --feature <name>`

**Missing flags:**
- `--from-session <id>` — specify which session to link
- `--turns <from>..<to>` — specify turn range for evidence
- `--since <duration>` — auto-select by time window
- `--summary` / `--decisions` / `--next-steps` — structured fields
- `--author` — author attribution

### 4. Server API Endpoints Missing (MODERATE)

These are specified in technical_requirements.md section 5.2:

| Endpoint | Status |
|----------|--------|
| `GET /v1/repos/{repoId}/features` | Missing |
| `GET /v1/repos/{repoId}/features/{feature}/overview` | Missing |
| `PUT /v1/repos/{repoId}/features/{feature}/overview` | Missing |
| `GET /v1/repos/{repoId}/context?level=...` | Missing |
| `POST /v1/repos/{repoId}/branches` | Missing (MVP-2+) |
| `GET /v1/repos/{repoId}/branches` | Missing (MVP-2+) |
| `POST /v1/repos/{repoId}/merge-synthesis` | Missing (MVP-2+) |
| `GET /v1/repos/{repoId}/policy` | Missing |
| `PUT /v1/repos/{repoId}/policy` | Missing |
| `POST /v1/repos/{repoId}/git-links` | Missing |
| `GET /v1/repos/{repoId}/git/{gitSha}/related` | Missing |
| `GET /v1/repos/{repoId}/symbols/{symbolKey}/blame` | Missing |

### 5. MCP Resources Missing (MODERATE)

| Resource | Status |
|----------|--------|
| `ctx://repo/<id>/features` | Missing |
| `ctx://repo/<id>/features/<f>/overview` | Missing |
| `ctx://repo/<id>/context?level=feature&feature=<f>` | Missing |
| `ctx://repo/<id>/context?level=log&...` | Missing |
| `ctx://repo/<id>/context?level=metadata` | Missing |
| `ctx://repo/<id>/context?level=full` | Missing |
| `ctx://repo/<id>/blame/symbol/<key>` | Missing |
| `ctx://repo/<id>/search?q=...` | Missing |

### 6. Security Gaps (for production, not MVP-1 blocking)

- No encryption at rest / client-side encryption
- No org/repo-level access control (beyond API key)
- No retention policies
- No session visibility levels (team/private/public)
- No token scopes

### 7. Explicitly Out of Scope for MVP-1 (Expected Missing)

These are correctly placed in MVP-2/3 per plan.md:
- Cursor adapter (MVP-2)
- Web dashboard (MVP-2)
- Branch/experiment management (MVP-2+)
- Merge synthesis commits (MVP-2+)
- Proactive commit suggestions (MVP-2+)
- GCC import/export (MVP-2+)
- SSO/OAuth (MVP-3)
- Search with relevance ranking (MVP-3)
- PR checks (MVP-3)

---

## Recommendations (Priority Order)

### P0 — Must Fix (Core MVP-1 gaps)
1. **Enrich context commit schema** — Add summary, decisions, next_steps, changes, author fields
2. **Add `ctx commit` structured flags** — --summary, --decisions, --from-session, --turns
3. **Add `ctx remote add`** — Configure server URL for push/pull
4. **Add `ctx auth login`** — Store API key for server auth
5. **Fix `ctx link` to accept `--commit` flag** — Currently auto-links to latest, should support explicit

### P1 — Important for MVP-1 completeness
6. **Add `ctx status`** — Show repo init state, remote config, capture status
7. **Add `ctx open-session`** — Drill down from commit to session evidence
8. **Add `ctx codex exec` wrapper** — The Codex adapter can parse but there's no CLI entry point
9. **Add `ctx context --full`** — Full roadmap view
10. **Add server feature overview endpoints** — GET/PUT for feature overviews
11. **Add server git-links endpoints** — POST/GET for git link management

### P2 — Nice to have for MVP-1
12. **Add `ctx config set`** — Runtime configuration
13. **Add `ctx session tail`** — Live session monitoring
14. **Add MCP context-level resources** — Multi-resolution MCP resources
15. **Add MCP symbol blame resource** — ctx://repo/.../blame/symbol/...
16. **Add server context-level endpoints** — GET /context?level=...
