package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/sugihAF/contexo/internal/schema"
)

// Client handles HTTP sync operations with the CtxHub server.
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

// PushCommit uploads a context commit to the server.
func (c *Client) PushCommit(repoID string, commit *schema.ContextCommit) error {
	data, err := json.Marshal(commit)
	if err != nil {
		return fmt.Errorf("sync: marshal commit: %w", err)
	}

	url := fmt.Sprintf("%s/v1/repos/%s/commits", c.baseURL, repoID)
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("sync: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sync: push commit: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sync: push commit failed (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// PushSessionChunk uploads a compressed session chunk.
func (c *Client) PushSessionChunk(repoID, sessionID, chunkID string, data []byte) error {
	url := fmt.Sprintf("%s/v1/repos/%s/sessions/%s/chunks/%s",
		c.baseURL, repoID, sessionID, chunkID)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("sync: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/gzip")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sync: push chunk: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sync: push chunk failed (%d)", resp.StatusCode)
	}

	return nil
}

// PullCommits fetches commits from the server.
func (c *Client) PullCommits(repoID string) ([]*schema.ContextCommit, error) {
	url := fmt.Sprintf("%s/v1/repos/%s/commits", c.baseURL, repoID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("sync: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sync: pull commits: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync: pull commits failed (%d)", resp.StatusCode)
	}

	var commits []*schema.ContextCommit
	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return nil, fmt.Errorf("sync: decode commits: %w", err)
	}

	return commits, nil
}
