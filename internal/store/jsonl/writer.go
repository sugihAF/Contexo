package jsonl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/sugihAF/contexo/internal/schema"
)

// Writer provides append-only JSONL writing for session events.
type Writer struct {
	mu   sync.Mutex
	path string
}

// NewWriter creates a JSONL writer for the given file path.
// It creates parent directories if they don't exist.
func NewWriter(path string) (*Writer, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("jsonl: create dir %s: %w", dir, err)
	}
	return &Writer{path: path}, nil
}

// Append writes a single SessionEvent as one JSON line.
func (w *Writer) Append(event *schema.SessionEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("jsonl: open %s: %w", w.path, err)
	}
	defer f.Close()

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("jsonl: marshal event: %w", err)
	}

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("jsonl: write: %w", err)
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("jsonl: fsync: %w", err)
	}

	return nil
}

// Path returns the file path of this writer.
func (w *Writer) Path() string {
	return w.path
}
