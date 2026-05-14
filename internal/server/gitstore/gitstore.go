package gitstore

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// Conflict describes a non-fast-forward write attempt.
type Conflict struct {
	Path              string `json:"path"`
	CurrentSHA        string `json:"current_sha"`
	CurrentContent    []byte `json:"current_content"`
	ExpectedParentSHA string `json:"expected_parent_sha"`
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
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
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

// Write writes a file, commits it, and returns the new HEAD sha.
// If parentSHA is non-empty, it must match the latest commit sha that touched
// filePath; otherwise returns ErrConflict with a *Conflict describing the
// current state.
func (s *Store) Write(repoID, filePath string, content []byte, authorName, authorEmail, message, parentSHA string) (string, *Conflict, error) {
	if !s.Exists(repoID) {
		return "", nil, ErrRepoNotFound
	}
	dir := s.repoDir(repoID)

	currentSHA, currentContent, _ := s.lastCommitForPath(dir, filePath)
	if parentSHA != "" && currentSHA != "" && parentSHA != currentSHA {
		return "", &Conflict{
			Path:              filePath,
			CurrentSHA:        currentSHA,
			CurrentContent:    currentContent,
			ExpectedParentSHA: parentSHA,
		}, ErrConflict
	}

	abs := filepath.Join(dir, filepath.FromSlash(filePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", nil, fmt.Errorf("gitstore: mkdir: %w", err)
	}
	if err := os.WriteFile(abs, content, 0o644); err != nil {
		return "", nil, fmt.Errorf("gitstore: write: %w", err)
	}
	if out, err := s.git(dir, "add", filePath); err != nil {
		return "", nil, fmt.Errorf("gitstore: add: %w: %s", err, out)
	}

	authorArg := fmt.Sprintf("--author=%s <%s>", authorName, authorEmail)
	out, err := s.git(dir, "commit", authorArg, "-m", message)
	if err != nil {
		if strings.Contains(out, "nothing to commit") {
			sha, _ := s.headSHA(dir)
			return sha, nil, nil
		}
		return "", nil, fmt.Errorf("gitstore: commit: %w: %s", err, out)
	}
	sha, err := s.headSHA(dir)
	return sha, nil, err
}

// Read returns the file content at HEAD and the sha of the last commit that
// touched the file. Returns os.ErrNotExist if the file doesn't exist.
func (s *Store) Read(repoID, filePath string) ([]byte, string, error) {
	if !s.Exists(repoID) {
		return nil, "", ErrRepoNotFound
	}
	dir := s.repoDir(repoID)
	sha, content, err := s.lastCommitForPath(dir, filePath)
	if err != nil {
		return nil, "", err
	}
	if sha == "" {
		return nil, "", os.ErrNotExist
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
		return splitLines(out), head, nil
	}
	if since == head {
		return nil, head, nil
	}
	out, err := s.git(dir, "diff", "--name-only", since, head)
	if err != nil {
		return nil, "", fmt.Errorf("gitstore: diff: %w: %s", err, out)
	}
	return splitLines(out), head, nil
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

// LogPath returns up to limit commits that touched filePath.
func (s *Store) LogPath(repoID, filePath string, limit int) ([]CommitMeta, error) {
	if !s.Exists(repoID) {
		return nil, ErrRepoNotFound
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

func (s *Store) git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
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
	show := exec.Command("git", "show", "HEAD:"+filePath)
	show.Dir = dir
	content, err := show.Output()
	if err != nil {
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
