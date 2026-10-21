package gitstore

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
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

// Init creates a new repo for repoID (idempotent).
func (s *Store) Init(repoID string) error {
	dir := s.repoDir(repoID)
	if dir == "" {
		return fmt.Errorf("gitstore: invalid repoID %q", repoID)
	}
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		return nil
	}
	// Concurrent first pushes to a new repo each call Init. Under the repo
	// lock only one of them runs git init and git config (which otherwise
	// collide on template files and config.lock), and a write, which takes
	// the same lock, cannot commit before user.name/user.email are set: .git
	// only exists without them while its creator holds the lock.
	unlock := lockRepo(dir)
	defer unlock()
	if _, err := os.Stat(gitDir); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("gitstore: mkdir repo: %w", err)
	}
	if out, err := s.git(dir, "init", "--initial-branch=main"); err != nil {
		return fmt.Errorf("gitstore: init: %w: %s", err, out)
	}
	if out, err := s.git(dir, "config", "user.email", "server@contexo.local"); err != nil {
		return fmt.Errorf("gitstore: config email: %w: %s", err, out)
	}
	if out, err := s.git(dir, "config", "user.name", "contexo-server"); err != nil {
		return fmt.Errorf("gitstore: config name: %w: %s", err, out)
	}
	return nil
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
		if _, exists, _ := s.blobAt(dir, "HEAD", filePath); exists {
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
	if out, err := s.git(dir, "add", "--", filePath); err != nil {
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
	authorArg := fmt.Sprintf("--author=%s <%s>", o.AuthorName, o.AuthorEmail)
	if out, err := s.git(dir, "commit", authorArg, "-m", o.Message, "--", filePath); err != nil {
		return WriteResult{}, nil, fmt.Errorf("gitstore: commit: %w: %s", err, out)
	}
	sha, err := s.headSHA(dir)
	if err != nil {
		return WriteResult{}, nil, err
	}
	return WriteResult{SHA: sha, Changed: true}, nil, nil
}

// requireIndexed checks, right after `git add -- filePath`, that the index
// holds exactly filePath. On a case-insensitive filesystem (core.ignorecase) a
// case variant of a tracked page ("Wiki/A.md" while wiki/a.md is tracked)
// opens and overwrites the tracked file, while git add stages nothing under
// the new name; unchecked, the write would be reported as an unchanged no-op
// and leave the tracked page modified on disk. ls-files pathspecs are literal
// (gitCmd) and case-sensitive, so its -z output must be filePath + NUL.
//
// On a mismatch the write is undone, best effort, and the path is refused with
// ErrUnsafePath. The caller holds the repo lock, so no other write is in
// flight and the index otherwise equals HEAD: a file this write created
// (abs, when created) is deleted, `reset -q` unstages what git add staged
// under the on-disk name (a new page under a case variant of a tracked
// directory, e.g. "Wiki/c.md" staged as wiki/c.md), and `checkout -- .` puts
// back the tracked file the write overwrote.
func (s *Store) requireIndexed(dir, filePath, abs string, created bool) error {
	out, err := gitCmd(dir, "ls-files", "-z", "--", filePath).Output()
	if err != nil {
		return fmt.Errorf("gitstore: ls-files: %w", err)
	}
	if string(out) == filePath+"\x00" {
		return nil
	}
	if created {
		_ = os.Remove(abs)
	}
	_, _ = s.git(dir, "reset", "-q")
	_, _ = s.git(dir, "checkout", "--", ".")
	return fmt.Errorf("%w: %q differs only in case from an existing path", ErrUnsafePath, filePath)
}

// repoLocks serializes writes per repo directory (in-process; the server is a
// single process per data root). Values are *sync.Mutex.
var repoLocks sync.Map

func lockRepo(dir string) (unlock func()) {
	v, _ := repoLocks.LoadOrStore(dir, &sync.Mutex{})
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
	// First verify the sha resolves so we can return a clean ErrUnknownSHA.
	out, err := s.git(dir, "rev-parse", "--verify", sha+"^{commit}")
	if err != nil {
		if isUnknownRev(out) {
			return nil, ErrUnknownSHA
		}
		return nil, fmt.Errorf("gitstore: rev-parse %s: %w: %s", sha, err, out)
	}
	// Read through the resolved full id, so an abbreviation cannot turn
	// ambiguous between the two git calls. blobAt decides absence by exit
	// status and object type (a tree such as "wiki" is not a page), never by
	// git's message text.
	commit := strings.TrimSpace(out)
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
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
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
	content, err = s.ReadAtSha(repoID, filePath, sha)
	if err != nil {
		return nil, "", err
	}
	// sha and filePath are both validated above, so neither can be read by
	// git as an option.
	out, err := s.git(s.repoDir(repoID), "log", "-n1", "--format=%H", sha, "--", filePath)
	if err != nil {
		if isUnknownRev(out) {
			return nil, "", ErrUnknownSHA
		}
		return nil, "", fmt.Errorf("gitstore: log %s -- %s: %w: %s", sha, filePath, err, out)
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

// gitPaths parses one path name per line of git's plain-text output.
func gitPaths(out string) []string {
	names := splitLines(out)
	for i, n := range names {
		names[i] = unquoteGitPath(n)
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
// absent or not a file (e.g. a directory) at rev. Absence is decided by git's
// exit status and the object type, never by message text.
func (s *Store) blobAt(dir, rev, filePath string) (content []byte, ok bool, err error) {
	obj := rev + ":" + filePath
	typ, terr := gitCmd(dir, "cat-file", "-t", obj).Output()
	if terr != nil || strings.TrimSpace(string(typ)) != "blob" {
		return nil, false, nil
	}
	out, err := gitCmd(dir, "cat-file", "blob", obj).Output()
	if err != nil {
		return nil, false, fmt.Errorf("gitstore: cat-file %s: %w", obj, err)
	}
	return out, true, nil
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
