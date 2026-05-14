package jsonl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/sugihAF/contexo/internal/schema"
)

// Reader provides JSONL reading with optional turn-range filtering.
type Reader struct {
	path string
}

// NewReader creates a JSONL reader for the given file path.
func NewReader(path string) *Reader {
	return &Reader{path: path}
}

// ReadAll reads all session events from the JSONL file.
func (r *Reader) ReadAll() ([]*schema.SessionEvent, error) {
	return r.ReadRange(0, 0)
}

// ReadRange reads session events filtered by turn range.
// If fromTurn and toTurn are both 0, all events are returned.
// If fromTurn > 0, only events with Turn >= fromTurn are included.
// If toTurn > 0, only events with Turn <= toTurn are included.
func (r *Reader) ReadRange(fromTurn, toTurn int) ([]*schema.SessionEvent, error) {
	f, err := os.Open(r.path)
	if err != nil {
		return nil, fmt.Errorf("jsonl: open %s: %w", r.path, err)
	}
	defer f.Close()

	var events []*schema.SessionEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var evt schema.SessionEvent
		if err := json.Unmarshal(line, &evt); err != nil {
			return nil, fmt.Errorf("jsonl: unmarshal line %d: %w", lineNum, err)
		}

		if fromTurn > 0 && evt.Turn < fromTurn {
			continue
		}
		if toTurn > 0 && evt.Turn > toTurn {
			continue
		}

		events = append(events, &evt)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("jsonl: scan: %w", err)
	}

	return events, nil
}

// Path returns the file path of this reader.
func (r *Reader) Path() string {
	return r.path
}
