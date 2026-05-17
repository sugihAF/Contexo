package capture

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Exchange is one user→assistant exchange extracted from a Claude Code
// transcript file.
type Exchange struct {
	User      string
	Assistant string
	Tools     []string
}

// LatestExchange returns the most recent (user, assistant, tools) tuple
// from the JSONL transcript at path. Returns an empty Exchange (no error)
// if the transcript has no assistant turns yet.
func LatestExchange(path string) (Exchange, error) {
	if path == "" {
		return Exchange{}, errors.New("capture: empty transcript path")
	}
	f, err := os.Open(path)
	if err != nil {
		return Exchange{}, fmt.Errorf("capture: open transcript: %w", err)
	}
	defer f.Close()

	var (
		records       []transcriptRecord
		scanner       = bufio.NewScanner(f)
	)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec transcriptRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue // tolerate unknown / malformed lines
		}
		// Only keep records we care about.
		if rec.Type == "user" || rec.Type == "assistant" {
			records = append(records, rec)
		}
	}
	if err := scanner.Err(); err != nil {
		return Exchange{}, fmt.Errorf("capture: scan transcript: %w", err)
	}

	// Walk backward, find last assistant record.
	lastAssistant := -1
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Type == "assistant" {
			lastAssistant = i
			break
		}
	}
	if lastAssistant < 0 {
		return Exchange{}, nil
	}

	// Walk backward from there, find preceding user record.
	lastUser := -1
	for i := lastAssistant - 1; i >= 0; i-- {
		if records[i].Type == "user" {
			lastUser = i
			break
		}
	}

	ex := Exchange{
		Assistant: extractText(records[lastAssistant].Message.Content),
		Tools:     extractTools(records[lastAssistant].Message.Content),
	}
	if lastUser >= 0 {
		ex.User = extractText(records[lastUser].Message.Content)
	}
	return ex, nil
}

type transcriptRecord struct {
	Type    string             `json:"type"`
	Message transcriptMessage `json:"message"`
}

type transcriptMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// extractText pulls human-readable text from a message's content field.
// Claude Code stores content as either a bare string (older user records)
// or an array of typed blocks. For blocks, we concatenate "text" type
// entries and ignore "thinking", "tool_use", and "tool_result".
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string form first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		case "tool_result":
			// tool_result content can be a string or a nested blocks list;
			// we skip both — they're transcript-bloat for capture purposes.
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// extractTools returns the tool_use names from an assistant message's
// content blocks, in invocation order.
func extractTools(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name != "" {
			out = append(out, b.Name)
		}
	}
	return out
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Name string `json:"name,omitempty"`
}
