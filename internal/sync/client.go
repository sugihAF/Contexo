package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	stdsync "sync"
	"time"

	"github.com/sugihAF/contexo/internal/diff"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// DefaultClientName is the X-Contexo-Client value when neither the
// CONTEXO_CLIENT env var nor SetClientName supplies one.
const DefaultClientName = "ctx-cli"

// clientEnvVar overrides the X-Contexo-Client value on every request (E15).
// The VS Code extension sets it to "contexo-vscode/<version>".
const clientEnvVar = "CONTEXO_CLIENT"

// Response headers a v1 server sends on every response (E13).
const (
	hdrServerVersion = "X-Contexo-Server-Version"
	hdrCapabilities  = "X-Contexo-Capabilities"
	hdrClient        = "X-Contexo-Client"
	hdrAt            = "X-Contexo-At"
	hdrPageSHA       = "X-Page-SHA"
)

// Client speaks HTTP to the Contexo server.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client

	// mu guards everything below: status fans requests out from goroutines.
	mu            stdsync.Mutex
	clientName    string
	serverVersion string
	serverCaps    []contract.ServerCapability
	capsKnown     bool
}

// SetClientName sets the X-Contexo-Client identifier sent on every request so
// the server can attribute who/what synced (e.g. "ctx-cli", "claude-code").
// The CONTEXO_CLIENT env var wins over it.
func (c *Client) SetClientName(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clientName = name
}

// ServerInfo reports the X-Contexo-Server-Version and X-Contexo-Capabilities
// headers of the most recent response. known is false before any response;
// after a response without the capabilities header, known is true and caps
// is empty (an old server).
func (c *Client) ServerInfo() (version string, caps []contract.ServerCapability, known bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	caps = make([]contract.ServerCapability, len(c.serverCaps))
	copy(caps, c.serverCaps)
	return c.serverVersion, caps, c.capsKnown
}

// ListPages returns the last-touch metadata of every page in the repo
// (GET /v1/repos/:id/pages). It never returns a nil slice on success.
func (c *Client) ListPages(repoID string) ([]PageMeta, error) {
	const op = "list pages"
	u := fmt.Sprintf("%s/v1/repos/%s/pages", c.baseURL, repoID)
	resp, body, err := c.request("GET", u, nil, op)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, NewHTTPError(resp.StatusCode, body,
			fmt.Sprintf("sync: list pages (%d): %s", resp.StatusCode, string(body)), nil, false)
	}
	var wrapper struct {
		Pages []PageMeta `json:"pages"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, parseError(resp, body, "list pages", err)
	}
	if wrapper.Pages == nil {
		// An empty repo answers {"pages":null} (gitstore.go:388-390).
		wrapper.Pages = []PageMeta{}
	}
	return wrapper.Pages, nil
}

// ReadPageAt fetches path as of commit sha (GET .../pages/<path>?at=<sha>).
// The second value is X-Page-SHA, the last commit touching path at or before
// sha. A response without an X-Contexo-At echo equal to sha (an old server
// that ignored ?at and served HEAD) is ErrServerUnsupported.
func (c *Client) ReadPageAt(repoID, path, sha string) ([]byte, string, error) {
	const op = "read page at"
	u := fmt.Sprintf("%s/v1/repos/%s/pages/%s?at=%s", c.baseURL, repoID, escapePath(path), url.QueryEscape(sha))
	resp, body, err := c.request("GET", u, nil, op)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", NewHTTPError(resp.StatusCode, body,
			fmt.Sprintf("sync: read page at (%d): %s", resp.StatusCode, string(body)), nil, true)
	}
	// The echo must be present AND equal: Get would turn an absent header
	// into "" and let an old server's HEAD through for an empty sha.
	if echo := resp.Header.Values(hdrAt); len(echo) == 0 || echo[0] != sha {
		return nil, "", atUnsupportedError(resp.StatusCode, body, sha)
	}
	return body, resp.Header.Get(hdrPageSHA), nil
}

// atUnsupportedError is the old-server ?at failure: the server answered 2xx
// but did not echo X-Contexo-At, so it ignored ?at and served HEAD (Gin
// ignores unknown query parameters).
func atUnsupportedError(status int, body []byte, sha string) *HTTPError {
	e := NewHTTPError(status, body,
		fmt.Sprintf("sync: read page at %s: server did not echo X-Contexo-At (no ?at support)", sha),
		ErrServerUnsupported, true)
	e.Code = contract.CodeServerUnsupported
	e.Details = contract.ServerUnsupportedDetails{
		Missing: []contract.ServerCapability{contract.SrvPagesAt},
	}
	return e
}

// requestTimeout bounds every call this client makes. Without it a
// black-holed network hangs `ctx status` and `ctx push` forever -- and any
// progress spinner drawn over such a call would spin forever too. 30s is
// generous enough for a large page push on a slow link.
const requestTimeout = 30 * time.Second

// NewClient creates a sync client.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// do sends req and is the ONLY place this client talks to the network
// (E15). It sets Authorization and X-Contexo-Client, reads the whole body,
// records the server version/capability headers of every response (any
// status), and turns a transport failure into a *NetworkError whose op is
// the method's legacy label, so Error() keeps the old "sync: <op>: <err>"
// text. The returned response's body is already consumed and closed.
func (c *Client) do(req *http.Request, op string) (*http.Response, []byte, error) {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set(hdrClient, c.effectiveClientName())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, &NetworkError{Op: op, Err: err}
	}
	defer resp.Body.Close()
	c.captureServerInfo(resp.Header)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// The connection broke (or timed out) mid-body: a transport failure,
		// never a silently truncated page.
		return nil, nil, &NetworkError{Op: op, Err: err}
	}
	return resp, body, nil
}

// request builds a request (a JSON body when payload is non-nil) and sends
// it through do.
func (c *Client) request(method, u string, payload []byte, op string) (*http.Response, []byte, error) {
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		// A malformed URL (e.g. a repo id with a bad %-escape) is a local
		// bug, not a network failure: keep it out of the retryable class.
		return nil, nil, fmt.Errorf("sync: %s: %v", op, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, op)
}

// effectiveClientName applies the X-Contexo-Client precedence: env
// CONTEXO_CLIENT (read per request), then SetClientName, then
// DefaultClientName. A value that is empty or holds control characters
// (CR/LF would make every request fail or inject headers) is skipped.
func (c *Client) effectiveClientName() string {
	if v, ok := usableClientName(os.Getenv(clientEnvVar)); ok {
		return v
	}
	c.mu.Lock()
	name := c.clientName
	c.mu.Unlock()
	if v, ok := usableClientName(name); ok {
		return v
	}
	return DefaultClientName
}

func usableClientName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return s, true
}

// captureServerInfo records the version and capability headers of a
// response. Capabilities are comma-separated; names are trimmed, empties
// dropped and unknown names kept in order. A missing header means an old
// server: caps [] with known true.
func (c *Client) captureServerInfo(h http.Header) {
	caps := []contract.ServerCapability{}
	for _, line := range h.Values(hdrCapabilities) {
		for _, name := range strings.Split(line, ",") {
			if name = strings.TrimSpace(name); name != "" {
				caps = append(caps, contract.ServerCapability(name))
			}
		}
	}
	version := strings.TrimSpace(h.Get(hdrServerVersion))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.serverVersion = version
	c.serverCaps = caps
	c.capsKnown = true
}

// parseError is the legacy "sync: parse <what>: <err>" failure for a 2xx
// (or 409) body that does not decode. The decode error stays in the chain.
func parseError(resp *http.Response, body []byte, what string, err error) *HTTPError {
	return NewHTTPError(resp.StatusCode, body, fmt.Sprintf("sync: parse %s: %v", what, err), err, false)
}

// statusError is the legacy "sync: <label> (<status>): <body>" failure.
func statusError(resp *http.Response, body []byte, format string) *HTTPError {
	return NewHTTPError(resp.StatusCode, body, fmt.Sprintf(format, resp.StatusCode, string(body)), nil, false)
}

// CreateRepo idempotently creates a repo on the server.
func (c *Client) CreateRepo(repoID string) error {
	url := fmt.Sprintf("%s/v1/repos/%s", c.baseURL, repoID)
	resp, body, err := c.request("POST", url, nil, "create repo")
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return statusError(resp, body, "sync: create repo (%d): %s")
	}
	return nil
}

// PushPages uploads a batch of files to the server. Returns PushResponse
// even on 409 so the caller can inspect conflicts.
func (c *Client) PushPages(repoID string, req *PushRequest) (*PushResponse, error) {
	url := fmt.Sprintf("%s/v1/repos/%s/sync/push", c.baseURL, repoID)
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("sync: marshal push: %w", err)
	}
	resp, body, err := c.request("POST", url, data, "push")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusPaymentRequired {
		return nil, quotaError(body)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		return nil, statusError(resp, body, "sync: push failed (%d): %s")
	}
	var pr PushResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, parseError(resp, body, "push response", err)
	}
	return &pr, nil
}

// PullPages fetches files changed since the given sha (empty = all).
func (c *Client) PullPages(repoID, since string) (*PullResponse, error) {
	url := fmt.Sprintf("%s/v1/repos/%s/sync/pull", c.baseURL, repoID)
	if since != "" {
		url += "?since=" + since
	}
	resp, body, err := c.request("GET", url, nil, "pull")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, body, "sync: pull failed (%d): %s")
	}
	var pr PullResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, parseError(resp, body, "pull response", err)
	}
	return &pr, nil
}

// JoinRepo consumes a repo invite key, adding the authenticated user as a
// member of the target repo. Returns the repo id the key was for.
func (c *Client) JoinRepo(key string) (string, string, error) {
	url := fmt.Sprintf("%s/v1/repos/join", c.baseURL)
	body, _ := json.Marshal(map[string]string{"key": key})
	resp, respBody, err := c.request("POST", url, body, "join")
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode == http.StatusPaymentRequired {
		return "", "", quotaError(respBody)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", statusError(resp, respBody, "sync: join failed (%d): %s")
	}
	var wrapper struct {
		RepoID string `json:"repo_id"`
		Role   string `json:"role"`
	}
	if err := json.Unmarshal(respBody, &wrapper); err != nil {
		return "", "", parseError(resp, respBody, "join response", err)
	}
	return wrapper.RepoID, wrapper.Role, nil
}

// quotaError turns a 402 Payment Required body ({"error","upgrade_url",...})
// into a clean upgrade prompt, so hosted plan-limit rejections reach the user
// as a readable message instead of raw JSON. Used wherever a request can hit a
// hosted usage cap (today: ctx join and ctx push). The result is a typed
// quota_exceeded *HTTPError whose Details carry the body's code and limit.
func quotaError(body []byte) *HTTPError {
	var q struct {
		Error      string `json:"error"`
		UpgradeURL string `json:"upgrade_url"`
	}
	msg := fmt.Sprintf("upgrade required (402): %s", string(body))
	if json.Unmarshal(body, &q) == nil && q.Error != "" {
		msg = q.Error
	}
	return NewHTTPError(http.StatusPaymentRequired, body, msg, nil, false)
}

// ListRepos returns the repos the authenticated user can see. Used by the
// CLI's interactive picker so the user doesn't have to type a repo_id.
func (c *Client) ListRepos() ([]RepoOption, error) {
	url := fmt.Sprintf("%s/v1/repos", c.baseURL)
	resp, body, err := c.request("GET", url, nil, "list repos")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, body, "sync: list repos (%d): %s")
	}
	var wrapper struct {
		Repos []RepoOption `json:"repos"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, parseError(resp, body, "list repos", err)
	}
	return wrapper.Repos, nil
}

// Timeline returns recent commits across the repo.
func (c *Client) Timeline(repoID string, limit int) ([]Commit, error) {
	url := fmt.Sprintf("%s/v1/repos/%s/timeline?limit=%d", c.baseURL, repoID, limit)
	resp, body, err := c.request("GET", url, nil, "timeline")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, body, "sync: timeline (%d): %s")
	}
	var wrapper struct {
		Commits []Commit `json:"commits"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, parseError(resp, body, "timeline", err)
	}
	return wrapper.Commits, nil
}

// MintInviteKey creates an invite key on repoID with the given label. Returns
// the persisted key metadata plus the raw token (only returned once).
func (c *Client) MintInviteKey(repoID, label string) (*InviteKey, string, error) {
	url := fmt.Sprintf("%s/v1/repos/%s/invite-keys", c.baseURL, repoID)
	body, _ := json.Marshal(map[string]string{"label": label})
	resp, respBody, err := c.request("POST", url, body, "mint invite key")
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, "", statusError(resp, respBody, "sync: mint invite key failed (%d): %s")
	}
	var wrapper struct {
		Key   InviteKey `json:"key"`
		Token string    `json:"token"`
	}
	if err := json.Unmarshal(respBody, &wrapper); err != nil {
		return nil, "", parseError(resp, respBody, "mint response", err)
	}
	return &wrapper.Key, wrapper.Token, nil
}

// ListInviteKeys returns the active invite keys for repoID (no raw tokens).
func (c *Client) ListInviteKeys(repoID string) ([]InviteKey, error) {
	url := fmt.Sprintf("%s/v1/repos/%s/invite-keys", c.baseURL, repoID)
	resp, body, err := c.request("GET", url, nil, "list invite keys")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, body, "sync: list invite keys (%d): %s")
	}
	var wrapper struct {
		Keys []InviteKey `json:"keys"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, parseError(resp, body, "list invite keys", err)
	}
	return wrapper.Keys, nil
}

// ErrPageNotFound is returned by ReadPage when the path doesn't exist on the
// server. Callers use it to distinguish "new page" from real errors.
var ErrPageNotFound = fmt.Errorf("sync: page not found")

// ReadPage fetches the current content of a single page from the server.
// Returns ErrPageNotFound if the path isn't tracked yet. The page's last-touch
// sha is returned via the X-Page-SHA response header.
func (c *Client) ReadPage(repoID, filePath string) ([]byte, string, error) {
	u := fmt.Sprintf("%s/v1/repos/%s/pages/%s", c.baseURL, repoID, escapePath(filePath))
	resp, body, err := c.request("GET", u, nil, "read page")
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		// Any 404 keeps errors.Is(ErrPageNotFound) and its text; the code
		// still distinguishes repo_not_found / a missing route.
		return nil, "", NewHTTPError(resp.StatusCode, body, ErrPageNotFound.Error(), ErrPageNotFound, false)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", statusError(resp, body, "sync: read page (%d): %s")
	}
	return body, resp.Header.Get(hdrPageSHA), nil
}

// PageHistory returns the commits that touched filePath, newest first.
// limit <= 0 lets the server pick a default.
func (c *Client) PageHistory(repoID, filePath string, limit int) ([]Commit, error) {
	u := fmt.Sprintf("%s/v1/repos/%s/history/%s", c.baseURL, repoID, escapePath(filePath))
	if limit > 0 {
		u += fmt.Sprintf("?limit=%d", limit)
	}
	resp, body, err := c.request("GET", u, nil, "page history")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, body, "sync: page history (%d): %s")
	}
	var wrapper struct {
		Commits []Commit `json:"commits"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, parseError(resp, body, "history", err)
	}
	return wrapper.Commits, nil
}

// PageEvolution returns the full evolution for filePath: up to `limit` recent
// commits touching the path, each paired with the section-aware diff against
// its immediate prior commit. One round-trip replaces (history + N diffs)
// when the caller wants the whole trajectory. blame populates each diff's
// section IntroducedBy field (best-effort, may add latency on long histories).
func (c *Client) PageEvolution(repoID, filePath string, limit int, blame bool) ([]EvolutionEntry, error) {
	u := fmt.Sprintf("%s/v1/repos/%s/evolution/%s", c.baseURL, repoID, escapePath(filePath))
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if blame {
		q.Set("blame", "true")
	}
	if encoded := q.Encode(); encoded != "" {
		u += "?" + encoded
	}
	resp, body, err := c.request("GET", u, nil, "page evolution")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, body, "sync: page evolution (%d): %s")
	}
	var wrapper struct {
		Path    string           `json:"path"`
		Entries []EvolutionEntry `json:"entries"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, parseError(resp, body, "evolution", err)
	}
	return wrapper.Entries, nil
}

// PageDiff returns a structured diff of filePath between two commits. Empty
// from/to defer to the server's defaults (to = HEAD-for-this-path, from =
// parent of to). When blame is true, each section in the result carries an
// IntroducedBy field pointing at the commit where its heading first appeared.
func (c *Client) PageDiff(repoID, filePath, from, to string, blame bool) (*diff.SectionDiff, error) {
	u := fmt.Sprintf("%s/v1/repos/%s/diff/%s", c.baseURL, repoID, escapePath(filePath))
	q := url.Values{}
	if from != "" {
		q.Set("from", from)
	}
	if to != "" {
		q.Set("to", to)
	}
	if blame {
		q.Set("blame", "true")
	}
	if encoded := q.Encode(); encoded != "" {
		u += "?" + encoded
	}
	resp, body, err := c.request("GET", u, nil, "page diff")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, body, "sync: page diff (%d): %s")
	}
	var d diff.SectionDiff
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, parseError(resp, body, "diff", err)
	}
	return &d, nil
}

// escapePath URL-encodes each path segment so slashes survive the wire but
// any non-ASCII / reserved characters within a segment are escaped. The
// server's wildcard route accepts the slashes literally.
func escapePath(p string) string {
	parts := splitPath(p)
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	return joinPath(parts)
}

func splitPath(p string) []string {
	var out []string
	cur := ""
	for _, r := range p {
		if r == '/' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

func joinPath(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "/"
		}
		out += p
	}
	return out
}

// DeleteInviteKey revokes the invite key with id keyID on repoID.
func (c *Client) DeleteInviteKey(repoID, keyID string) error {
	url := fmt.Sprintf("%s/v1/repos/%s/invite-keys/%s", c.baseURL, repoID, keyID)
	resp, body, err := c.request("DELETE", url, nil, "delete invite key")
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return statusError(resp, body, "sync: delete invite key (%d): %s")
	}
	return nil
}

// Sentinel errors for RemoveMember so the CLI can map the known refusal cases
// to friendly messages without string-matching status codes.
var (
	ErrNotOwner       = fmt.Errorf("sync: owner role required")
	ErrMemberNotFound = fmt.Errorf("sync: not a member of this repo")
	ErrLastOwner      = fmt.Errorf("sync: cannot remove the last owner")
)

// ListMembers returns every member of repoID with their email and role. Any
// member of the repo may call this.
func (c *Client) ListMembers(repoID string) ([]Member, error) {
	url := fmt.Sprintf("%s/v1/repos/%s/members", c.baseURL, repoID)
	resp, body, err := c.request("GET", url, nil, "list members")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, body, "sync: list members (%d): %s")
	}
	var wrapper struct {
		Members []Member `json:"members"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, parseError(resp, body, "list members", err)
	}
	return wrapper.Members, nil
}

// RemoveMember removes userID from repoID. Only an owner may do this. It
// returns ErrNotOwner (403), ErrMemberNotFound (404), or ErrLastOwner (409)
// for the known refusal cases.
func (c *Client) RemoveMember(repoID, userID string) error {
	u := fmt.Sprintf("%s/v1/repos/%s/members/%s", c.baseURL, repoID, url.PathEscape(userID))
	resp, body, err := c.request("DELETE", u, nil, "remove member")
	if err != nil {
		return err
	}
	var sentinel error
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusForbidden:
		sentinel = ErrNotOwner
	case http.StatusNotFound:
		sentinel = ErrMemberNotFound
	case http.StatusConflict:
		sentinel = ErrLastOwner
	default:
		return statusError(resp, body, "sync: remove member (%d): %s")
	}
	return NewHTTPError(resp.StatusCode, body, sentinel.Error(), sentinel, false)
}

// ListActivity returns a page of the repo's push/pull events (newest first)
// plus the total event count for pagination. limit <= 0 lets the server pick
// its default; offset <= 0 starts at the newest.
func (c *Client) ListActivity(repoID string, limit, offset int) ([]ActivityEvent, int, error) {
	u := fmt.Sprintf("%s/v1/repos/%s/activity", c.baseURL, repoID)
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", offset))
	}
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	resp, body, err := c.request("GET", u, nil, "list activity")
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, statusError(resp, body, "sync: list activity (%d): %s")
	}
	var wrapper struct {
		Events []ActivityEvent `json:"events"`
		Total  int             `json:"total"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, 0, parseError(resp, body, "list activity", err)
	}
	return wrapper.Events, wrapper.Total, nil
}
