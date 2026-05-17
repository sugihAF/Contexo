package capture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTmpContexo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	contexo := filepath.Join(dir, ".contexo")
	if err := os.MkdirAll(contexo, 0o755); err != nil {
		t.Fatalf("mkdir contexo: %v", err)
	}
	return contexo
}

func TestAppendAndRead(t *testing.T) {
	contexo := newTmpContexo(t)
	b := Open(contexo, "sess-1")

	if err := b.AppendTurn(TurnRecord{User: "hello", Assistant: "hi"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := b.AppendTurn(TurnRecord{User: "what now", Assistant: "do X"}); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	recs, err := b.Records()
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].Turn != 1 || recs[1].Turn != 2 {
		t.Errorf("turn auto-increment: got %d, %d, want 1, 2", recs[0].Turn, recs[1].Turn)
	}
	if recs[0].User != "hello" {
		t.Errorf("user: got %q", recs[0].User)
	}
}

func TestDedupeByTurnIndex(t *testing.T) {
	contexo := newTmpContexo(t)
	b := Open(contexo, "sess-1")

	if err := b.AppendTurn(TurnRecord{Turn: 5, User: "first"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := b.AppendTurn(TurnRecord{Turn: 5, User: "duplicate"}); err != nil {
		t.Fatalf("append dup: %v", err)
	}

	recs, _ := b.Records()
	if len(recs) != 1 {
		t.Fatalf("dedupe failed: got %d records, want 1", len(recs))
	}
	if recs[0].User != "first" {
		t.Errorf("first write should win: got %q", recs[0].User)
	}
}

func TestTruncation(t *testing.T) {
	contexo := newTmpContexo(t)
	b := Open(contexo, "sess-1")

	longAssistant := strings.Repeat("a", MaxAssistantBytes+1000)
	longUser := strings.Repeat("u", MaxUserBytes+1000)

	if err := b.AppendTurn(TurnRecord{User: longUser, Assistant: longAssistant}); err != nil {
		t.Fatalf("append: %v", err)
	}

	recs, _ := b.Records()
	if len(recs[0].Assistant) > MaxAssistantBytes {
		t.Errorf("assistant not truncated: %d bytes", len(recs[0].Assistant))
	}
	if len(recs[0].User) > MaxUserBytes {
		t.Errorf("user not truncated: %d bytes", len(recs[0].User))
	}
	if !strings.HasSuffix(recs[0].Assistant, "...") {
		t.Errorf("assistant missing truncation marker")
	}
}

func TestOverflowInsertsMarker(t *testing.T) {
	contexo := newTmpContexo(t)
	b := Open(contexo, "sess-1")

	for i := 0; i < MaxTurns; i++ {
		if err := b.AppendTurn(TurnRecord{User: "x"}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	// One more push the buffer over.
	if err := b.AppendTurn(TurnRecord{User: "final"}); err != nil {
		t.Fatalf("append final: %v", err)
	}

	recs, _ := b.Records()
	if len(recs) != MaxTurns-DropOldestOnOverflow+2 {
		t.Errorf("overflow: got %d records, want %d", len(recs), MaxTurns-DropOldestOnOverflow+2)
	}
	if recs[0].Truncated == nil {
		t.Errorf("first record should be the truncation marker")
	}
	if recs[0].Truncated.Dropped != DropOldestOnOverflow {
		t.Errorf("dropped count: got %d, want %d", recs[0].Truncated.Dropped, DropOldestOnOverflow)
	}
}

func TestArchive(t *testing.T) {
	contexo := newTmpContexo(t)
	b := Open(contexo, "sess-1")
	if err := b.AppendTurn(TurnRecord{User: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := b.Archive(); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if b.Exists() {
		t.Errorf("buffer file should be gone after archive")
	}
	archived := filepath.Join(contexo, filepath.FromSlash(ArchiveDirRel), "sess-1.jsonl")
	if _, err := os.Stat(archived); err != nil {
		t.Errorf("archived file missing: %v", err)
	}
}

func TestArchiveNoFile(t *testing.T) {
	contexo := newTmpContexo(t)
	b := Open(contexo, "never-written")
	if err := b.Archive(); err != nil {
		t.Errorf("archive of nonexistent file should be no-op, got: %v", err)
	}
}

func TestListMostRecentOrdering(t *testing.T) {
	contexo := newTmpContexo(t)
	now := time.Now()
	// Stagger mtimes so "a" is newest, "c" is oldest.
	staggers := map[string]time.Duration{
		"a": 1 * time.Minute,
		"b": 5 * time.Minute,
		"c": 10 * time.Minute,
	}
	for sid, age := range staggers {
		b := Open(contexo, sid)
		if err := b.AppendTurn(TurnRecord{User: "x"}); err != nil {
			t.Fatalf("append %s: %v", sid, err)
		}
		past := now.Add(-age)
		_ = os.Chtimes(b.Path(), past, past)
	}

	bs, err := List(contexo)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(bs) != 3 {
		t.Fatalf("got %d buffers, want 3", len(bs))
	}
	if bs[0].SessionID != "a" {
		t.Errorf("first buffer: got %q, want %q", bs[0].SessionID, "a")
	}
	if bs[2].SessionID != "c" {
		t.Errorf("last buffer: got %q, want %q", bs[2].SessionID, "c")
	}
}

func TestMostRecentRespectsMaxAge(t *testing.T) {
	contexo := newTmpContexo(t)
	b := Open(contexo, "old")
	if err := b.AppendTurn(TurnRecord{User: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	old := time.Now().Add(-25 * time.Hour)
	_ = os.Chtimes(b.Path(), old, old)

	got, err := MostRecent(contexo, 6*time.Hour)
	if err != nil {
		t.Fatalf("most-recent: %v", err)
	}
	if got != nil {
		t.Errorf("stale buffer should be filtered, got %q", got.SessionID)
	}

	got, err = MostRecent(contexo, 48*time.Hour)
	if err != nil {
		t.Fatalf("most-recent wide: %v", err)
	}
	if got == nil {
		t.Errorf("buffer should be returned within 48h window")
	}
}

func TestPruneOlderThan(t *testing.T) {
	contexo := newTmpContexo(t)
	for _, sid := range []string{"keep", "drop"} {
		b := Open(contexo, sid)
		if err := b.AppendTurn(TurnRecord{User: "x"}); err != nil {
			t.Fatalf("append %s: %v", sid, err)
		}
	}
	old := time.Now().Add(-40 * 24 * time.Hour)
	_ = os.Chtimes(Open(contexo, "drop").Path(), old, old)

	removed, err := PruneOlderThan(contexo, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed: got %d, want 1", removed)
	}
	if Open(contexo, "keep").Exists() == false {
		t.Errorf("keep buffer should still exist")
	}
	if Open(contexo, "drop").Exists() == true {
		t.Errorf("drop buffer should be gone")
	}
}

func TestRecordsHandlesMalformedLine(t *testing.T) {
	contexo := newTmpContexo(t)
	b := Open(contexo, "sess-1")
	if err := b.AppendTurn(TurnRecord{User: "valid"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Inject a garbage line.
	f, _ := os.OpenFile(b.Path(), os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("{not json\n")
	_ = f.Close()

	recs, err := b.Records()
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(recs) != 1 {
		t.Errorf("malformed line should be skipped, got %d records", len(recs))
	}
}
