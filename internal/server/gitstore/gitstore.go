package gitstore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Store wraps a directory holding one git working repo per repo_id.
type Store struct {
	Root string
}

// ErrConflict is returned when a Write's parent_sha does not match the
// current sha of the last commit that touched the file.
var ErrConflict = errors.New("gitstore: parent_sha conflict")

// ErrRepoNotFound is returned for operations on an uninitialized repo.
var ErrRepoNotFound = errors.New("gitstore: repo not initialized")

// ErrUnsafePath is returned when a caller-supplied file path could escape the
// repo working tree or be parsed by git as an option (path traversal / git
// argument injection). Every public method that forwards a file path to git or
// the filesystem validates it with validatePagePath first.
var ErrUnsafePath = errors.New("gitstore: unsafe file path")

// Conflict describes a non-fast-forward write attempt. AncestorContent is
// populated by callers (see handler.Push) when they can resolve the
// ExpectedParentSHA — the differ needs all three versions (yours, theirs,
// ancestor) for a useful merge brief.
type Conflict struct {
	Path              string `json:"path"`
	CurrentSHA        string `json:"current_sha"`
	CurrentContent    []byte `json:"current_content"`
	ExpectedParentSHA string `json:"expected_parent_sha"`
	AncestorContent   []byte `json:"ancestor_content,omitempty"`
}

// CommitMeta describes a single commit returned by Log.
type CommitMeta struct {
	SHA     string    `json:"sha"`
	Author  string    `json:"author"`
	Email   string    `json:"email"`
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// Open returns a Store rooted at the given directory, creating it if missing.
func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("gitstore: mkdir root: %w", err)
	}
	return &Store{Root: root}, nil
}

// Init creates a new repo for repoID (idempotent). See Create.
func (s *Store) Init(repoID string) error {
	_, err := s.Create(repoID)
	return err
}

// Create creates a new repo for repoID if it does not exist yet. created is
// true only for the caller that actually ran git init: of several concurrent
// callers for one new repo, exactly one is told it created it, so a caller
// can grant ownership of a new repo to that one alone (checking Exists and
// then calling Init races, and every racer would see "did not exist").
func (s *Store) Create(repoID string) (created bool, err error) {
	dir := s.repoDir(repoID)
	if dir == "" {
		return false, fmt.Errorf("gitstore: invalid repoID %q", repoID)
	}
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		return false, nil
	}
	// Concurrent first pushes to a new repo each call Init. Under the repo
	// lock only one of them runs git init and git config (which otherwise
	// collide on template files and config.lock), and a write, which takes
	// the same lock, cannot commit before user.name/user.email are set: .git
	// only exists without them while its creator holds the lock.
	unlock := lockRepo(dir)
	defer unlock()
	if _, err := os.Stat(gitDir); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("gitstore: mkdir repo: %w", err)
	}
	if out, err := s.git(dir, "init", "--initial-branch=main"); err != nil {
		return false, fmt.Errorf("gitstore: init: %w: %s", err, out)
	}
	if out, err := s.git(dir, "config", "user.email", "server@contexo.local"); err != nil {
		return false, fmt.Errorf("gitstore: config email: %w: %s", err, out)
	}
	if out, err := s.git(dir, "config", "user.name", "contexo-server"); err != nil {
		return false, fmt.Errorf("gitstore: config name: %w: %s", err, out)
	}
	return true, nil
}

// Exists reports whether a repo has been initialized.
func (s *Store) Exists(repoID string) bool {
	_, err := os.Stat(filepath.Join(s.repoDir(repoID), ".git"))
	return err == nil
}

// Write writes a file, commits it, and returns the commit sha that now holds
// the content: the new commit, or the path's own last commit when the content
// was unchanged. If parentSHA is non-empty, it must match the latest commit sha
// that touched filePath; otherwise returns ErrConflict with a *Conflict
// describing the current state. See WriteWithOptions.
func (s *Store) Write(repoID, filePath string, content []byte, authorName, authorEmail, message, parentSHA string) (string, *Conflict, error) {
	res, conflict, err := s.WriteWithOptions(repoID, filePath, content, WriteOptions{
		AuthorName:  authorName,
		AuthorEmail: authorEmail,
		Message:     message,
		ParentSHA:   parentSHA,
	})
	return res.SHA, conflict, err
}

// WriteOptions configures WriteWithOptions.
type WriteOptions struct {
	AuthorName, AuthorEmail, Message string
	// ParentSHA, when non-empty, must equal the last commit that touched the
	// path, otherwise the write is a conflict.
	ParentSHA string
	// ExpectAbsent makes the write create-only: if the path already exists at
	// HEAD the write is a conflict (ExpectedParentSHA ""). It wins over
	// ParentSHA. Clients send it for a page they have never synced, which
	// closes the same-path create race (contract push.expect_absent).
	ExpectAbsent bool
}

// WriteResult is the outcome of a successful (non-conflicting) write.
type WriteResult struct {
	// SHA is the commit that holds the written content: the new commit when
	// Changed, otherwise the path's own last commit (repo HEAD only when the
	// path has no history). Never repo HEAD for an unchanged page.
	SHA string
	// Changed reports whether a new commit was created.
	Changed bool
}

// WriteWithOptions writes a file and commits it alone. Writes to one repo are
// serialized in-process, so the parent check, the no-op detection and the
// commit see a consistent HEAD even when several clients push at once.
func (s *Store) WriteWithOptions(repoID, filePath string, content []byte, o WriteOptions) (WriteResult, *Conflict, error) {
	if !s.Exists(repoID) {
		return WriteResult{}, nil, ErrRepoNotFound
	}
	if err := validatePagePath(filePath); err != nil {
		return WriteResult{}, nil, err
	}
	dir := s.repoDir(repoID)
	unlock := lockRepo(dir)
	defer unlock()

	currentSHA, currentContent, err := s.lastCommitForPath(dir, filePath)
	if err != nil {
		return WriteResult{}, nil, err
	}
	if o.ExpectAbsent {
		// Fail closed: if existence cannot be checked, do not write. Treating
		// an error as "absent" would overwrite the page this guard protects.
		_, exists, err := s.blobAt(dir, "HEAD", filePath)
		if err != nil {
			return WriteResult{}, nil, fmt.Errorf("gitstore: expect_absent check: %w", err)
		}
		if exists {
			return WriteResult{}, &Conflict{
				Path:              filePath,
				CurrentSHA:        currentSHA,
				CurrentContent:    currentContent,
				ExpectedParentSHA: "",
			}, ErrConflict
		}
	} else if o.ParentSHA != "" && currentSHA != "" && o.ParentSHA != currentSHA {
		return WriteResult{}, &Conflict{
			Path:              filePath,
			CurrentSHA:        currentSHA,
			CurrentContent:    currentContent,
			ExpectedParentSHA: o.ParentSHA,
		}, ErrConflict
	}

	abs := filepath.Join(dir, filepath.FromSlash(filePath))
	_, statErr := os.Lstat(abs)
	created := errors.Is(statErr, os.ErrNotExist)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return WriteResult{}, nil, fmt.Errorf("gitstore: mkdir: %w", err)
	}
	if err := os.WriteFile(abs, content, 0o644); err != nil {
		return WriteResult{}, nil, fmt.Errorf("gitstore: write: %w", err)
	}
	// -f: ignore rules never apply to an explicitly written page. A member can
	// push a .gitignore, and "*" in it would otherwise make every later add
	// fail ("paths are ignored").
	if out, err := s.git(dir, "add", "-f", "--", filePath); err != nil {
		return WriteResult{}, nil, fmt.Errorf("gitstore: add: %w: %s", err, out)
	}
	if err := s.requireIndexed(dir, filePath, abs, created); err != nil {
		return WriteResult{}, nil, err
	}

	// No-op detection by exit status, not by matching git's "nothing to
	// commit" text, which is translated under non-English locales. Exit 0
	// means the staged path equals HEAD; exit 1 means it differs.
	if out, err := s.git(dir, "diff", "--cached", "--quiet", "--", filePath); err == nil {
		// Unchanged: report the commit that last touched THIS path. Returning
		// repo HEAD here (the old behaviour) handed clients a sha that is not
		// in the path's history, so their next push 409'd (false drift).
		if currentSHA != "" {
			return WriteResult{SHA: currentSHA}, nil, nil
		}
		head, herr := s.headSHA(dir)
		return WriteResult{SHA: head}, nil, herr
	} else if !isExitCode(err, 1) {
		return WriteResult{}, nil, fmt.Errorf("gitstore: diff --cached: %w: %s", err, out)
	}

	// `commit -- <path>` commits only this path, so nothing else left in the
	// index (e.g. by a crashed earlier write) is swept into this commit.
	authorArg, message := commitMeta(o)
	if out, err := s.git(dir, "commit", authorArg, "-m", message, "--", filePath); err != nil {
		return WriteResult{}, nil, fmt.Errorf("gitstore: commit: %w: %s", err, out)
	}
	sha, err := s.headSHA(dir)
	if err != nil {
		return WriteResult{}, nil, err
	}
	return WriteResult{SHA: sha, Changed: true}, nil, nil
}

// requireIndexed checks, right after `git add -- filePath`, that the index
// holds the file filePath names. On a case-insensitive filesystem
// (core.ignorecase) a case variant of a tracked page ("Wiki/A.md" while
// wiki/a.md is tracked) opens and overwrites the tracked file, while git add
// stages nothing under the new name; unchecked, the write would be reported
// as an unchanged no-op and leave the tracked page modified on disk.
//
// The check is that `ls-files -z -- filePath` lists exactly one entry, not
// that the entry's bytes equal filePath: with core.precomposeunicode (git's
// default on macOS) git precomposes its arguments and indexes a decomposed
// (NFD) name such as "wiki/café.md" under its NFC bytes, which is the
// same page. ls-files pathspecs are literal (gitCmd) and case-sensitive, so
// they only ever match the file git staged for this name, and a case variant
// matches nothing.
//
// When there is not exactly one entry the write is undone, best effort, and
// the path is refused with ErrUnsafePath. The caller holds the repo lock, so
// no other write is in flight and the index otherwise equals HEAD: a file
// this write created (abs, when created) is deleted, `reset -q` unstages what
// git add staged under the on-disk name (a new page under a case variant of a
// tracked directory, e.g. "Wiki/c.md" staged as wiki/c.md), and
// `checkout -- .` puts back the tracked file the write overwrote.
func (s *Store) requireIndexed(dir, filePath, abs string, created bool) error {
	out, err := gitCmd(dir, "ls-files", "-z", "--", filePath).Output()
	if err != nil {
		return fmt.Errorf("gitstore: ls-files: %w", err)
	}
	if entries := string(out); strings.Count(entries, "\x00") == 1 && strings.HasSuffix(entries, "\x00") {
		return nil
	}
	if created {
		_ = os.Remove(abs)
	}
	_, _ = s.git(dir, "reset", "-q")
	_, _ = s.git(dir, "checkout", "--", ".")
	return fmt.Errorf("%w: %q differs only in case from an existing path", ErrUnsafePath, filePath)
}

// commitMeta returns the --author argument and message for a write's commit.
// Both come from the client, and git refuses a commit whose message is only
// whitespace or whose author name or email is empty after git strips its
// "crud" characters; '<', '>' and control characters would break the
// "Name <email>" form. Such values fall back to the defaults handler.Push
// uses for missing ones.
func commitMeta(o WriteOptions) (authorArg, message string) {
	// NUL cannot appear in a process argument (exec fails with EINVAL).
	message = strings.ReplaceAll(o.Message, "\x00", "")
	if strings.TrimSpace(message) == "" {
		message = "ctx push"
	}
	name := CleanIdent(o.AuthorName)
	if name == "" {
		name = "unknown"
	}
	email := CleanIdent(o.AuthorEmail)
	if email == "" {
		email = "unknown@contexo.local"
	}
	return fmt.Sprintf("--author=%s <%s>", name, email), message
}

// CleanIdent returns an author name or email as git will accept it in
// "--author=Name <email>": '<', '>' and control characters removed, and any
// whitespace (Unicode included) and git's "crud" characters (. , : ; " ' \)
// trimmed from both ends. It returns "" when nothing else is left, which
// callers treat as a missing value (git refuses an empty identity).
func CleanIdent(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '<' || r == '>' || r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(".,:;\"'\\", r)
	})
}

// repoLocks serializes writes per repo directory (in-process; the server is a
// single process per data root). Values are *sync.Mutex.
var repoLocks sync.Map

// lockRepo locks dir's repo and returns its unlock. The key is the lowercased
// dir: on a case-insensitive filesystem (macOS, Windows) repo IDs "Repo" and
// "repo" name one git repo, which must have one mutex. On a case-sensitive
// host two repos that differ only in case then share a mutex, which only
// serializes their writes and is harmless.
func lockRepo(dir string) (unlock func()) {
	v, _ := repoLocks.LoadOrStore(strings.ToLower(dir), &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func isExitCode(err error, code int) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == code
}

// Read returns the file content at HEAD and the sha of the last commit that
// touched the file. Returns os.ErrNotExist if the path is not a file at HEAD:
// never written, deleted, a directory (e.g. "wiki"), or the repo has no
// commits. An empty file is present, with empty non-nil content.
func (s *Store) Read(repoID, filePath string) ([]byte, string, error) {
	if !s.Exists(repoID) {
		return nil, "", ErrRepoNotFound
	}
	if err := validatePagePath(filePath); err != nil {
		return nil, "", err
	}
	dir := s.repoDir(repoID)
	out, err := s.git(dir, "log", "-n1", "--format=%H", "--", filePath)
	if err != nil {
		if strings.Contains(out, "does not have any commits") {
			return nil, "", os.ErrNotExist
		}
		return nil, "", fmt.Errorf("gitstore: log path: %w: %s", err, out)
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return nil, "", os.ErrNotExist
	}
	// The content comes from the path's own last commit, which holds the same
	// bytes as HEAD. Reading both from one commit keeps them paired even when
	// a write lands between the two git calls; reading HEAD instead could
	// return new content with the old sha, or vice versa. Absence is blobAt's
	// ok (a deletion or a tree), never nil content: an empty page is a page.
	content, ok, err := s.blobAt(dir, sha, filePath)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", os.ErrNotExist
	}
	if content == nil {
		content = []byte{}
	}
	return content, sha, nil
}

// HeadSHA returns the current HEAD sha, or empty if the repo has no commits.
func (s *Store) HeadSHA(repoID string) (string, error) {
	if !s.Exists(repoID) {
		return "", ErrRepoNotFound
	}
	return s.headSHA(s.repoDir(repoID))
}

// ErrUnknownSHA is returned by ReadAtSha when the given sha doesn't resolve.
var ErrUnknownSHA = errors.New("gitstore: unknown sha")

// ErrPathNotAtSHA is returned by ReadAtSha when the sha exists but the path
// does not exist in that commit's tree.
var ErrPathNotAtSHA = errors.New("gitstore: path not present at sha")

// ResolveParentSHAForPath returns the parent commit sha that touched filePath
// before `sha`. Returns "" with no error when there is no such parent (i.e.
// `sha` introduced the file). Returns ErrUnknownSHA if `sha` itself doesn't
// resolve.
func (s *Store) ResolveParentSHAForPath(repoID, filePath, sha string) (string, error) {
	if !s.Exists(repoID) {
		return "", ErrRepoNotFound
	}
	if err := validatePagePath(filePath); err != nil {
		return "", err
	}
	if !isHexRef(sha) {
		return "", ErrUnknownSHA
	}
	dir := s.repoDir(repoID)
	out, err := s.git(dir, "log", "-n2", "--format=%H", sha, "--", filePath)
	if err != nil {
		if isUnknownRev(out) {
			return "", ErrUnknownSHA
		}
		return "", fmt.Errorf("gitstore: log parent: %w: %s", err, out)
	}
	lines := splitLines(out)
	if len(lines) < 2 {
		return "", nil
	}
	return lines[1], nil
}

// HeadSHAForPath returns the sha of the most recent commit that touched
// filePath, or "" if the file has no history in the repo.
func (s *Store) HeadSHAForPath(repoID, filePath string) (string, error) {
	if !s.Exists(repoID) {
		return "", ErrRepoNotFound
	}
	if err := validatePagePath(filePath); err != nil {
		return "", err
	}
	dir := s.repoDir(repoID)
	out, err := s.git(dir, "log", "-n1", "--format=%H", "--", filePath)
	if err != nil {
		if isUnknownRev(out) || strings.Contains(out, "does not have any commits") {
			return "", nil
		}
		return "", fmt.Errorf("gitstore: head for path: %w: %s", err, out)
	}
	return strings.TrimSpace(out), nil
}

// ReadAtSha returns the contents of filePath at the given commit sha.
// Returns ErrUnknownSHA if the sha doesn't resolve to a commit, or
// ErrPathNotAtSHA if the commit exists but filePath is not a file in it
// (never written, deleted, or a directory). An empty file is returned as
// empty, non-nil content.
func (s *Store) ReadAtSha(repoID, filePath, sha string) ([]byte, error) {
	if !s.Exists(repoID) {
		return nil, ErrRepoNotFound
	}
	if sha == "" {
		return nil, ErrUnknownSHA
	}
	if err := validatePagePath(filePath); err != nil {
		return nil, err
	}
	if !isHexRef(sha) {
		return nil, ErrUnknownSHA
	}
	dir := s.repoDir(repoID)
	commit, err := s.resolveCommit(dir, sha)
	if err != nil {
		return nil, err
	}
	return s.pageAtCommit(dir, commit, filePath)
}

// resolveCommit turns a validated hex sha (full or abbreviated) into the full
// id of the commit it names: ErrUnknownSHA when it does not resolve to a
// commit (unknown, ambiguous, or a tree/blob id). Callers then use only the
// full id, so an abbreviation cannot turn ambiguous between git calls.
func (s *Store) resolveCommit(dir, sha string) (string, error) {
	out, err := s.git(dir, "rev-parse", "--verify", sha+"^{commit}")
	if err != nil {
		if isUnknownRev(out) {
			return "", ErrUnknownSHA
		}
		return "", fmt.Errorf("gitstore: rev-parse %s: %w: %s", sha, err, out)
	}
	return strings.TrimSpace(out), nil
}

// pageAtCommit returns filePath's content at the full commit id commit:
// ErrPathNotAtSHA when it is not a file there. blobAt decides absence by the
// object type (a tree such as "wiki" is not a page), never by git's message
// text. An empty file is empty, non-nil content.
func (s *Store) pageAtCommit(dir, commit, filePath string) ([]byte, error) {
	content, ok, err := s.blobAt(dir, commit, filePath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrPathNotAtSHA
	}
	if content == nil {
		content = []byte{}
	}
	return content, nil
}

// ValidSHA reports whether s is acceptable as a client-supplied commit id: 4
// to 64 hex characters, either case (an abbreviated or full SHA-1 or SHA-256
// id). Callers validate a request's sha (e.g. ?at) with it before any git
// call, so flags (--all, -G.), ref names (HEAD, main) and rev syntax (abcd^)
// never reach git. Unlike isHexRef it rejects "", which must not silently
// mean "HEAD".
func ValidSHA(s string) bool {
	return len(s) >= 4 && isHexRef(s)
}

// ReadPageAt returns filePath's content at commit sha (full or abbreviated)
// and pathSHA, the full id of the last commit at or before sha that touched
// filePath: the sha a client records as the page's parent. Errors, checked in
// this order: ErrRepoNotFound; ErrUnknownSHA when sha is not ValidSHA (before
// any git call) or does not resolve to a commit; ErrUnsafePath; and
// ErrPathNotAtSHA when filePath is not a file at sha (never written, deleted,
// or a directory).
func (s *Store) ReadPageAt(repoID, filePath, sha string) (content []byte, pathSHA string, err error) {
	if !s.Exists(repoID) {
		return nil, "", ErrRepoNotFound
	}
	if !ValidSHA(sha) {
		return nil, "", ErrUnknownSHA
	}
	if err := validatePagePath(filePath); err != nil {
		return nil, "", err
	}
	// sha and filePath are both validated above, so neither can be read by
	// git as an option. After resolveCommit only the full id is used.
	dir := s.repoDir(repoID)
	commit, err := s.resolveCommit(dir, sha)
	if err != nil {
		return nil, "", err
	}
	content, err = s.pageAtCommit(dir, commit, filePath)
	if err != nil {
		return nil, "", err
	}
	out, err := s.git(dir, "log", "-n1", "--format=%H", commit, "--", filePath)
	if err != nil {
		return nil, "", fmt.Errorf("gitstore: log %s -- %s: %w: %s", commit, filePath, err, out)
	}
	pathSHA = strings.TrimSpace(out)
	if pathSHA == "" {
		return nil, "", ErrPathNotAtSHA
	}
	return content, pathSHA, nil
}

func isUnknownRev(out string) bool {
	return strings.Contains(out, "unknown revision") ||
		strings.Contains(out, "ambiguous argument") ||
		strings.Contains(out, "bad revision") ||
		strings.Contains(out, "Needed a single revision")
}

// ChangedSince returns file paths changed since the given sha (or all files at
// HEAD when since is empty), plus the current HEAD sha.
func (s *Store) ChangedSince(repoID, since string) ([]string, string, error) {
	if !s.Exists(repoID) {
		return nil, "", ErrRepoNotFound
	}
	dir := s.repoDir(repoID)
	head, err := s.headSHA(dir)
	if err != nil || head == "" {
		return nil, "", err
	}
	if since == "" {
		out, err := s.git(dir, "ls-tree", "-r", "--name-only", "HEAD")
		if err != nil {
			return nil, "", fmt.Errorf("gitstore: ls-tree: %w: %s", err, out)
		}
		return gitPaths(out), head, nil
	}
	if since == head {
		return nil, head, nil
	}
	if !isHexRef(since) {
		return nil, "", ErrUnknownSHA
	}
	out, err := s.git(dir, "diff", "--name-only", since, head, "--")
	if err != nil {
		return nil, "", fmt.Errorf("gitstore: diff: %w: %s", err, out)
	}
	return gitPaths(out), head, nil
}

// gitPaths parses one path name per line of git's plain-text output. Unlike
// splitLines it never trims spaces: validatePagePath accepts leading and
// trailing spaces ("notes/todo.md "), and a trimmed name is a page nobody can
// read back. Only a trailing '\r' is dropped, which no valid page path has.
func gitPaths(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line != "" {
			names = append(names, unquoteGitPath(line))
		}
	}
	return names
}

// Log returns up to limit recent commits across the whole repo.
func (s *Store) Log(repoID string, limit int) ([]CommitMeta, error) {
	if !s.Exists(repoID) {
		return nil, ErrRepoNotFound
	}
	if limit <= 0 {
		limit = 50
	}
	dir := s.repoDir(repoID)
	out, err := s.git(dir, "log", fmt.Sprintf("-n%d", limit), "--format=%H%x09%an%x09%ae%x09%at%x09%s")
	if err != nil {
		if strings.Contains(out, "does not have any commits") {
			return nil, nil
		}
		return nil, fmt.Errorf("gitstore: log: %w: %s", err, out)
	}
	return parseLog(out), nil
}

// PageMeta describes a tracked file's last-touch metadata in a repo.
type PageMeta struct {
	Path    string    `json:"path"`
	SHA     string    `json:"sha"`
	Author  string    `json:"author"`
	Email   string    `json:"email"`
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// RepoSummary is the JSON shape returned for the repo list.
type RepoSummary struct {
	ID         string      `json:"id"`
	PageCount  int         `json:"page_count"`
	LastCommit *CommitMeta `json:"last_commit,omitempty"`
}

// ListRepos returns every initialized repo ID under the store root, sorted.
func (s *Store) ListRepos() ([]string, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("gitstore: list repos: %w", err)
	}
	var repos []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.Root, e.Name(), ".git")); err == nil {
			repos = append(repos, e.Name())
		}
	}
	sort.Strings(repos)
	return repos, nil
}

// ListReposWithMeta returns repo summaries (page count + last commit).
func (s *Store) ListReposWithMeta() ([]RepoSummary, error) {
	ids, err := s.ListRepos()
	if err != nil {
		return nil, err
	}
	out := make([]RepoSummary, 0, len(ids))
	for _, id := range ids {
		pages, _ := s.ListPages(id)
		log, _ := s.Log(id, 1)
		sum := RepoSummary{ID: id, PageCount: len(pages)}
		if len(log) > 0 {
			c := log[0]
			sum.LastCommit = &c
		}
		out = append(out, sum)
	}
	return out, nil
}

// ListPages returns metadata for every tracked file in a repo. One git
// process walks all commits newest → oldest; for each file the first time
// it appears is its last-touch commit.
func (s *Store) ListPages(repoID string) ([]PageMeta, error) {
	if !s.Exists(repoID) {
		return nil, ErrRepoNotFound
	}
	dir := s.repoDir(repoID)
	out, err := s.git(dir, "log", "--name-only", "--format=COMMIT\t%H\t%an\t%ae\t%at\t%s")
	if err != nil {
		if strings.Contains(out, "does not have any commits") {
			return nil, nil
		}
		return nil, fmt.Errorf("gitstore: list pages: %w: %s", err, out)
	}

	seen := map[string]bool{}
	var pages []PageMeta
	var cur PageMeta
	hasCommit := false
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "COMMIT\t") {
			parts := strings.SplitN(strings.TrimPrefix(line, "COMMIT\t"), "\t", 5)
			if len(parts) < 5 {
				continue
			}
			unix, _ := strconv.ParseInt(parts[3], 10, 64)
			cur = PageMeta{
				SHA:     parts[0],
				Author:  parts[1],
				Email:   parts[2],
				Time:    time.Unix(unix, 0).UTC(),
				Message: parts[4],
			}
			hasCommit = true
			continue
		}
		name := unquoteGitPath(line)
		if !hasCommit || seen[name] {
			continue
		}
		seen[name] = true
		p := cur
		p.Path = name
		pages = append(pages, p)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Path < pages[j].Path })
	return pages, nil
}

// LogPath returns up to limit commits that touched filePath.
func (s *Store) LogPath(repoID, filePath string, limit int) ([]CommitMeta, error) {
	if !s.Exists(repoID) {
		return nil, ErrRepoNotFound
	}
	if err := validatePagePath(filePath); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	dir := s.repoDir(repoID)
	out, err := s.git(dir, "log", fmt.Sprintf("-n%d", limit), "--format=%H%x09%an%x09%ae%x09%at%x09%s", "--", filePath)
	if err != nil {
		if strings.Contains(out, "does not have any commits") {
			return nil, nil
		}
		return nil, fmt.Errorf("gitstore: log path: %w: %s", err, out)
	}
	return parseLog(out), nil
}

func (s *Store) repoDir(repoID string) string {
	safe := sanitize(repoID)
	if safe == "" {
		return ""
	}
	return filepath.Join(s.Root, safe)
}

func sanitize(repoID string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			return r
		default:
			return -1
		}
	}, repoID)
}

// ValidatePagePath returns ErrUnsafePath when p is not an acceptable page
// path: the same check every Store method applies to a page path before it
// reaches git or the filesystem. Callers use it to validate every path of a
// batch before writing any of them.
func ValidatePagePath(p string) error {
	return validatePagePath(p)
}

// validatePagePath rejects any caller-supplied page path that could escape the
// repo working tree or be parsed by git as an option. Legitimate page paths are
// clean, relative, forward-slash paths such as "wiki/concepts/foo.md". This is
// the load-bearing server-side guard against the path-traversal write and the
// git-argument-injection bugs: every entry point (CLI push, MCP ctx_push, the
// public sync API) funnels its file paths through gitstore, so validating here
// closes them all regardless of what an untrusted client sends.
func validatePagePath(p string) error {
	if p == "" {
		return ErrUnsafePath
	}
	// Reject NUL and other control bytes outright, so the boundary returns a
	// clean ErrUnsafePath (HTTP 400) instead of relying on the kernel rejecting
	// the pathname later with EINVAL (a 500-class error). Page paths never
	// legitimately contain control characters.
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return ErrUnsafePath
		}
	}
	// Normalize Windows separators so the checks are OS-independent (a self-host
	// server may run on Windows, where "\" is also a path separator).
	norm := strings.ReplaceAll(p, `\`, "/")
	if strings.HasPrefix(norm, "/") {
		return ErrUnsafePath // absolute path
	}
	for _, seg := range strings.Split(norm, "/") {
		// Reject empty (//, leading/trailing /), "." / ".." traversal, and any
		// segment that git could read as a flag.
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, "-") {
			return ErrUnsafePath
		}
		// Never let a page path descend into a repo's ".git" directory: writing
		// .git/hooks/* or .git/config is a server-side code-exec / tamper vector
		// even without leaving the repo. Case-folded for case-insensitive FSes.
		if strings.EqualFold(seg, ".git") {
			return ErrUnsafePath
		}
	}
	// The cleaned slash path must equal the input (no ".."/"."/double-slash
	// normalization), and filepath.IsLocal must agree it stays in-tree.
	if path.Clean(norm) != norm {
		return ErrUnsafePath
	}
	if !filepath.IsLocal(filepath.FromSlash(norm)) {
		return ErrUnsafePath
	}
	return nil
}

// isHexRef reports whether s is a valid git object id: empty or up to 64 hex
// characters. It blocks flag injection (--output=, -G, --all) and ref names
// (HEAD) from reaching git as a revision argument via the since/from/to params.
func isHexRef(s string) bool {
	if len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func (s *Store) git(dir string, args ...string) (string, error) {
	out, err := gitCmd(dir, args...).CombinedOutput()
	return string(out), err
}

// gitCmd builds a git command in dir with LC_ALL=C. Several helpers here
// (isUnknownRev, the "does not have any commits" checks) match git's English
// messages, which a translated git would otherwise change under the host's
// locale.
//
// GIT_LITERAL_PATHSPECS=1 makes every "-- <path>" argument a literal path.
// Page paths are client-supplied, and as pathspecs a leading ':' is magic
// (":memo.md" means memo.md) and '*', '?' and '[' are globs ("wiki/a?.md" also
// matches wiki/ab.md), so a write or read would otherwise check, stage and
// commit other pages than the one named.
//
// core.quotePath=false keeps non-ASCII names verbatim in git's plain-text
// output (wiki/café.md, not "wiki/caf\303\251.md"); names git still quotes
// go through unquoteGitPath.
func gitCmd(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-c", "core.quotePath=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_LITERAL_PATHSPECS=1")
	return cmd
}

// unquoteGitPath undoes git's C-style quoting of a path name in plain-text
// output. Even with core.quotePath=false (gitCmd) git quotes a name that
// contains '"', '\' or a control byte, as "wiki/quo\"te.md" (any non-ASCII
// bytes stay raw inside the quotes). A name that fails to unquote is kept as
// is.
func unquoteGitPath(name string) string {
	if !strings.HasPrefix(name, `"`) {
		return name
	}
	if u, err := strconv.Unquote(name); err == nil {
		return u
	}
	return name
}

// blobAt returns the content of filePath at rev when the path is a file
// (blob) in rev's tree. ok is false when rev does not resolve, or the path is
// absent or not a file (e.g. a directory) at rev. An empty blob is empty,
// non-nil content.
//
// It costs one git process, since Read calls it for every page pull serves:
// `cat-file --batch` reads "<rev>:<path>" on stdin and answers
// "<oid> <type> <size>" followed by the content, or "<name> missing" or
// "<name> ambiguous". Absence is decided by that header and the object type,
// never by message text. The stdin line cannot be split or extended: rev is
// a commit id or HEAD, validatePagePath refuses control characters (so a
// newline) in filePath, and under the default format git takes the whole
// line, spaces included, as the object name.
//
// One exception costs a second process: on macOS git precomposes its
// arguments (core.precomposeunicode) but not stdin, so a decomposed (NFD)
// name such as "wiki/café.md", which git tracks under its NFC bytes,
// is "missing" on stdin. A missing non-ASCII name there is retried once with
// the name on the command line (blobAtPrecomposed); without the retry Read
// would 404 such a page and ExpectAbsent would overwrite it.
func (s *Store) blobAt(dir, rev, filePath string) (content []byte, ok bool, err error) {
	obj := rev + ":" + filePath
	cmd := gitCmd(dir, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(obj + "\n")
	out, err := cmd.Output()
	if err != nil {
		return nil, false, fmt.Errorf("gitstore: cat-file --batch %s: %w", obj, err)
	}
	header, body, found := bytes.Cut(out, []byte("\n"))
	if !found {
		return nil, false, fmt.Errorf("gitstore: cat-file --batch %s: no header in %q", obj, out)
	}
	// A found object's header ends in its size, so a path that itself ends
	// in " missing" cannot be mistaken for absence.
	h := string(header)
	if strings.HasSuffix(h, " missing") {
		if runtime.GOOS == "darwin" && !isASCII(filePath) {
			return blobAtPrecomposed(dir, obj)
		}
		return nil, false, nil
	}
	if strings.HasSuffix(h, " ambiguous") {
		return nil, false, nil
	}
	fields := strings.Split(h, " ")
	if len(fields) != 3 {
		return nil, false, fmt.Errorf("gitstore: cat-file --batch %s: bad header %q", obj, h)
	}
	size, perr := strconv.ParseInt(fields[2], 10, 64)
	if perr != nil || size < 0 {
		return nil, false, fmt.Errorf("gitstore: cat-file --batch %s: bad size in %q", obj, h)
	}
	if fields[1] != "blob" {
		return nil, false, nil
	}
	if int64(len(body)) < size {
		return nil, false, fmt.Errorf("gitstore: cat-file --batch %s: %d of %d bytes", obj, len(body), size)
	}
	// body is a sub-slice of out (never nil here), so an empty blob is empty,
	// non-nil content; the cap stops an append from reaching git's trailer.
	return body[:size:size], true, nil
}

// blobAtPrecomposed reads obj ("<rev>:<path>") with the object name on the
// command line, which git on macOS precomposes (see blobAt). `cat-file blob`
// prints only a blob and exits non-zero for anything else (absent, a tree, an
// unresolved rev), so a non-zero exit means obj is not a page.
func blobAtPrecomposed(dir, obj string) (content []byte, ok bool, err error) {
	out, err := gitCmd(dir, "cat-file", "blob", obj).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("gitstore: cat-file blob %s: %w", obj, err)
	}
	if out == nil {
		out = []byte{}
	}
	return out, true, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func (s *Store) headSHA(dir string) (string, error) {
	out, err := s.git(dir, "rev-parse", "HEAD")
	if err != nil {
		if strings.Contains(out, "unknown revision") || strings.Contains(out, "ambiguous argument") {
			return "", nil
		}
		return "", fmt.Errorf("gitstore: rev-parse: %w: %s", err, out)
	}
	return strings.TrimSpace(out), nil
}

// lastCommitForPath returns the sha of the last commit that touched filePath
// and the file content at HEAD. Returns ("", nil, nil) if the file doesn't
// exist in history.
func (s *Store) lastCommitForPath(dir, filePath string) (string, []byte, error) {
	out, err := s.git(dir, "log", "-n1", "--format=%H", "--", filePath)
	if err != nil {
		if strings.Contains(out, "does not have any commits") || strings.Contains(out, "ambiguous argument") {
			return "", nil, nil
		}
		return "", nil, fmt.Errorf("gitstore: log path: %w: %s", err, out)
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return "", nil, nil
	}
	content, ok, err := s.blobAt(dir, "HEAD", filePath)
	if err != nil || !ok {
		return sha, nil, nil
	}
	return sha, content, nil
}

func parseLog(out string) []CommitMeta {
	var commits []CommitMeta
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 5 {
			continue
		}
		unix, _ := strconv.ParseInt(parts[3], 10, 64)
		commits = append(commits, CommitMeta{
			SHA:     parts[0],
			Author:  parts[1],
			Email:   parts[2],
			Time:    time.Unix(unix, 0).UTC(),
			Message: parts[4],
		})
	}
	return commits
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
