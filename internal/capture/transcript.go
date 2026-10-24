package capture

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Exchange is one user→assistant exchange extracted from a Claude Code
// transcript file.
type Exchange struct {
	User      string
	Assistant string
	Tools     []string
}

// Bounds of LatestTurn's re-read when the transcript has not caught up with
// the turn yet. Claude Code writes the transcript asynchronously, so the
// final text record can land after the Stop hook starts reading.
const (
	TurnSettleWait = 2 * time.Second
	turnSettlePoll = 100 * time.Millisecond
)

// LatestExchange returns the latest turn of the JSONL transcript at path:
// User is the last real prompt (not a tool_result, meta or sidechain
// record), Assistant joins the text of every assistant record after it and
// Tools lists every tool_use name after it, in order. Current Claude Code
// transcripts write each content block (thinking, tool_use, text) as its own
// record; older transcripts hold several blocks per record. Both parse the
// same. Returns an empty Exchange (no error) if the transcript has neither a
// prompt nor assistant text.
func LatestExchange(path string) (Exchange, error) {
	t, err := readLatestTurn(path)
	if err != nil {
		return Exchange{}, err
	}
	return t.exchange(""), nil
}

// LatestTurn is LatestExchange for the Stop hook. finalText is the hook's
// last_assistant_message ("" when absent): it is authoritative for the
// turn's final text and is appended unless the transcript already ends with
// it. Without it, a prompted turn whose reply text has not landed yet (its
// latest text, tool_use or tool result is not assistant text) is re-read
// every 100 ms for up to wait. The turn is returned even if the text never
// arrives, so a real prompt is never dropped.
func LatestTurn(path, finalText string, wait time.Duration) (Exchange, error) {
	deadline := time.Now().Add(wait)
	for {
		t, err := readLatestTurn(path)
		if err != nil {
			return Exchange{}, err
		}
		if strings.TrimSpace(finalText) != "" || !t.hasPrompt || t.settled || !time.Now().Before(deadline) {
			return t.exchange(finalText), nil
		}
		time.Sleep(turnSettlePoll)
	}
}

// latestTurn is the parsed latest turn of a transcript.
type latestTurn struct {
	user      string
	hasPrompt bool
	texts     []string
	tools     []string
	// settled: the turn's latest text, tool_use or tool result is assistant
	// text, so the final reply is in the transcript.
	settled bool
}

// exchange assembles the Exchange, appending finalText when the transcript
// does not end with it yet and keeping the newest text within the buffer's
// MaxAssistantBytes (AppendTurn keeps the head of an oversized field).
func (t latestTurn) exchange(finalText string) Exchange {
	texts := t.texts
	if final := strings.TrimSpace(finalText); final != "" {
		if !strings.HasSuffix(strings.Join(texts, "\n"), final) {
			texts = append(append([]string(nil), texts...), final)
		}
	}
	return Exchange{User: t.user, Assistant: joinNewest(texts, MaxAssistantBytes), Tools: t.tools}
}

// joinNewest joins parts with "\n". When the result would exceed limit
// bytes, the oldest parts are dropped (marked with a leading "...") so the
// final answer survives; a single oversized last part is kept whole.
func joinNewest(parts []string, limit int) string {
	all := strings.Join(parts, "\n")
	if len(all) <= limit || len(parts) < 2 {
		return all
	}
	const marker = "...\n"
	kept := len(parts[len(parts)-1])
	first := len(parts) - 1
	for first > 0 && len(marker)+kept+1+len(parts[first-1]) <= limit {
		first--
		kept += 1 + len(parts[first])
	}
	return marker + strings.Join(parts[first:], "\n")
}

func readLatestTurn(path string) (latestTurn, error) {
	if path == "" {
		return latestTurn{}, errors.New("capture: empty transcript path")
	}
	f, err := os.Open(path)
	if err != nil {
		return latestTurn{}, fmt.Errorf("capture: open transcript: %w", err)
	}
	defer f.Close()

	var (
		records []transcriptRecord
		scanner = bufio.NewScanner(f)
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
		// Keep the main conversation's user and assistant records only:
		// attachments, system records, meta records (expanded skills,
		// command caveats), compact summaries and sidechains are not turns.
		if (rec.Type == "user" || rec.Type == "assistant") && !rec.IsSidechain && !rec.IsMeta && !rec.IsCompactSummary {
			records = append(records, rec)
		}
	}
	if err := scanner.Err(); err != nil {
		return latestTurn{}, fmt.Errorf("capture: scan transcript: %w", err)
	}

	// The turn starts at the last real prompt (the whole transcript when
	// there is none).
	start := 0
	var t latestTurn
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Type == "user" && isPrompt(records[i].Message.Content) {
			start = i + 1
			t.hasPrompt = true
			t.user = extractText(records[i].Message.Content)
			break
		}
	}
	for _, rec := range records[start:] {
		switch rec.Type {
		case "user": // a tool result: the reply after it has not started yet
			t.settled = false
		case "assistant":
			if text := extractText(rec.Message.Content); text != "" {
				t.texts = append(t.texts, text)
			}
			t.tools = append(t.tools, extractTools(rec.Message.Content)...)
			if s, ok := settledAfter(rec.Message.Content); ok {
				t.settled = s
			}
		}
	}
	return t, nil
}

// settledAfter reports, for an assistant record's blocks in order, whether
// the last text or tool_use block is text (ok=false when it has neither). A
// tool_use always gets a result and a later reply, so a turn whose latest
// such block is a tool_use is not finished in the transcript yet.
func settledAfter(raw json.RawMessage) (settled, ok bool) {
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			return true, true
		}
		return false, false
	}
	for _, b := range blocks {
		switch {
		case b.Type == "text" && strings.TrimSpace(b.Text) != "":
			settled, ok = true, true
		case b.Type == "tool_use":
			settled, ok = false, true
		}
	}
	return settled, ok
}

type transcriptRecord struct {
	Type             string            `json:"type"`
	IsSidechain      bool              `json:"isSidechain,omitempty"`
	IsMeta           bool              `json:"isMeta,omitempty"`
	IsCompactSummary bool              `json:"isCompactSummary,omitempty"`
	Message          transcriptMessage `json:"message"`
}

type transcriptMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// isPrompt reports whether a user record's content is a prompt rather than
// tool results: string content, or blocks with a text block or without any
// tool_result block (for example an image-only prompt).
func isPrompt(raw json.RawMessage) bool {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return true
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return false
	}
	hasResult := false
	for _, b := range blocks {
		switch b.Type {
		case "text":
			return true
		case "tool_result":
			hasResult = true
		}
	}
	return !hasResult
}

// ContentText returns the human-readable text of a message content value in
// any of the forms hooks and transcripts use: a bare string, one content
// block ({"type":"text","text":...}), a list of blocks, or a message object
// with a "content" field. Non-text blocks are ignored.
func ContentText(raw json.RawMessage) string {
	var obj struct {
		Type    string          `json:"type"`
		Text    *string         `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		switch {
		case obj.Text != nil && (obj.Type == "" || obj.Type == "text"):
			return strings.TrimSpace(*obj.Text)
		case len(obj.Content) > 0:
			return extractText(obj.Content)
		}
		return ""
	}
	return extractText(raw)
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
