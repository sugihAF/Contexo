package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/sugihAF/contexo/internal/schema"
)

// CodexMessage represents a message from the Codex CLI JSON stream.
type CodexMessage struct {
	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
	Model   string `json:"model,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ParseCodexStream parses a Codex CLI --json output stream and returns SessionEvents.
func ParseCodexStream(reader io.Reader, sessionID string) ([]*schema.SessionEvent, error) {
	var events []*schema.SessionEvent
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	turn := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var msg CodexMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue // skip non-JSON lines
		}

		evt := normalizeCodexMessage(&msg, sessionID, &turn)
		if evt != nil {
			events = append(events, evt)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("codex: scan stream: %w", err)
	}

	return events, nil
}
