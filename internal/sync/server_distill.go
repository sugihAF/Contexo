package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// DistillRequest is the body of POST /v1/repos/:id/sync/distill.
// Phase 4 will define this fully. Today the server returns 501 regardless.
type DistillRequest struct {
	SessionID string   `json:"session_id"`
	Buffer    []byte   `json:"buffer"`
	PageSlugs []string `json:"page_slugs"`
}

// DistillResponse will carry the server-produced source page (Phase 4).
// Only the error path matters for v1.
type DistillResponse struct {
	SourcePath string `json:"source_path,omitempty"`
	Body       string `json:"body,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ServerDistill posts a buffer to the server's distillation endpoint.
// Today this always returns an error because the server stub returns 501.
// The function exists so the CLI's --fallback-server wiring is real and we
// can swap in the live implementation in Phase 4 without re-plumbing.
func (c *Client) ServerDistill(repoID string, req *DistillRequest) (*DistillResponse, error) {
	url := fmt.Sprintf("%s/v1/repos/%s/sync/distill", c.baseURL, repoID)
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("sync: distill marshal: %w", err)
	}
	httpReq, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sync: distill: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotImplemented {
		return nil, fmt.Errorf("server-side distillation not implemented (planned for Phase 4)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync: distill failed (%d): %s", resp.StatusCode, string(data))
	}
	var dr DistillResponse
	if err := json.Unmarshal(data, &dr); err != nil {
		return nil, fmt.Errorf("sync: parse distill response: %w", err)
	}
	return &dr, nil
}
