package syncstate

import (
	"errors"
	"time"
)

// ErrBaseUnavailable is wrapped by ReadVerified when a path has no usable
// base: no record, a missing base file, or bytes that no longer hash to the
// recorded hash. Engines map it to the base_unavailable contract code.
var ErrBaseUnavailable = errors.New("syncstate: base unavailable")

// BaseRecord is one bases.json entry: the server sha the base corresponds
// to, Hash(base bytes), and when it was stored (UTC, whole seconds).
type BaseRecord struct {
	SHA      string    `json:"sha"`
	Hash     string    `json:"hash"`
	SyncedAt time.Time `json:"synced_at"`
}

// Bases is the BASE snapshot store: .sync/base/<P> plus the bases.json
// index. Changes to the index are kept in memory until Save. A Bases is not
// safe for concurrent use; cross-process safety comes from Lock.
type Bases struct {
	hub     string
	pages   map[string]BaseRecord
	corrupt bool
}

// OpenBases loads bases.json. A missing file gives an empty set; a corrupt
// one gives an empty set with Corrupt() == true and a nil error. Other I/O
// errors are returned.
func OpenBases(hub string) (*Bases, error) {
	return nil, errNotImplemented
}

// Corrupt reports whether bases.json existed but could not be parsed.
func (b *Bases) Corrupt() bool { return b.corrupt }

// Get returns the index record for rel.
func (b *Bases) Get(rel string) (BaseRecord, bool) {
	return BaseRecord{}, false
}

// Valid reports whether rel has a valid base for stateSHA (the state.json
// page_shas value): stateSHA != "", the record's SHA == stateSHA and
// base/<rel> exists.
func (b *Bases) Valid(rel, stateSHA string) bool {
	return false
}

// Read returns the base bytes of rel verbatim.
func (b *Bases) Read(rel string) ([]byte, error) {
	return nil, errNotImplemented
}

// ReadVerified is Read that also checks Hash(bytes) == the record's Hash.
// Every failure wraps ErrBaseUnavailable.
func (b *Bases) ReadVerified(rel string) ([]byte, error) {
	return nil, errNotImplemented
}

// Put atomically writes content verbatim to base/<rel> and indexes it as
// {sha, Hash(content), now in UTC whole seconds}. The index is persisted by
// Save.
func (b *Bases) Put(rel string, content []byte, sha string, now time.Time) error {
	return errNotImplemented
}

// Delete removes base/<rel> and its index record (persisted by Save).
func (b *Bases) Delete(rel string) error {
	return errNotImplemented
}

// Paths returns the indexed paths sorted in byte order.
func (b *Bases) Paths() []string {
	return nil
}

// Save atomically writes bases.json:
// {"schema":"ctx.bases.v1","pages":{P:{"sha","hash","synced_at"}}}.
func (b *Bases) Save() error {
	return errNotImplemented
}
