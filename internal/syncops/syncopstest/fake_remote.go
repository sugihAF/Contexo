// Package syncopstest provides test doubles for the syncops engines.
//
// syncopstest imports syncops, so tests that use it from the syncops
// directory must be in package syncops_test.
package syncopstest

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"sort"
	"strconv"
	stdsync "sync"
	"time"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// FakeOptions selects server behaviours. The zero value is a current
// (WP-0.2) server.
type FakeOptions struct {
	// OldServer models a server without ?at support or capability headers:
	// ReadPageAt fails with sync.ErrServerUnsupported and ServerInfo reports
	// no capabilities. Like a real old server it ignores ?at and answers
	// the HEAD read, so a path absent at HEAD gets its codeless JSON 404,
	// which the real client maps to sha_not_found.
	OldServer bool
	// LegacyNoopHeadSHA models the pre-fix gitstore bug: a no-op write
	// returns repo HEAD instead of the path's last commit.
	LegacyNoopHeadSHA bool
	// NoExpectAbsent makes the server ignore PushFile.ExpectAbsent.
	NoExpectAbsent bool
	// ServerVersion is the X-Contexo-Server-Version the fake reports
	// ("0.7.0-fake" when empty; an OldServer reports none).
	ServerVersion string
}

// FakeRemote is an in-memory, per-path commit model implementing
// syncops.Remote with deterministic 40-hex SHAs. It mirrors
// internal/server/gitstore.Write and handler.Push/Pull/ReadPage with the
// WP-0.2 fixes: one commit per changed file; commit n has sha
// hex(sha1("fake:"+n)) and time 2026-01-01T00:00:00Z + n seconds. The repo
// id is ignored (one repo). Every method is safe for concurrent use.
//
// Errors are the *sync.HTTPError values a real *sync.Client returns for the
// same server answer (built with sync.NewHTTPError).
type FakeRemote struct {
	opts FakeOptions

	mu       stdsync.Mutex
	commits  []fakeCommit
	bySHA    map[string]int // commit sha -> index in commits
	requests map[string]int
	failNext map[string][]error
	known    bool // a Remote call got a "response" (ServerInfo semantics)
}

type fakeCommit struct {
	sha, path              string
	content                []byte
	author, email, message string
	time                   time.Time
}

var _ syncops.Remote = (*FakeRemote)(nil)

// Remote method names, as Requests and FailNext spell them.
const (
	methodPushPages  = "PushPages"
	methodPullPages  = "PullPages"
	methodListPages  = "ListPages"
	methodReadPage   = "ReadPage"
	methodReadPageAt = "ReadPageAt"
)

// Wire bodies of the server's error answers (WP-0.2, and pre-WP-0.2 for an
// OldServer).
const (
	bodyPageNotFound    = `{"error":"page not found","code":"page_not_found"}`
	bodyPageNotFoundOld = `{"error":"page not found"}`
	bodySHANotFound     = `{"error":"sha not found","code":"sha_not_found"}`
)

const defaultServerVersion = "0.7.0-fake"

// fakeEpoch is the time of commit 0; commit n is fakeEpoch + n seconds.
var fakeEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// NewFakeRemote returns an empty repo.
func NewFakeRemote(o FakeOptions) *FakeRemote {
	return &FakeRemote{
		opts:     o,
		bySHA:    map[string]int{},
		requests: map[string]int{},
		failNext: map[string][]error{},
	}
}

// PushPages implements syncops.Remote. Files are processed in order, one
// commit per changed file (gitstore.Write). A file conflicts when it asks
// for expect_absent and the path exists (unless NoExpectAbsent), or when
// its parent_sha and the path's last commit are both set and differ; the
// conflict carries ancestor_content when the parent commit holds the path.
// Content equal to the HEAD content is a no-op that returns the path's last
// commit (repo HEAD under LegacyNoopHeadSHA). NewHead is the last pushed
// sha, else HEAD. Conflicts are data: the error stays nil, as the real
// client returns the 409 body.
func (f *FakeRemote) PushPages(repoID string, req *sync.PushRequest) (*sync.PushResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(methodPushPages); err != nil {
		return nil, err
	}
	if req == nil {
		req = &sync.PushRequest{}
	}
	author, email, message := req.AuthorName, req.AuthorEmail, req.Message
	// handler.Push fallbacks (its email fallback uses the server's own
	// domain; the fake keeps test data on acme.dev).
	if author == "" {
		author = "unknown"
	}
	if email == "" {
		email = "unknown@acme.dev"
	}
	if message == "" {
		message = "ctx push"
	}

	resp := &sync.PushResponse{}
	newHead := ""
	for _, file := range req.Files {
		cur, curContent := "", []byte(nil)
		if i, ok := f.lastTouch(file.Path, len(f.commits)-1); ok {
			cur, curContent = f.commits[i].sha, f.commits[i].content
		}
		if file.ExpectAbsent && cur != "" && !f.opts.NoExpectAbsent {
			resp.Conflicts = append(resp.Conflicts, sync.Conflict{
				Path:           file.Path,
				CurrentSHA:     cur,
				CurrentContent: clone(curContent),
			})
			continue
		}
		if file.ParentSHA != "" && cur != "" && file.ParentSHA != cur {
			var ancestor []byte
			if pi, ok := f.bySHA[file.ParentSHA]; ok {
				if ai, ok := f.lastTouch(file.Path, pi); ok {
					ancestor = clone(f.commits[ai].content)
				}
			}
			resp.Conflicts = append(resp.Conflicts, sync.Conflict{
				Path:              file.Path,
				CurrentSHA:        cur,
				CurrentContent:    clone(curContent),
				ExpectedParentSHA: file.ParentSHA,
				AncestorContent:   ancestor,
			})
			continue
		}
		var sha string
		if cur != "" && bytes.Equal(curContent, []byte(file.Content)) {
			sha = cur // nothing to commit
			if f.opts.LegacyNoopHeadSHA {
				sha = f.head()
			}
		} else {
			sha = f.commit(file.Path, []byte(file.Content), author, email, message)
		}
		resp.Pushed = append(resp.Pushed, sync.PushedFile{Path: file.Path, SHA: sha})
		newHead = sha
	}
	if newHead == "" {
		newHead = f.head()
	}
	resp.NewHead = newHead
	return resp, nil
}

// PullPages implements syncops.Remote. since "" (or a sha the repo does not
// know) returns every path at HEAD; otherwise only the paths touched by
// commits after since. Each file carries its HEAD content and last-touch
// sha; files are sorted by path and never nil. NewHead is HEAD.
func (f *FakeRemote) PullPages(repoID, since string) (*sync.PullResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(methodPullPages); err != nil {
		return nil, err
	}
	from := 0
	if since != "" {
		if i, ok := f.bySHA[since]; ok {
			from = i + 1
		}
	}
	last := f.lastTouches()
	touched := map[string]bool{}
	for _, c := range f.commits[from:] {
		touched[c.path] = true
	}
	files := []sync.PullFile{}
	for _, p := range sortedKeys(touched) {
		c := f.commits[last[p]]
		files = append(files, sync.PullFile{Path: p, Content: string(c.content), SHA: c.sha})
	}
	return &sync.PullResponse{NewHead: f.head(), Files: files}, nil
}

// ListPages implements syncops.Remote: every path with its last-touch
// commit's sha, author, email, time and message, sorted by path, never nil.
func (f *FakeRemote) ListPages(repoID string) ([]sync.PageMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(methodListPages); err != nil {
		return nil, err
	}
	last := f.lastTouches()
	pages := []sync.PageMeta{}
	for _, p := range sortedKeys(last) {
		c := f.commits[last[p]]
		pages = append(pages, sync.PageMeta{
			Path: p, SHA: c.sha, Author: c.author, Email: c.email, Time: c.time, Message: c.message,
		})
	}
	return pages, nil
}

// ReadPage implements syncops.Remote: the HEAD content and the path's
// last-touch sha (X-Page-SHA); an absent path is the real client's 404
// page_not_found error.
func (f *FakeRemote) ReadPage(repoID, path string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(methodReadPage); err != nil {
		return nil, "", err
	}
	i, ok := f.lastTouch(path, len(f.commits)-1)
	if !ok {
		body := []byte(bodyPageNotFound)
		if f.opts.OldServer {
			body = []byte(bodyPageNotFoundOld)
		}
		return nil, "", sync.NewHTTPError(404, body, sync.ErrPageNotFound.Error(), sync.ErrPageNotFound, false)
	}
	return clone(f.commits[i].content), f.commits[i].sha, nil
}

// ReadPageAt implements syncops.Remote: the content of the path's latest
// commit at or before sha, and that commit's sha (X-Page-SHA). An unknown
// sha, or a path absent at it, is the real client's 404 sha_not_found
// error. An OldServer ignores ?at (see FakeOptions.OldServer).
func (f *FakeRemote) ReadPageAt(repoID, path, sha string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(methodReadPageAt); err != nil {
		return nil, "", err
	}
	if f.opts.OldServer {
		i, ok := f.lastTouch(path, len(f.commits)-1)
		if !ok {
			return nil, "", readAtStatusError(404, []byte(bodyPageNotFoundOld))
		}
		// 200 with HEAD content and no X-Contexo-At echo (sync.Client
		// ReadPageAt's old-server branch).
		e := sync.NewHTTPError(200, clone(f.commits[i].content),
			fmt.Sprintf("sync: read page at %s: server did not echo X-Contexo-At (no ?at support)", sha),
			sync.ErrServerUnsupported, true)
		e.Code = contract.CodeServerUnsupported
		e.Details = contract.ServerUnsupportedDetails{
			Missing: []contract.ServerCapability{contract.SrvPagesAt},
		}
		return nil, "", e
	}
	at, ok := f.bySHA[sha]
	i := -1
	if ok {
		i, ok = f.lastTouch(path, at)
	}
	if !ok {
		return nil, "", readAtStatusError(404, []byte(bodySHANotFound))
	}
	return clone(f.commits[i].content), f.commits[i].sha, nil
}

// readAtStatusError is sync.Client ReadPageAt's non-200 error.
func readAtStatusError(status int, body []byte) *sync.HTTPError {
	return sync.NewHTTPError(status, body,
		fmt.Sprintf("sync: read page at (%d): %s", status, string(body)), nil, true)
}

// ServerInfo implements syncops.Remote with the real client's semantics:
// known is false until a Remote call got an answer (a FailNext error that is
// a *sync.NetworkError is not one); then an OldServer reports ("", [])
// and a current server its version and capabilities, minus write.path_sha
// under LegacyNoopHeadSHA and push.expect_absent under NoExpectAbsent.
// ServerInfo is not counted in Requests.
func (f *FakeRemote) ServerInfo() (string, []contract.ServerCapability, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	caps := []contract.ServerCapability{}
	if !f.known {
		return "", caps, false
	}
	if f.opts.OldServer {
		return "", caps, true
	}
	caps = append(caps, contract.SrvPagesAt)
	if !f.opts.NoExpectAbsent {
		caps = append(caps, contract.SrvExpectAbsent)
	}
	if !f.opts.LegacyNoopHeadSHA {
		caps = append(caps, contract.SrvWritePathSHA)
	}
	v := f.opts.ServerVersion
	if v == "" {
		v = defaultServerVersion
	}
	return v, caps, true
}

// Seed commits content at path (one commit, author "Seed"
// <seed@acme.dev>, message "seed <path>") and returns the commit sha. Like
// a no-op write, unchanged content makes no commit and returns the path's
// last commit. Seed is not a Remote call: it is not counted and does not
// make ServerInfo known.
func (f *FakeRemote) Seed(path string, content []byte) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i, ok := f.lastTouch(path, len(f.commits)-1); ok && bytes.Equal(f.commits[i].content, content) {
		return f.commits[i].sha
	}
	return f.commit(path, clone(content), "Seed", "seed@acme.dev", "seed "+path)
}

// Head returns the current HEAD sha ("" for an empty repo).
func (f *FakeRemote) Head() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.head()
}

// Content returns the HEAD content of path.
func (f *FakeRemote) Content(path string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.lastTouch(path, len(f.commits)-1)
	if !ok {
		return nil, false
	}
	return clone(f.commits[i].content), true
}

// Requests returns the number of calls per Remote method name
// ("PushPages", "PullPages", "ListPages", "ReadPage", "ReadPageAt"). Only
// called methods appear; the map is a copy.
func (f *FakeRemote) Requests() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(f.requests))
	for k, v := range f.requests {
		out[k] = v
	}
	return out
}

// FailNext makes the next call of method return err (the call still counts).
// Calls queue: two FailNext for one method fail its next two calls. An
// unknown method name panics (a typo would otherwise never fire).
func (f *FakeRemote) FailNext(method string, err error) {
	switch method {
	case methodPushPages, methodPullPages, methodListPages, methodReadPage, methodReadPageAt:
	default:
		panic(fmt.Sprintf("syncopstest: FailNext: unknown method %q", method))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext[method] = append(f.failNext[method], err)
}

// begin counts a Remote call and returns its queued failure, if any. The
// caller holds f.mu.
func (f *FakeRemote) begin(method string) error {
	f.requests[method]++
	if q := f.failNext[method]; len(q) > 0 {
		err := q[0]
		f.failNext[method] = q[1:]
		var ne *sync.NetworkError
		if !errors.As(err, &ne) {
			f.known = true
		}
		return err
	}
	f.known = true
	return nil
}

// commit appends one commit and returns its sha. The caller holds f.mu and
// passes content the fake owns.
func (f *FakeRemote) commit(path string, content []byte, author, email, message string) string {
	n := len(f.commits) + 1
	sha := fmt.Sprintf("%x", sha1.Sum([]byte("fake:"+strconv.Itoa(n))))
	f.commits = append(f.commits, fakeCommit{
		sha: sha, path: path, content: content,
		author: author, email: email, message: message,
		time: fakeEpoch.Add(time.Duration(n) * time.Second),
	})
	f.bySHA[sha] = n - 1
	return sha
}

func (f *FakeRemote) head() string {
	if len(f.commits) == 0 {
		return ""
	}
	return f.commits[len(f.commits)-1].sha
}

// lastTouch returns the index of the latest commit at or before index upto
// that touched path.
func (f *FakeRemote) lastTouch(path string, upto int) (int, bool) {
	for i := upto; i >= 0; i-- {
		if f.commits[i].path == path {
			return i, true
		}
	}
	return -1, false
}

// lastTouches maps every path to the index of its last commit.
func (f *FakeRemote) lastTouches() map[string]int {
	last := map[string]int{}
	for i, c := range f.commits {
		last[c.path] = i
	}
	return last
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte{}, b...)
}
