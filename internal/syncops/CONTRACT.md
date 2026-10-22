# ctx contract v1: normative summary

This file summarizes the machine contract between `ctx` and programmatic
callers (the Contexo VS Code extension first). It is a summary, not the
source of truth. WP-0.1 wrote it in wave 0; WP-2.2 finalized it (2026-10-22)
with the errata applied, every wire shape linked to a golden produced by the
real binary, and the Known-deviations table.

No test under internal/syncops reads this file
(`TestNoSyncopsTestReadsContractMD`). The one reader is the e2e suite's
`TestDeviationsDocumented` (internal/e2e), which requires a row in
"Known deviations" for every registered `DEV-n`. The package tests pin the
behaviour summarized here: for example, `TestHTTPMappingUnlistedStatuses`
pins the HTTP mapping table and deviation 1, and the e2e suite pins the
commands end to end.

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
| `internal/syncops` | `Env`, `Author`, `Remote`, `NewEnv`, `(Env).Online`, `RequireHub`, `NotImplemented`/`New`/`Wrap`, `SidecarFact`/`ApplySidecar`, one file per engine | never internal/cli or internal/mcp (both call the engines, so the import would be a cycle). In wave 0 only, also not internal/config, internal/syncstate or internal/ui. The wave-1 engines sit on internal/syncstate (E3), and WP-1.1's status.go may use `*config.Remote` and `ui.ToContractError` |
| `internal/syncops/syncopstest` | `FakeRemote`, `NewFakeRemote(FakeOptions)` | sync, contract (never syncops) |

`syncopstest` never imports `syncops`: the `var _ syncops.Remote =
(*FakeRemote)(nil)` check lives in its test file. A package-`syncops` test
can therefore drive an engine on `FakeRemote` while it overrides the
engine's unexported seams (`ensurebase_test.go` is the in-package example).

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

## Commands and documents (E25, E26)

Every shape below is pinned by a golden that the real binary produced
(`internal/e2e/testdata/contract/`, see "Golden fixtures" below) and by the
canonical hand-written fixture of the same name in
`internal/syncops/contract/testdata/v1/` where one exists. Extension argv is
exactly E25; R is the absolute workspace-folder path. The extension spawns ctx
with `{...process.env, NO_COLOR:'1', CONTEXO_NO_UPDATE_CHECK:'1',
CONTEXO_CLIENT:'contexo-vscode/<ver>'}` minus `CONTEXO_DISTILL_DISABLE` (E26).

| Command (E25 argv) | stdout (schema) | Exit codes (every command may also exit 1 internal and 3 usage) | Goldens |
|---|---|---|---|
| `version --json` (no --root) | `VersionDoc` (`ctx.version.v1`) | 0 | `version.dev.json` |
| `status --json --root R [--offline]` | `StatusDoc` (`ctx.status.v1`); remote problems go to `remote.error` with exit 0 | 0, 5 | `status.empty.json`, `status.all-states.json`, `status.offline-sidecar.json`, `status.offline-nocache.json`, `status.not-authenticated.json`, `status.auth-rejected.json`, `status.network-error.json`, `status.old-server.json`, `status.repo-missing.json`, `status.url-mismatch.json`, `status.captures-blocking.json` |
| `show P --at base\|server\|<sha> --root R` (no --json; always machine mode) | raw page bytes, no added newline | 0, 4, 5, 7, 8, 10 (3 also for invalid_path) | `show.base.raw.md`, `show.server.raw.md` (stream raw) |
| `pull --json --root R` | `PullDoc` (`ctx.pull.v1`); skips exit 0 | 0, 4, 5, 7, 8, 11, 12 | `pull.ok.json`, `pull.skipped-and-adopted.json`, `pull.up-to-date.json` |
| `push --json --yes --root R [-m M] [-- paths...]` | `PushDoc` (`ctx.push.v1`), also on exit 6 | 0, 4, 5, 6, 7, 9, 11, 12 (3 also for invalid_path) | `push.ok.json`, `push.dry-run.json`, `push.nothing.json`, `push.partial-conflict.json` + `push.partial-conflict.stderr.json`, `push.distill-bypassed.json` |
| `resolve P --ours\|--theirs --json --root R [--if-server-sha S]` | `ResolveDoc` (`ctx.resolve.v1`) | 0, 4, 5, 6, 7, 8, 9, 12 (3 also for invalid_path) | `resolve.ours.json`, `resolve.theirs.json` |
| `repair [--dry-run] --json --root R` (ctx only; the extension never calls it) | `syncops.RepairResult` (`ctx.repair.v1`, E18) | 0, 4, 5, 7, 8, 12 | `repair.json` |

Notes on the shapes as built:
- `push --json` never prompts; `--yes` is accepted as a no-op, and both
  `push --json` and `push --json --yes -- <paths>` work (scenario 01).
- `status`'s `remote.head` is omitted when the newest listing time is shared
  by different shas (WP-1.1 R1); consumers must not require it. Use a push
  document's `new_head` for the repo HEAD.
- `pull.case-collision.json` has no real-binary golden: it needs two server
  paths that differ only by case, which the test gitstore cannot hold on the
  case-insensitive file systems of the macOS and Windows CI runners. The
  canonical fixture stays the reference.
- `resolve.theirs.json`'s `backup_file` is relative to `.contexo/`:
  `.sync/backup/<P>.<YYYYMMDDTHHMMSSZ>`.
- Contract §7(i) ("lossy frontmatter not detected") no longer holds for
  unknown frontmatter keys (WP-1.6): they survive push -> pull -> teammate
  edit -> pull, normalized (schema keys first, extras sorted, YAML re-indented)
  and the page stays unchanged afterwards (e2e scenario 12).

## Exit codes and error codes (E2, E17)

24 stable codes. The envelope code is authoritative; the exit code is
`contract.ExitCodeFor(code)` (unknown codes give 1). Every code except
`internal` and `not_implemented` has a real-binary golden
`internal/e2e/testdata/contract/error.<code>.json` (22 files; e2e scenario 07
reaches each of them, 08 busy, 09 sha_not_found and server_unsupported);
`internal` and `not_implemented` exist only as canonical fixtures.

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
- Registered by: status.json, status.offline -> cli/status.go (WP-1.1);
  push.json, push.paths, push.distill_guard -> cli/push.go (WP-1.2);
  pull.json -> cli/pull.go (WP-1.3); show.base, show.server, show.sha ->
  cli/show.go, resolve -> cli/resolve.go (WP-1.4); repair -> cli/repair.go
  (WP-1.5); errors.envelope -> cli/capabilities.go (WP-0.1).
- A build from this tree advertises all 12, in this order (e2e scenario 10
  and `version.dev.json`):

| # | Capability | Required by the extension |
|---|---|---|
| 1 | status.json | yes |
| 2 | status.offline | yes |
| 3 | show.base | yes |
| 4 | show.server | yes |
| 5 | show.sha | no (optional) |
| 6 | pull.json | yes |
| 7 | push.json | yes |
| 8 | push.paths | yes |
| 9 | push.distill_guard | yes |
| 10 | resolve | yes |
| 11 | errors.envelope | yes |
| 12 | repair | no (optional, E18) |

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
| 5xx; a body that does not decode on a status the method accepts (2xx, push 409) | server_error |
| 409 other than a decodable push answer, 1xx, 3xx and any other unlisted status | internal (never conflict; deviation 1) |

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
- `lock`: `{pid,host,command,started_at}`, created `O_CREATE|O_EXCL`; a lock
  of the same host whose pid is dead, older than 10 min, or unparseable and
  older than 30 s is reclaimed
- `lock.guard`: transient `O_EXCL` guard held while removing `lock` (stale
  reclaim or unlock); cleared when older than 5 s
- `backup/<relpath>.<YYYYMMDDTHHMMSSZ>`: the working file saved by
  `resolve --theirs` before it overwrites a differing file
- `remote.json`: reserved, never written or read in v1 (E8)

As built and observed by the e2e suite: the first write creates
`.sync/.gitignore` = `*\n` (scenario 01); a pull conflict skip writes a
`source: pull` sidecar and leaves `state.json` and the base untouched
(scenario 02); `resolve --theirs` writes the backup and clears the sidecar
(scenario 02); a push 409 writes a `source: push` sidecar (scenario 07
conflict); `status --offline` reads sidecars only (E9) and writes nothing.

Write order per operation: working files, base files, `conflicts/<path>`,
`bases.json`, `conflicts.json`, `state.json`, each index once, atomically.
push, pull, resolve and repair hold the lock (busy, exit 12, after
`CONTEXO_LOCK_TIMEOUT`); online status only TryLocks inside EnsureBase;
`status --offline` and `push --dry-run` write nothing (E5).

## Engines (as built)

The wave-0 stubs are gone; every engine is real and shared by the CLI and
(after WP-2.1) the MCP tools. Frozen identifiers are the E4 table; options and
results may only gain fields.

| Files | Built by | Entry point |
|---|---|---|
| status.go (+ cli/status.go) | WP-1.1 | `Status(ctx, env, StatusOptions{Offline}) (*contract.StatusDoc, error)`, then `FillStatusIdentity(doc, remote, unusable)` |
| push.go, guard.go (+ cli/push.go) | WP-1.2 | `Push(ctx, env, PushOptions{Paths, Message, DryRun, NoDistill, Resolved, Feature, Tag, Type, Confirm})`; `CheckGuard(env, batch) (*GuardResult{Required, Bypassed, Details}, error)` |
| pull.go (+ cli/pull.go) | WP-1.3 | `Pull(ctx, env, PullOptions{}) (*contract.PullDoc, error)` |
| show.go, resolve.go (+ cli/show.go, cli/resolve.go) | WP-1.4 | `Show(ctx, env, ShowOptions{Path, At})`, `Resolve(ctx, env, ResolveOptions{Path, Strategy, IfServerSHA})`, `ShowPreflight`, `ResolvePreflight` |
| ensurebase.go, repair.go (+ cli/repair.go) | WP-1.5 | `EnsureBase(ctx, env, EnsureBaseOptions{Listing, Paths, Locked})`, `Repair(ctx, env, RepairOptions{DryRun, Budget, Concurrency}) (*RepairResult, error)` |

- Frozen tests (never weakened): `env_test.go`, `overlay_test.go`.
  `ensurebase_test.go` is package `syncops` and uses `FakeRemote`: it stops
  compiling (import cycle) if `syncopstest` ever imports `syncops`.
- `TestRepairResultJSONKeyOrder` pins the E18 shape of `ctx repair --json`
  (`schema, dry_run, server_capabilities, backfilled{path,sha},
  repaired{path,old_sha,new_sha,reason}, skipped{path,reason}, unverified,
  warnings`; arrays `[]`, never null); `repair.json` is its real-binary golden.
- `ApplySidecar` (E9) is the one online override: a modified result becomes a
  conflict of the sidecar's kind (both_modified when empty) iff a sidecar
  exists whose server sha differs from the base sha; nothing else changes.
- `FakeRemote` mirrors gitstore.Write and handler.Push/Pull/ReadPage with the
  WP-0.2 fixes: one commit per changed file, sha `hex(sha1("fake:"+n))`,
  time `2026-01-01T00:00:00Z + n s`; parent check only when parent and
  current are both set (ancestor_content when the parent holds the path);
  expect_absent conflicts unless `NoExpectAbsent`; a no-op returns the path's
  last commit (HEAD under `LegacyNoopHeadSHA`); push `new_head` is always
  repo HEAD after the batch, never a trailing no-op file's older sha.
  `ReadPageAt` applies gitstore.ValidSHA (4-64 hex, either case; otherwise
  400 invalid_sha, client code usage) and resolves a unique case-insensitive
  prefix like git (unknown or ambiguous: 404 "sha not found"; path absent at
  the commit: 404 "page not found at sha"; both sha_not_found), returning the
  full last-touch sha. `PullPages` lists the paths whose content differs
  between since and HEAD (git diff), and a since the repo cannot resolve is a
  500 (server_error), as gitstore.ChangedSince answers; it never means
  "everything". Error bodies are byte-equal to gin's (sorted keys), and
  errors are the same `*sync.HTTPError` values a real client returns (a test
  pins this against `sync.Client` on httptest).

## MCP alignment (D5, D13; WP-2.1)

`ctx mcp` is the same binary, so the agent tools follow the same contract
(E39: no installable build carries the CLI engines without this):
- `ctx_push` and `ctx_pull` run `syncops.Push` / `syncops.Pull` under
  `.sync/lock` and write bases, sidecars and `state.json` only through the
  engines; `ctx_status` runs `syncops.Status` + `FillStatusIdentity`.
- A push 409 never bumps `page_shas` (it writes a `source: push` sidecar and
  answers MERGE_REQUIRED); a modified page is never overwritten by `ctx_pull`.
- `ctx_push` takes `resolved: ["<path>", ...]`, which applies `resolve --ours`
  semantics (re-base onto the server sha the agent was shown in the merge
  brief) before pushing. The MERGE_REQUIRED text tells the agent to write the
  merged page and call `ctx_push` with `resolved=["<path>"]`. Still 10 tools.
- The distill handshake uses the same `CheckGuard` predicate as CLI push (D4).
- `X-Contexo-Client` is the MCP client name, else `ctx-mcp` (E15).

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
- `push.dry-run.json`: a dry run writes nothing, neither the server nor
  `.contexo/.sync` (E31(h)); the document has `dry_run` true, no `new_head`,
  `pushed` entries without `sha` and `conflicts` `[]`.
- Hygiene: emails only `@acme.dev`; no access tokens; page and commit SHAs
  are full 40-hex lowercase; times are `YYYY-MM-DDTHH:MM:SSZ`; no fixture lists
  `pages.etag` or `remote.source` `cache`; 24 `error.<code>.json` files;
  version.release and version.dev list the 11 v1 capabilities then `repair`.

### Golden fixtures from the real binary (e2e, WP-2.2)

- Location: `internal/e2e/testdata/contract/` (48 files: 11 status, 1 version,
  6 push incl. the exit-6 stderr half, 3 pull, 2 resolve, 2 raw show, 1 repair,
  22 `error.<code>.json`) plus `manifest.json`; `internal/e2e/testdata/.gitattributes`
  is `* -text`. Names follow E31 (`pull.case-collision.json` excepted, see
  above).
- `manifest.json`: `{"contract_version":1,"generated_by":"go test ./internal/e2e -run TestGolden -update","fixtures":[{file, schema, command, stream, exit_code, sha256}]}`,
  sorted by file. `command` is the argv array without `--root <dir>` (the E25
  extension argv for extension commands); `stream` is `stdout`, `stderr` or
  `raw` (E32); `schema` is `ctx.*.v1`, null for raw; `repair.json` is stdout
  with `ctx.repair.v1`; `sha256` is of the redacted file.
- Each golden comes from ONE ctx run in a deterministic state (fixed page
  bodies, frontmatter times, session ids, and server commit dates pinned via
  `GIT_AUTHOR_DATE`/`GIT_COMMITTER_DATE` for the in-process gitstore). The raw
  streams are checked first (one 2-space document, empty stderr; or exactly
  one envelope line; E16 times and 40-hex shas on the unredacted bytes).
- Redaction is textual (key order and indentation survive), unit-tested in
  `internal/e2e/redact_test.go`: 40-hex shas -> per-file first-appearance
  placeholders `%040x` from 1 (also inside the manifest `command`); whole-second
  `Z` times -> `2000-01-01T00:00:00Z` (any other time form is left to fail the
  schemas); server URLs -> `http://contexo-e2e.invalid` (bare host:port ->
  `contexo-e2e.invalid`, and the Windows `connectex` refusal text -> the unix
  `connect: connection refused`); absolute roots, raw or JSON-escaped, ->
  `/e2e/<user>` with `/` separators below them; lock pid -> 4242 and host ->
  `e2e-host`; `backup/<P>.<ts>` -> `.20000101T000000Z`; `version --json`
  `os`/`arch` -> `e2e-os`/`e2e-arch`. Content hashes are kept (they are not
  OS-dependent: pages are written with LF).
- Regenerate ONLY for a legitimate shape change (a contract-additive field, or
  a fix that makes ctx emit the contract shape a golden recorded wrongly):
  `make test-e2e-update` (= `CONTEXO_NO_UPDATE_CHECK=1 go test ./internal/e2e -run TestGolden -update -count=1`),
  then review `git diff internal/e2e/testdata`. A second `-update` must leave
  the tree unchanged. Check: `make test-e2e`.
- Knobs: `CONTEXO_E2E_CTX=<abs path>` runs the suite against a prebuilt ctx
  (version.dev.json and the dev-only checks of scenario 10 are skipped for it);
  `CONTEXO_E2E_STRICT=1` turns every known-deviation skip into a failure.
  `go test -short` skips everything that needs the binary.
- Meta tests (no binary needed): `TestGoldenManifest` (disk, manifest and the
  golden groups match one-to-one; every sha256 matches), `TestGoldenStrictDecode`
  (each stdout golden decodes with DisallowUnknownFields into its contract type
  and re-encodes through `contract.WriteDoc` to identical compacted JSON; each
  stderr golden is one envelope line with typed details),
  `TestGoldens_NoSecrets`, `TestGoldens_NoPagesETag`.
- Consumers: WP-2.I validates every stdout/stderr golden with
  contexo-vscode `contract/validate.mjs --manifest` (WP-2.2's early run:
  45 validated, 3 skipped (repair, 2 raw), 0 failed); WP-3.1 vendors them;
  WP-3.2 uses them as `json-output.mdx` examples.

## Deviations confirmed by the user (2026-10-21)

Decisions the errata do not cover.

| # | Decision | Why | Cost if wrong |
|---|---|---|---|
| 1 | HTTP statuses outside E14's list: an unexpected 409 (any 409 but a decodable push answer), any other unlisted 4xx, and 1xx/3xx map to internal; a garbled body (one that does not decode on a status the method accepts: 2xx, or a push 409) maps to server_error. | E14 names 400/401/402/403/404/429/5xx and forbids mapping 409 to conflict, but is silent on the rest. An unlisted status, an informational answer or a redirect net/http did not follow (304, a 3xx without Location) means client and server disagree on the protocol: a bug, not transient, so it is not retried. A garbled body on an expected status is a server or proxy fault, like a 5xx (contract section 5). | The extension shows an internal error (exit 1, no retry) where a retry might have helped (a proxy answering 3xx), or retries (exit 7) a garbled answer that will never decode. |
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
| 12 | `ctx show`'s hidden `--json` cannot be set false: `--json=false` fails flag parsing (usage, exit 3), and `EnvelopeMode` treats the top-level `ctx show` as envelope mode whatever its flags. | E20/E21 make show always machine mode; a plain bool flag let `--json=false` switch show to human rendering, exit 1 and the update nudge. | A caller passing `--json=false` to show gets a usage envelope; show has no human mode to fall back to. |
| 13 | With a live listing, every page an online status leaves unverified is named by a `base_repair_skipped` warning; an offline status never carries that warning. | Contract status ONLINE ("those pages stay unverified and warning base_repair_skipped is added"), E5 (EnsureBase is a no-op without a listing) and E12. The canonical fixtures follow it. | WP-1.1 must warn for every online unverified page, or its output disagrees with `status.all-states.json` / `status.old-server.json`. |
| 14 | Status fixtures obey invariants read from the contract prose: `authenticated` implies `remote.source` live; offline implies `server.capabilities_known` false; source none implies no `repo_exists`/`head` and `page_count` 0; `remote.error` implies source none; `server_outdated` iff capabilities are known and a v1 server capability is missing (E13); a remote side without a live listing carries only `sha` (E9). | The fixtures must be documents a conforming ctx can emit; `TestStatusFixturesConsistent` checks them. | If WP-1.1 reads one rule differently, its goldens (WP-2.2) disagree with a canonical fixture and one side changes then. |

## Known deviations

Product behavior that differs from the contract, found by the e2e suite
(`internal/e2e`). The scenario skips through `knownDeviation(t, "DEV-n")`;
`CONTEXO_E2E_STRICT=1` turns the skip into a failure. Owners fix the
product, never the test.

| id | CONTRACT text | Actual behavior | Owner |
|---|---|---|---|
