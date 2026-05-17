// Package capture implements the agent-reasoning-capture buffer that the
// Stop hook appends to after every assistant turn. The buffer is a JSONL
// file per Claude Code session under .contexo/raw/sessions/_pending/, kept
// bounded by per-turn truncation and a hard turn cap.
//
// The buffer is read by the ctx_push MCP tool's distill handshake (Phase 1)
// to produce a structured "source" page. See docs/specs/2026-05-17-agent-
// reasoning-capture-design.md.
package capture

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// File-tree constants. All paths are relative to the .contexo directory.
const (
	PendingDirRel = "raw/sessions/_pending"
	ArchiveDirRel = "raw/sessions/_pending/_archive"

	MaxAssistantBytes    = 4 * 1024
	MaxUserBytes         = 2 * 1024
	MaxTurns             = 500
	DropOldestOnOverflow = 100
)

// TurnRecord is one JSONL line in the buffer.
type TurnRecord struct {
	Timestamp string         `json:"ts,omitempty"`
	Turn      int            `json:"turn"`
	User      string         `json:"user,omitempty"`
	Assistant string         `json:"assistant,omitempty"`
	Tools     []string       `json:"tools,omitempty"`
	Truncated *TruncationTag `json:"truncated,omitempty"`
}

// TruncationTag marks a marker line inserted when older turns are dropped
// to keep the buffer under MaxTurns.
type TruncationTag struct {
	Dropped int    `json:"dropped"`
	Reason  string `json:"reason"`
}

// Buffer is one session's pending capture file.
type Buffer struct {
	ContexoDir string
	SessionID  string
}

// Open returns a Buffer handle for the given session. Does not create the
// file; AppendTurn does that on first write.
func Open(contexoDir, sessionID string) *Buffer {
	return &Buffer{ContexoDir: contexoDir, SessionID: sessionID}
}

// Path returns the absolute path to the buffer's JSONL file.
func (b *Buffer) Path() string {
	return filepath.Join(b.ContexoDir, filepath.FromSlash(PendingDirRel), b.SessionID+".jsonl")
}

// Exists reports whether the buffer's JSONL file is present on disk.
func (b *Buffer) Exists() bool {
	_, err := os.Stat(b.Path())
	return err == nil
}

// AppendTurn writes one record. Truncates oversized fields, dedupes by
// turn index against existing records, and inserts a marker line when the
// buffer would exceed MaxTurns. If rec.Turn is zero or negative, it is
// auto-assigned to one past the current last turn.
func (b *Buffer) AppendTurn(rec TurnRecord) error {
	if err := os.MkdirAll(filepath.Dir(b.Path()), 0o755); err != nil {
		return fmt.Errorf("capture: mkdir pending: %w", err)
	}

	existing, err := b.Records()
	if err != nil {
		return err
	}

	if rec.Turn <= 0 {
		rec.Turn = nextTurnIndex(existing)
	}
	for _, e := range existing {
		if e.Truncated == nil && e.Turn == rec.Turn {
			return nil // dedupe: already wrote this turn
		}
	}

	rec.User = truncate(rec.User, MaxUserBytes)
	rec.Assistant = truncate(rec.Assistant, MaxAssistantBytes)
	if rec.Timestamp == "" {
		rec.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	if len(existing) >= MaxTurns {
		if err := b.rewriteWithOverflow(existing, rec); err != nil {
			return err
		}
		return nil
	}

	return appendLine(b.Path(), rec)
}

// Records returns all turn records in append order. Returns nil if the
// buffer file does not exist.
func (b *Buffer) Records() ([]TurnRecord, error) {
	f, err := os.Open(b.Path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("capture: open buffer: %w", err)
	}
	defer f.Close()

	var out []TurnRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec TurnRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue // skip malformed lines defensively
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("capture: scan buffer: %w", err)
	}
	return out, nil
}

// Archive moves the buffer file from _pending/ to _pending/_archive/.
// No-op if the file does not exist.
func (b *Buffer) Archive() error {
	src := b.Path()
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	archiveDir := filepath.Join(b.ContexoDir, filepath.FromSlash(ArchiveDirRel))
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return fmt.Errorf("capture: mkdir archive: %w", err)
	}
	dst := filepath.Join(archiveDir, b.SessionID+".jsonl")
	return os.Rename(src, dst)
}

// List returns all non-archived buffers in the .contexo directory, sorted
// by modification time descending (most-recent first). Empty list if the
// pending directory does not exist.
func List(contexoDir string) ([]*Buffer, error) {
	pendingDir := filepath.Join(contexoDir, filepath.FromSlash(PendingDirRel))
	entries, err := os.ReadDir(pendingDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("capture: list pending: %w", err)
	}

	type bufWithTime struct {
		buf  *Buffer
		mod  time.Time
	}
	var bs []bufWithTime
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		sid := strings.TrimSuffix(name, ".jsonl")
		bs = append(bs, bufWithTime{buf: Open(contexoDir, sid), mod: info.ModTime()})
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].mod.After(bs[j].mod) })

	out := make([]*Buffer, len(bs))
	for i, b := range bs {
		out[i] = b.buf
	}
	return out, nil
}

// MostRecent returns the most-recently-modified buffer whose mtime is
// within maxAge of now. Returns nil (no error) if none qualifies.
func MostRecent(contexoDir string, maxAge time.Duration) (*Buffer, error) {
	bs, err := List(contexoDir)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().Add(-maxAge)
	for _, b := range bs {
		info, err := os.Stat(b.Path())
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			return b, nil
		}
	}
	return nil, nil
}

// PruneOlderThan deletes pending buffer files whose mtime is older than
// maxAge. Returns the count of removed files.
func PruneOlderThan(contexoDir string, maxAge time.Duration) (int, error) {
	bs, err := List(contexoDir)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, b := range bs {
		info, err := os.Stat(b.Path())
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(b.Path()); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

func nextTurnIndex(existing []TurnRecord) int {
	max := 0
	for _, e := range existing {
		if e.Turn > max {
			max = e.Turn
		}
	}
	return max + 1
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func appendLine(path string, rec TurnRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("capture: marshal record: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("capture: open for append: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("capture: append: %w", err)
	}
	return nil
}

// rewriteWithOverflow drops the oldest DropOldestOnOverflow records,
// inserts a marker line, and appends the new record.
func (b *Buffer) rewriteWithOverflow(existing []TurnRecord, rec TurnRecord) error {
	keep := existing[DropOldestOnOverflow:]
	tmpPath := b.Path() + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("capture: overflow rewrite: %w", err)
	}
	w := bufio.NewWriter(f)
	marker := TurnRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Truncated: &TruncationTag{Dropped: DropOldestOnOverflow, Reason: "buffer_cap"},
	}
	for _, line := range append([]TurnRecord{marker}, append(keep, rec)...) {
		data, err := json.Marshal(line)
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("capture: overflow marshal: %w", err)
		}
		if _, err := w.Write(append(data, '\n')); err != nil {
			_ = f.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("capture: overflow write: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("capture: overflow flush: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("capture: overflow close: %w", err)
	}
	return os.Rename(tmpPath, b.Path())
}
