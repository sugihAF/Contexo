package recorder

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/sugihAF/contexo/internal/redaction"
	"github.com/sugihAF/contexo/internal/schema"
	boltdbstore "github.com/sugihAF/contexo/internal/store/boltdb"
	"github.com/sugihAF/contexo/internal/store/jsonl"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

// Recorder manages event ingestion, redaction, and persistence.
type Recorder struct {
	mu       sync.Mutex
	ctxDir   string
	db       *sqlitestore.DB
	blobs    *boltdbstore.BlobStore
	pipeline *redaction.Pipeline
	writers  map[string]*jsonl.Writer // sessionID -> writer

	// BlobThreshold is the minimum content size to store as a blob.
	BlobThreshold int
}

// New creates a Recorder using the given .ctx directory.
func New(ctxDir string, db *sqlitestore.DB, blobs *boltdbstore.BlobStore) *Recorder {
	return &Recorder{
		ctxDir:        ctxDir,
		db:            db,
		blobs:         blobs,
		pipeline:      redaction.NewPipeline(),
		writers:       make(map[string]*jsonl.Writer),
		BlobThreshold: 10000, // 10KB default
	}
}

// IngestEvent processes a single event: redact, persist to JSONL + SQLite, optionally BlobDB.
func (r *Recorder) IngestEvent(ctx context.Context, event *schema.SessionEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Redact
	redacted := r.pipeline.Redact(event)

	// Store large content as blob
	if len(redacted.Content.Text) > r.BlobThreshold && r.blobs != nil {
		hash, err := r.blobs.Put(ctx, []byte(redacted.Content.Text))
		if err != nil {
			return fmt.Errorf("recorder: store blob: %w", err)
		}
		redacted.Content.Text = fmt.Sprintf("[blob:%s]", hash)
	}

	// Write to JSONL
	w, err := r.getWriter(redacted)
	if err != nil {
		return fmt.Errorf("recorder: get writer: %w", err)
	}
	if err := w.Append(redacted); err != nil {
		return fmt.Errorf("recorder: append jsonl: %w", err)
	}

	// Index in SQLite
	if err := r.db.InsertEvent(ctx, redacted); err != nil {
		return fmt.Errorf("recorder: index event: %w", err)
	}

	// Upsert session metadata with incremented event count
	if err := r.db.IncrementSessionEventCount(ctx, redacted.Session.ID, redacted.Session.Source, redacted.Session.Repo, redacted.Ts); err != nil {
		return fmt.Errorf("recorder: upsert session: %w", err)
	}

	return nil
}

func (r *Recorder) getWriter(evt *schema.SessionEvent) (*jsonl.Writer, error) {
	key := evt.Session.ID
	if w, ok := r.writers[key]; ok {
		return w, nil
	}

	source := evt.Session.Source
	if source == "" {
		source = "unknown"
	}

	path := filepath.Join(r.ctxDir, "sessions", source, evt.Session.ID+".jsonl")
	w, err := jsonl.NewWriter(path)
	if err != nil {
		return nil, err
	}
	r.writers[key] = w
	return w, nil
}

// SetPipeline replaces the redaction pipeline.
func (r *Recorder) SetPipeline(p *redaction.Pipeline) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pipeline = p
}

// SessionCount returns the number of active session writers.
func (r *Recorder) SessionCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.writers)
}

// UpdateSessionEnd updates the session end time and event count.
func (r *Recorder) UpdateSessionEnd(ctx context.Context, sessionID string, endedAt time.Time, eventCount int) error {
	end := endedAt
	meta := &schema.SessionMeta{
		ID:         sessionID,
		StartedAt:  endedAt, // will be ignored on conflict
		EndedAt:    &end,
		EventCount: eventCount,
	}
	return r.db.UpsertSession(ctx, meta)
}
