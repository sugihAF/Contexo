# Onboarding UX Quick Wins — Design

Status: approved 2026-05-18
Owner: sugihAF

## Problem

First-time onboarding feedback from a teammate on macOS surfaced three friction points:

1. **Fresh Go install doesn't put `~/go/bin` on PATH automatically.** `ctx --help` fails with "command not found" even after a successful `go install`. The user has to manually edit `~/.zshrc` and reload — a step the docs mention but isn't enforced.
2. **`ctx login` requires a pasted PAT** even after the user has signed in via the dashboard. There's no way to bridge a dashboard session to the CLI without manually minting a token.
3. **`ctx remote set-repo` requires the exact `repo_id`** typed verbatim, even when the user is already a member of N repos that the server could enumerate.

This spec covers (1) and (3) — the two pure-CLI quick wins. (2) is a larger change spanning the CLI, dashboard, and backend; it gets its own spec.

## Goals

- New users can copy-paste a single install command and end up with `ctx` working in a new terminal, without editing dotfiles by hand.
- `ctx remote set-repo` (no arg) and `ctx login` (no `--repo`) present an arrow-key picker of the user's repos when run interactively, while still working in scripts/CI.

## Non-goals

- Auto-installing Go itself (refuse with a clear message if missing).
- Replacing `go install` with pre-built binaries on GitHub Releases. (Future spec — would also unlock package-manager distribution.)
- Browser-based login (separate spec).
- Search-while-typing in the picker (survey supports it automatically once the list passes ~50 items; we won't hit that for a while).
- Per-repo metadata (page count, last commit) in the picker. Keep it terse.

---

## Feature 1 — PATH installer

### Architecture

Two scripts at the Contexo repo root, served via raw GitHub URL:

```
scripts/install.sh   — POSIX (macOS, Linux), bash/zsh/fish-aware
scripts/install.ps1  — Windows PowerShell 5.1+
README.md            — install section rewritten to lead with the scripts
```

Invocation:

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.sh | sh

# Windows
iwr -useb https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.ps1 | iex
```

A future Caddy alias of `contexo.live/install.sh` / `.ps1` would make the URL prettier; not in scope for v1.

### POSIX flow (`install.sh`)

```
1. command -v go >/dev/null || die "Go not found. Install Go 1.25+ first."
2. go install github.com/sugihAF/Contexo/cmd/ctx@latest
3. INSTALL_DIR = $(go env GOBIN); if empty, INSTALL_DIR = "$(go env GOPATH)/bin"
4. case ":$PATH:" in *":$INSTALL_DIR:"*) echo "Already on PATH"; exit 0 ;; esac
5. Detect shell via $SHELL basename:
     zsh   → RC=~/.zshrc                  ; LINE='export PATH="$PATH:'$INSTALL_DIR'"'
     bash  → on macOS: RC=~/.bash_profile ; on Linux: RC=~/.bashrc
             same LINE
     fish  → RC=~/.config/fish/config.fish ; LINE='set -gx PATH $PATH '$INSTALL_DIR
     other → print manual instructions and exit 0
6. grep -F -q "$LINE" "$RC" || { mkdir -p "$(dirname "$RC")" && echo "$LINE" >> "$RC"; }
7. echo "Open a new terminal, then run: ctx --help"
```

### PowerShell flow (`install.ps1`)

```
1. Get-Command go -ErrorAction SilentlyContinue → if null, die
2. & go install github.com/sugihAF/Contexo/cmd/ctx@latest
3. $installDir = $env:GOBIN; if (-not $installDir) { $installDir = "$(go env GOPATH)\bin" }
4. $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
   if ($userPath -split ';' -contains $installDir) { "Already on PATH"; exit 0 }
5. [Environment]::SetEnvironmentVariable("Path", "$userPath;$installDir", "User")
   $env:Path = "$env:Path;$installDir"   # also update the current shell
6. "ctx is now on PATH. Try: ctx --help"
```

PowerShell can refresh the current session's `$env:Path`, so the user doesn't need to restart their terminal (unlike POSIX shells where `source` is required).

### Edge cases

| Scenario | Behavior |
|---|---|
| Go not installed | Exit 1 with a one-line message including "install Go from https://go.dev/dl" |
| Script run twice | Idempotent: `grep -F` check / PATH-contains check skips duplicate writes |
| `GOBIN` set | Honor it; install dir is `GOBIN`, not `GOPATH/bin` |
| Unknown shell (`nu`, `xonsh`, etc.) | Print manual instructions, exit 0 (don't fail) |
| PowerShell run via right-click "Run with PowerShell" | Same as iwr invocation; works |
| Windows with no User Path scope yet | `SetEnvironmentVariable` creates it |
| User without write permission on RC file | Falls through with explanatory message |

### Testing

No automated tests — these scripts touch the user's actual environment. Manual smoke on each OS before tagging the install scripts as stable:

- macOS zsh (Big Sur+): fresh `go install`, no prior PATH entry
- Linux bash (Ubuntu): same
- Windows 11 PowerShell 5.1: same
- Windows 11 PowerShell 7: same
- Re-run on each → confirms idempotency

Documentation update lives in `README.md` install section.

---

## Feature 2 — Interactive set-repo

### Architecture

A new dependency (`github.com/AlecAivazis/survey/v2`) plus a small helper that fetches the user's repo memberships and shows an arrow-key picker.

```
internal/sync/client.go         (mod)  + ListRepos() ([]RepoOption, error)
internal/sync/payloads.go       (mod)  + RepoOption{ID, Role}
internal/cli/interactive.go     (new)  selectRepoInteractive(client, out) (string, error)
internal/cli/remote.go          (mod)  set-repo: TTY + no-arg → picker
internal/cli/auth.go            (mod)  login: TTY + no --repo → picker
go.mod / go.sum                 (mod)  AlecAivazis/survey/v2 dep
```

### Data flow

```
ctx remote set-repo                      (or ctx login without --repo)
    │
    ▼
Is stdin a TTY?  (via golang.org/x/term.IsTerminal)
    │
    ├── no → existing behavior (error: "set-repo requires <id>")
    │       (so scripts/CI never hang waiting on stdin)
    │
    └── yes
            │
            ▼
        sync.Client.ListRepos()  → GET /v1/repos with Bearer token
            │
            ▼
        len(repos) == 0?
            │
            ├── yes → print onboarding hint, exit 1
            │
            └── no → survey.Select prompt:
                      "Pick a repo:"
                      → [shoplens (owner), chompchat-prod (owner), …]
                      → user arrows + enters
                      ▼
                   save to config.Config{RepoID: chosen}
                   "Repo: <chosen>"
```

### Components

**`sync.Client.ListRepos`** — single HTTP GET, returns the decoded list:

```go
type RepoOption struct {
    ID   string `json:"id"`
    Role string `json:"role"`   // "owner" | "member" | ""
}

func (c *Client) ListRepos() ([]RepoOption, error) { ... }
```

Decodes into an anonymous struct internally (`{"repos": [...]}`), maps into `[]RepoOption`. Errors surface 401/403/500 with the response body text.

**`internal/cli/interactive.go`** — one helper, split so the fetch+format part is unit-testable:

```go
// fetchRepoOptions is pure: no terminal interaction.
func fetchRepoOptions(c *sync.Client) ([]RepoOption, []string /* labels */, error)

// selectRepoInteractive shows the picker. Calls fetchRepoOptions internally.
// Returns the chosen ID, or an error (including the empty-list onboarding hint).
func selectRepoInteractive(c *sync.Client, out io.Writer) (string, error)
```

Labels are formatted `"shoplens         (owner)"` with role right-padded for column alignment.

**`internal/cli/remote.go`** `set-repo` patch:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    if len(args) > 0 {
        return saveRepoID(args[0])
    }
    if !term.IsTerminal(int(os.Stdin.Fd())) {
        return fmt.Errorf("set-repo requires a <repo-id> arg when stdin isn't a TTY")
    }
    chosen, err := selectRepoInteractive(client, cmd.OutOrStdout())
    if err != nil { return err }
    return saveRepoID(chosen)
}
```

**`internal/cli/auth.go`** `login` patch: after token validates and creds are saved, if `--repo` is empty and TTY, run the picker and save the chosen ID into the same credentials/config file.

### Empty list handling

```
$ ctx remote set-repo
You're not a member of any repos yet.
Sign in to https://contexo-web.pages.dev to join a repo (or
ask the owner to mint an invite key and run `ctx join <key>`),
then re-run.
```

### Edge cases

| Scenario | Behavior |
|---|---|
| Non-TTY stdin (CI, pipe) | Existing arg-required error; never blocks |
| Empty repo list | Friendly hint with exact next steps (above) |
| Server returns 401 | Surface as auth error → "run `ctx login` first" |
| Server returns 500 | Surface raw message; user can retry |
| User Ctrl-C's the picker | `survey` returns `terminal.InterruptErr`; we surface "cancelled" and exit 1 cleanly |
| `set-repo <id>` for an id the user isn't a member of | Existing behavior (server's permission check on next push surfaces the error); not validated client-side |
| `--repo` flag passed alongside positional arg on `login` | Flag wins (matches existing behavior pattern) |

### Testing

- `sync.Client.ListRepos`: `httptest` server returning canned `{"repos": [...]}`, plus 401 and 500 cases
- `fetchRepoOptions`: pure function, tested directly
- `selectRepoInteractive`: no direct test (survey owns the input loop); coverage comes via the manual smoke
- `remote.go set-repo`: TTY-not-detected + no-arg path returns the right error message
- Manual smoke on macOS and Windows after impl

---

## Rollout

| Phase | Scope | Risk | Tests |
|---|---|---|---|
| 1 | PATH installer scripts + README update | Low — scripts run on user machines, not in CI | Manual on 3 OSes |
| 2 | Interactive set-repo + login picker | Low — additive, opt-out by passing the arg | Unit + manual |

Both phases ship as separate commits in the same session, following the per-phase commit pattern already used in this project (see [[per-phase-commits]] memory).

## Out of scope (deferred)

- Browser-based login (separate spec)
- Pre-built binaries / GitHub Releases / Homebrew tap / scoop manifest
- `--server` and `--repo` defaults inferred from a `~/.contexo/global.json` config
- Auto-detecting which Contexo server is reachable when multiple are configured
- Repo metadata in the picker (page count, last commit, owner email)
- Search/filter in the picker
