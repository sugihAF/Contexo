package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
