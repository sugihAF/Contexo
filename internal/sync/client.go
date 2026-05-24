package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/sugihAF/contexo/internal/diff"
)

// Client speaks HTTP to the Contexo server.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewClient creates a sync client.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{},
	}
}

// CreateRepo idempotently creates a repo on the server.
func (c *Client) CreateRepo(repoID string) error {
	url := fmt.Sprintf("%s/v1/repos/%s", c.baseURL, repoID)
	req, _ := http.NewRequest("POST", url, nil)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sync: create repo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sync: create repo (%d): %s", resp.StatusCode, string(body))
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
	httpReq, _ := http.NewRequest("POST", url, bytes.NewReader(data))
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sync: push: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		return nil, fmt.Errorf("sync: push failed (%d): %s", resp.StatusCode, string(body))
	}
	var pr PushResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("sync: parse push response: %w", err)
	}
	return &pr, nil
}

// PullPages fetches files changed since the given sha (empty = all).
func (c *Client) PullPages(repoID, since string) (*PullResponse, error) {
	url := fmt.Sprintf("%s/v1/repos/%s/sync/pull", c.baseURL, repoID)
	if since != "" {
		url += "?since=" + since
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sync: pull: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync: pull failed (%d): %s", resp.StatusCode, string(body))
	}
	var pr PullResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("sync: parse pull response: %w", err)
	}
	return &pr, nil
}

// JoinRepo consumes a repo invite key, adding the authenticated user as a
// member of the target repo. Returns the repo id the key was for.
func (c *Client) JoinRepo(key string) (string, string, error) {
	url := fmt.Sprintf("%s/v1/repos/join", c.baseURL)
	body, _ := json.Marshal(map[string]string{"key": key})
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("sync: join: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("sync: join failed (%d): %s", resp.StatusCode, string(respBody))
	}
	var wrapper struct {
		RepoID string `json:"repo_id"`
		Role   string `json:"role"`
	}
	if err := json.Unmarshal(respBody, &wrapper); err != nil {
		return "", "", fmt.Errorf("sync: parse join response: %w", err)
	}
	return wrapper.RepoID, wrapper.Role, nil
}

// ListRepos returns the repos the authenticated user can see. Used by the
// CLI's interactive picker so the user doesn't have to type a repo_id.
func (c *Client) ListRepos() ([]RepoOption, error) {
	url := fmt.Sprintf("%s/v1/repos", c.baseURL)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sync: list repos: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync: list repos (%d): %s", resp.StatusCode, string(body))
	}
	var wrapper struct {
		Repos []RepoOption `json:"repos"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("sync: parse list repos: %w", err)
	}
	return wrapper.Repos, nil
}

// Timeline returns recent commits across the repo.
func (c *Client) Timeline(repoID string, limit int) ([]Commit, error) {
	url := fmt.Sprintf("%s/v1/repos/%s/timeline?limit=%d", c.baseURL, repoID, limit)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sync: timeline: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync: timeline (%d): %s", resp.StatusCode, string(body))
	}
	var wrapper struct {
		Commits []Commit `json:"commits"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("sync: parse timeline: %w", err)
	}
	return wrapper.Commits, nil
}

// MintInviteKey creates an invite key on repoID with the given label. Returns
// the persisted key metadata plus the raw token (only returned once).
func (c *Client) MintInviteKey(repoID, label string) (*InviteKey, string, error) {
	url := fmt.Sprintf("%s/v1/repos/%s/invite-keys", c.baseURL, repoID)
	body, _ := json.Marshal(map[string]string{"label": label})
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("sync: mint invite key: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("sync: mint invite key failed (%d): %s", resp.StatusCode, string(respBody))
	}
	var wrapper struct {
		Key   InviteKey `json:"key"`
		Token string    `json:"token"`
	}
	if err := json.Unmarshal(respBody, &wrapper); err != nil {
		return nil, "", fmt.Errorf("sync: parse mint response: %w", err)
	}
	return &wrapper.Key, wrapper.Token, nil
}

// ListInviteKeys returns the active invite keys for repoID (no raw tokens).
func (c *Client) ListInviteKeys(repoID string) ([]InviteKey, error) {
	url := fmt.Sprintf("%s/v1/repos/%s/invite-keys", c.baseURL, repoID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sync: list invite keys: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync: list invite keys (%d): %s", resp.StatusCode, string(body))
	}
	var wrapper struct {
		Keys []InviteKey `json:"keys"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("sync: parse list invite keys: %w", err)
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
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("sync: read page: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", ErrPageNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("sync: read page (%d): %s", resp.StatusCode, string(body))
	}
	return body, resp.Header.Get("X-Page-SHA"), nil
}

// PageHistory returns the commits that touched filePath, newest first.
// limit <= 0 lets the server pick a default.
func (c *Client) PageHistory(repoID, filePath string, limit int) ([]Commit, error) {
	u := fmt.Sprintf("%s/v1/repos/%s/history/%s", c.baseURL, repoID, escapePath(filePath))
	if limit > 0 {
		u += fmt.Sprintf("?limit=%d", limit)
	}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sync: page history: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync: page history (%d): %s", resp.StatusCode, string(body))
	}
	var wrapper struct {
		Commits []Commit `json:"commits"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("sync: parse history: %w", err)
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
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sync: page evolution: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync: page evolution (%d): %s", resp.StatusCode, string(body))
	}
	var wrapper struct {
		Path    string           `json:"path"`
		Entries []EvolutionEntry `json:"entries"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("sync: parse evolution: %w", err)
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
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sync: page diff: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync: page diff (%d): %s", resp.StatusCode, string(body))
	}
	var d diff.SectionDiff
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("sync: parse diff: %w", err)
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
	req, _ := http.NewRequest("DELETE", url, nil)
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sync: delete invite key: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sync: delete invite key (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}
