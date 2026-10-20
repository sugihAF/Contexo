# ctx contract v1: normative summary

This file summarizes the machine contract between `ctx` and programmatic
callers (the Contexo VS Code extension first). It is a summary, not the
source of truth. WP-0.1 wrote it in wave 0 and WP-2.2 finalizes it.

## Sources and precedence

- Contract page: `/Users/sugih/Codes/llm-wiki/wiki/concepts/contexo-vscode-mvp-contract.md`
  (go_types, ts_types, error envelope, page states, shared fixtures).
- Plan page: `/Users/sugih/Codes/llm-wiki/wiki/concepts/contexo-vscode-extension-mvp-plan.md`,
  section `## Contract errata` (E1-E41). The errata override the contract page.
- Page hashes recorded at WP-0.1 setup (`shasum -a 256`):

```
CONTRACT_SHA256=86043118d56a3828857b4431f5a9c985226b4e3834e0452967191a835f849850
PLAN_SHA256=c159a3b6e663f5e0237b6d8789ba1dcb6d400b1c543a953e13956cbfd2b8aaa8
```

**Precedence (E1).** Contract matters are wire JSON shapes and key order,
field names, error codes, exit numbers, capability names, page states, SCM
and tree presentation rules, the on-disk layout under `.contexo/.sync`, and
the frozen Go/TS identifiers of E4. For these the order is: Contract errata >
contract page > WP section > Global Constraints > Decisions > Review Focus.
For everything else (owned files, setup commands, acceptance commands, merge
order) the WP section wins. A WP that finds a conflict follows this order,
records the ruling in its status file and never edits another WP's files.
The scratch prototype (`contract-probe`) is informational only (E40).

## Packages

| Package | Holds | Imports |
|---|---|---|
| `internal/syncops/contract` (import path `github.com/sugihAF/contexo/internal/syncops/contract`) | wire types, error/exit table, `Classify`, `ClassifySides`, `SCMGroup`, `WriteDoc`/`WriteEnvelope`, `TimeOf`/`TimePtr`, canonical fixtures in `testdata/v1/` | standard library only |
| `internal/syncops` | `Env`, `Author`, `Remote`, `NewEnv`, `(Env).Online`, `RequireHub`, `NotImplemented`/`New`/`Wrap`, `SidecarFact`/`ApplySidecar`, one file per engine | never internal/config, internal/syncstate, internal/ui or internal/cli |
| `internal/syncops/syncopstest` | `FakeRemote`, `NewFakeRemote(FakeOptions)` | syncops, sync, contract |

`syncopstest` imports `syncops`, so a test in this directory that uses
`FakeRemote` must be in package `syncops_test`.

## Additive-only rule

Frozen after WP-0.I, additions only (never rename, retype or remove): the
contract package and its fixtures, `syncops/{doc,types,env,errors}.go`,
`overlay.go`, the exported APIs of internal/syncstate and internal/fsutil, the
`sync.Client` methods, the ui exit table, cli/root.go registration and
cli/capabilities.go. Identifiers are exactly the E4 table. Engine option and
result types may gain fields; the sanctioned additions are
`PushOptions.Feature/Tag/Type` and `GuardResult.Bypassed` (WP-1.2) and
`RepairOptions.Budget/Concurrency` plus RepairResult fields (WP-1.5).

On the wire, additive changes keep `contract_version` 1: new optional fields,
new commands or flags announced by a new Capability, new ErrorCode /
WarningCode / NoteCode / SkipReason values, new ServerCapability values.
Breaking changes bump `contract_version` to 2: removing, renaming or
retyping a field, changing a field's meaning, a new PageState or
ConflictKind, changing an exit-code category, or changing the
one-document-per-invocation rule. Fixtures are versioned by directory
(`testdata/v1`, later `testdata/v2`).

## Exit codes and error codes (E2, E17)

24 stable codes. The envelope code is authoritative; the exit code is
`contract.ExitCodeFor(code)` (unknown codes give 1).

| Exit | Codes | Retryable |
|---|---|---|
| 0 | ok | |
| 1 | internal | no |
| 3 | usage, invalid_path | no |
| 4 | not_authenticated, auth_rejected, forbidden | no |
| 5 | not_initialized, repo_not_configured, server_unresolved | no |
| 6 | conflict, stale | no |
| 7 | network, server_error, rate_limited | yes |
| 8 | repo_not_found, page_not_found, base_unavailable, sha_not_found | no |
| 9 | distill_required, not_resolvable | no |
| 10 | server_unsupported, not_implemented | no |
| 11 | quota_exceeded | no |
| 12 | busy | yes |

Exit 2 is never used deliberately: it is a Go panic and maps to internal.
130/143 mean the process was killed. `not_implemented` is returned only by a
hidden wave-0 stub command (`syncops.NotImplemented`, advice `["ctx update"]`);
no release returns it for an advertised capability.

## Error envelope (E2(3), E20, E21)

- Emitted only when the executed command's `--json` is true, when the raw
  argv holds `--json` or `--json=true` before `--`, or for `ctx show` (its
  hidden `--json` flag defaults to true). Without `--json`, failures keep the
  human rendering and exit 1.
- Exactly one line, the last non-empty line of stderr:
  `{"schema":"ctx.error.v1","exit_code":N,"error":{"code","message","advice":[],"retryable","http_status"?,"details"?}}`.
  There is no `contract` key. `advice` is `[]`, never null. `http_status`
  appears only when an HTTP response caused the error; `details` only for
  codes that have a details type (`contract.NewDetails`).
- `exit_code` equals the process exit code. On failure stdout is empty,
  except `push` with conflicts, which writes its stdout document and the
  envelope (exit 6).
- `cmd/ctx/main.go` is `os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))`.
  `ui.ToContractError` maps any error chain: a `*contract.Error`; else the
  first error with a duck-typed `ErrorCode() string` (plus optional
  `ErrorAdvice`, `ErrorDetails`, `HTTPStatus`); else `*url.Error`,
  `net.Error` or `context.DeadlineExceeded` as network; else cobra parse
  messages as usage; else internal.
- Wave-1 engines return `*contract.Error` directly; contract times are built
  only through `contract.TimeOf` / `contract.TimePtr` (E16).

## Capabilities (E13, E18, E19)

ctx capabilities (`ctx version --json`), v1 list in contract order:

1. status.json
2. status.offline
3. show.base
4. show.server
5. show.sha
6. pull.json
7. push.json
8. push.paths
9. push.distill_guard
10. resolve
11. errors.envelope

Optional (outside the v1 list): `repair`. The extension requires all v1
names except `show.sha`, and never requires or calls `repair`.

- Advertised = the registered subset, ordered by `contract.Capabilities` then
  `contract.OptionalCapabilities`; never sorted alphabetically.
- Each owner registers its names with `registerCapability(c)` from an
  `init()` in its own file; it panics on an unknown name or a duplicate.
  A wave-0 build advertises only `errors.envelope`.
- Reserved owners: status.json, status.offline -> WP-1.1; push.json,
  push.paths, push.distill_guard -> WP-1.2; pull.json -> WP-1.3; show.base,
  show.server, show.sha, resolve -> WP-1.4; repair -> WP-1.5.

Server capabilities (`X-Contexo-Capabilities`): `pages.at`,
`push.expect_absent`, `write.path_sha`. `pages.etag` is reserved and never
advertised or used. ctx learns them only from response headers through
`sync.Client.ServerInfo()` (known=false before any response; a response
without the header means known=true with no capabilities) and never calls
`/health`.

## HTTP error mapping (E14 + rulings)

Every `sync.Client` request goes through one private `do()` that sets
`Authorization` and `X-Contexo-Client` (env `CONTEXO_CLIENT`, then
`SetClientName`, then `ctx-cli`), records the server headers and turns a
transport failure into `*sync.NetworkError` (code network). Every non-success
answer becomes a `*sync.HTTPError{Status, Code, ServerCode, Body, Msg,
Details}` built by `sync.NewHTTPError`. `Error()` is the legacy per-method
text (byte-stable), `Unwrap()` the legacy sentinel, and `Is()` also matches
`ErrRepoNotFound`, `ErrSHANotFound`, `ErrServerUnsupported` and
`ErrPageNotFound` by Code.

| Status | Code |
|---|---|
| 400 | invalid_path iff the JSON body code is invalid_path; otherwise usage (incl. invalid_sha) |
| 401 | auth_rejected |
| 402 | quota_exceeded, details `{quota_code, limit, upgrade_url?}` from the body |
| 403 | forbidden |
| 404, JSON with code page_not_found / repo_not_found / sha_not_found | that code |
| 404, JSON without such a code | repo_not_found if error == "repo not found"; else sha_not_found on an `?at` read; else page_not_found |
| 404, non-JSON | server_unsupported (details missing `[pages.at]` on an `?at` read) |
| 429 | rate_limited |
| 5xx, garbled 2xx body | server_error |
| 409 and any other status | never conflict; see the deviations table |

`ReadPageAt` requires the `X-Contexo-At` echo to equal the requested sha;
otherwise the error is server_unsupported (`errors.Is ErrServerUnsupported`,
details missing `[pages.at]`). Push conflicts (409) are data in
`PushResponse.Conflicts`, never an error; `stale` is computed client-side.

## On-disk layout

The source of truth is `internal/syncstate/layout.go` (WP-0.4). Summary,
everything under `<root>/.contexo/.sync/`, written only by ctx:

- `.gitignore` containing `*`, created before the first write (E41)
- `state.json`: format unchanged; `page_shas` = base sha; `last_pull_sha` still written
- `base/<relpath>`: base snapshots
- `bases.json`: `{"schema":"ctx.bases.v1","pages":{rel:{sha,hash,synced_at}}}`
- `conflicts.json`: `{"schema":"ctx.conflicts.v1","conflicts":{rel:{server_sha,base_sha?,kind,source,detected_at,has_content}}}`
- `conflicts/<relpath>`: sanitized server content, present iff has_content
- `lock`: `{pid,host,command,started_at}`
- `backup/`: written by `resolve --theirs`
- `remote.json`: reserved, never written or read in v1 (E8)

Write order per operation: working files, base files, `conflicts/<path>`,
`bases.json`, `conflicts.json`, `state.json`, each index once, atomically.
push, pull, resolve and repair hold the lock (busy, exit 12, after
`CONTEXO_LOCK_TIMEOUT`); online status only TryLocks inside EnsureBase;
`status --offline` and `push --dry-run` write nothing (E5).

## Engine stubs and wave-1 ownership

Each engine is one stub file plus one inherited stub test. The owner fills in
the file, may add fields, replaces the stub assertion, and never renames or
removes a frozen identifier.

| Files | Owner | Entry point (stub behaviour) |
|---|---|---|
| status.go, status_test.go | WP-1.1 | `Status(ctx, env, StatusOptions{Offline}) (*contract.StatusDoc, error)` (not_implemented) |
| push.go, push_test.go | WP-1.2 | `Push(ctx, env, PushOptions{Paths, Message, DryRun, NoDistill, Resolved}) (*contract.PushDoc, error)` (not_implemented) |
| guard.go, guard_test.go | WP-1.2 | `CheckGuard(env, batch) (*GuardResult{Required, Details}, error)` (Required false) |
| pull.go, pull_test.go | WP-1.3 | `Pull(ctx, env, PullOptions{}) (*contract.PullDoc, error)` (not_implemented) |
| show.go, show_test.go (+ internal/cli show.go, show_test.go) | WP-1.4 | `Show(ctx, env, ShowOptions{Path, At}) ([]byte, error)` (not_implemented) |
| resolve.go, resolve_test.go (+ internal/cli resolve.go, resolve_test.go) | WP-1.4 | `Resolve(ctx, env, ResolveOptions{Path, Strategy, IfServerSHA}) (*contract.ResolveDoc, error)` (not_implemented) |
| repair.go, repair_test.go (+ internal/cli repair.go, repair_test.go) | WP-1.5 | `Repair(ctx, env, RepairOptions{DryRun}) (*RepairResult, error)` (not_implemented) |
| ensurebase.go, ensurebase_test.go | WP-1.5 | `EnsureBase(ctx, env, EnsureBaseOptions{Listing, Paths, Locked}) (*EnsureBaseResult, error)` (empty result, zero requests) |

- Frozen tests (never weakened): `env_test.go`, `overlay_test.go`. The shared
  stub helpers (`wp01StubEnv`, `wp01AssertNotImplemented`) live in
  `env_test.go`, so rewriting one stub test never breaks another.
  `ensurebase_test.go` is package `syncops_test` because it uses `FakeRemote`.
- `TestRepairResultJSONKeyOrder` pins the E18 shape of `ctx repair --json`
  (`schema, dry_run, server_capabilities, backfilled{path,sha},
  repaired{path,old_sha,new_sha,reason}, skipped{path,reason}, unverified,
  warnings`; arrays `[]`, never null) and stays when the stub goes.
- Hidden CLI stubs (`ctx show`, `ctx resolve`, `ctx repair`) run
  `syncops.RequireHub` and return `syncops.NotImplemented`. Owners replace
  RunE and set `Hidden = false`.
- `ApplySidecar` (E9) is the one online override: a modified result becomes a
  conflict of the sidecar's kind (both_modified when empty) iff a sidecar
  exists whose server sha differs from the base sha; nothing else changes.
- `FakeRemote` mirrors gitstore.Write and handler.Push/Pull/ReadPage with the
  WP-0.2 fixes: one commit per changed file, sha `hex(sha1("fake:"+n))`,
  time `2026-01-01T00:00:00Z + n s`; parent check only when parent and
  current are both set (ancestor_content when the parent holds the path);
  expect_absent conflicts unless `NoExpectAbsent`; a no-op returns the path's
  last commit (HEAD under `LegacyNoopHeadSHA`); errors are the same
  `*sync.HTTPError` values a real client returns (a test pins this against
  `sync.Client` on httptest).

## Fixtures (E31, E32, E33)

- Canonical set: `internal/syncops/contract/testdata/v1/`, with
  `testdata/.gitattributes` (`* -text`) outside v1 so Windows checkouts keep
  the hashed bytes. WP-0.I/WP-1.7 vendor v1 into contexo-vscode
  `test/fixtures/contract/v1/` with a SOURCE note; the extension never edits it.
- `manifest.json`: `{"contract_version":1,"fixtures":[{file, schema, command, stream, exit_code, sha256}]}`,
  sorted by file, listing every file under v1 except itself. `stream` is
  `stdout`, `stderr`, `raw` or `data`. `error.*.json` and `*.stderr.json`
  are `stderr` (one `\n`-terminated line); `raw/*.md` are `raw` with schema
  null, command `show`, exit_code 0; `classify.cases.json` is `data` with
  schema, command and exit_code null. `go test ./internal/syncops/contract/ -run TestManifest -update`
  regenerates it (2-space indent, trailing newline). Consumers select files by
  stream and schema, never by extension.
- `classify.cases.json`: `{"contract_version":1,"cases":[{id, desc, input:{local:{kind,hash?}, base:{kind,sha?,hash?}, remote:{kind,sha?}}, expected:{state, conflict_kind?, notes:[]}, scm_group}]}`.
  21 rows: r01-r19 plus r03b and r12b. `expected.state` `""` means no entry
  (r07). `scm_group` is null where `SCMGroup` returns `""` (unchanged,
  unverified, r07).
- Hygiene: emails only `@acme.dev`; no access tokens; page and commit SHAs
  are full 40-hex lowercase; times are `YYYY-MM-DDTHH:MM:SSZ`; no fixture lists
  `pages.etag` or `remote.source` `cache`; 24 `error.<code>.json` files;
  version.release and version.dev list the 11 v1 capabilities then `repair`.

## Deviations pending review

Decisions the errata do not cover. [HUMAN] confirms or vetoes each row before
WP-0.I.

| # | Decision | Why | Cost if wrong |
|---|---|---|---|
| 1 | HTTP statuses outside E14's list: an unexpected 409 and other 4xx map to internal; 1xx, 3xx and a garbled 2xx map to server_error. | E14 names 400/401/402/403/404/429/5xx and forbids mapping 409 to conflict, but is silent on the rest. An unlisted 4xx means client and server disagree on the protocol (a bug, not transient). An informational, redirect or unreadable success answer is a server or proxy fault like E14's garbled body. | The extension shows an internal error (exit 1, no retry) where a retry might have helped, or retries (exit 7) a status that will never succeed. |
| 2 | Codeless JSON 404s on non-page routes (members, invite keys, history, diff) follow E14's fallback: page_not_found (exit 8). Legacy sentinels and texts are kept. | One mapping function for every route; E14 does not limit the fallback to page routes. Existing callers keep working through the legacy sentinels and byte-stable `Error()` text. | A `--json` failure of those commands reports page_not_found instead of a more specific code. The extension never calls them. |
| 3 | `ReadPage` keeps sentinel `ErrPageNotFound` for every 404 (legacy behaviour) while Code follows E14, and `HTTPError.Is` also matches the code sentinels by Code. | diff.go and push_preview.go treat `errors.Is(err, ErrPageNotFound)` as "new page"; the envelope still gets the precise code (repo_not_found, server_unsupported). New code can test any code sentinel uniformly. | A missing repo satisfies both `errors.Is(ErrPageNotFound)` and `errors.Is(ErrRepoNotFound)`, so legacy callers keep treating it as a new page, as today. |
| 4 | `sync.NewHTTPError(status, body, msg, sentinel, at)` is exported (additive). | `syncopstest.FakeRemote` must build errors identical to the real client's, and the sentinel field is unexported. | One more frozen exported name on the sync surface; a later mapping change must keep its signature. |
| 5 | `VersionDoc` commit and date carry goreleaser `ShortCommit` and `CommitDate` (7-hex, RFC 3339), not 40-hex. | `.goreleaser.yaml` sets `version.Commit`/`version.Date` that way; the 40-hex rule is for knowledge-base page and commit SHAs, not build metadata. | A TS schema that demands 40-hex for `commit` rejects every real ctx build. |
| 6 | `classify.cases.json` uses the shape above, with `scm_group` null where `SCMGroup` is `""` (unchanged, unverified, r07). | The contract describes the rows but not their JSON; null separates "no group" from a group name. | WP-1.7's groups test must map `""` to null; a different expectation fails that test, not the extension. |
| 7 | Manifest `command` is the subcommand name; raw entries are command `show`, exit_code 0; each error fixture names one representative raiser. | E32 fixes the fields but not their values; most codes are raised by several commands. | A consumer that filters error fixtures by command sees one representative per code. |
| 8 | `not_implemented` carries advice `["ctx update"]`. | E17: the extension treats it like a too-old ctx and offers Update ctx; advice lists shell commands that fix the error. | A pre-release build user is told to update when no newer release exists yet. |
| 9 | An unknown duck-typed `ErrorCode()` maps to internal; an empty one is ignored and the chain walk continues. | The envelope code must be one of the 24 known codes, consistent with `ExitCodeFor` (unknown gives 1). An empty code must not hide a coded error deeper in the chain. | A code a parallel WP adds before the table knows it surfaces as internal (exit 1) until the table is extended. |
| 10 | `EnvelopeMode` is false for `ctx mcp`, `ctx capture turn` and shell completion, even with a raw `--json`. | These surfaces own their streams (MCP stdio JSON-RPC; capture hooks keep stdout byte-empty; cobra completion). | A `--json` failure there exits 1 with human text instead of an envelope. |
| 11 | `http_status` carries the real status, so a garbled 200 reports 200. | It is defined as the status of the response that caused the error; the real value keeps FakeRemote/client parity exact. | Logs show server_error with http_status 200, which looks odd; behaviour follows the code (retryable). |
