package syncstate

import "time"

// Conflict kinds and sidecar sources (E8/E10). The values equal the
// contract literals; engines convert with contract.ConflictKind(rec.Kind).
const (
	KindBothModified = "both_modified"
	KindBothAdded    = "both_added"

	SourcePush   = "push"
	SourcePull   = "pull"
	SourceRepair = "repair"
)

// ConflictRecord is one conflicts.json entry: a server version that
// conflicts with the local file. Path is the map key and is not marshalled.
// BaseSHA is omitted for both_added.
type ConflictRecord struct {
	Path       string    `json:"-"`
	ServerSHA  string    `json:"server_sha"`
	BaseSHA    string    `json:"base_sha,omitempty"`
	Kind       string    `json:"kind"`
	Source     string    `json:"source"`
	DetectedAt time.Time `json:"detected_at"`
	HasContent bool      `json:"has_content"`
}

// Conflicts is the conflict sidecar store: .sync/conflicts/<P> plus the
// conflicts.json index. Changes to the index are kept in memory until Save.
// A Conflicts is not safe for concurrent use; cross-process safety comes
// from Lock.
type Conflicts struct {
	hub     string
	recs    map[string]ConflictRecord
	corrupt bool
}

// OpenConflicts loads conflicts.json. A missing file gives an empty set; a
// corrupt one gives an empty set with Corrupt() == true and a nil error.
func OpenConflicts(hub string) (*Conflicts, error) {
	return nil, errNotImplemented
}

// Corrupt reports whether conflicts.json existed but could not be parsed.
func (c *Conflicts) Corrupt() bool { return c.corrupt }

// Put records rec for rec.Path. serverContent (the caller passes the
// sanitized server bytes) is stored verbatim at conflicts/<P>, and
// HasContent = len(serverContent) > 0. The index is persisted by Save.
func (c *Conflicts) Put(rec ConflictRecord, serverContent []byte) error {
	return errNotImplemented
}

// Get returns the record for rel.
func (c *Conflicts) Get(rel string) (ConflictRecord, bool) {
	return ConflictRecord{}, false
}

// Content returns the stored server bytes for rel.
func (c *Conflicts) Content(rel string) ([]byte, error) {
	return nil, errNotImplemented
}

// List returns every record sorted by Path in byte order.
func (c *Conflicts) List() []ConflictRecord {
	return nil
}

// Clear removes conflicts/<rel> and its record (persisted by Save).
func (c *Conflicts) Clear(rel string) error {
	return errNotImplemented
}

// Save atomically writes conflicts.json:
// {"schema":"ctx.conflicts.v1","conflicts":{P:{...}}}.
func (c *Conflicts) Save() error {
	return errNotImplemented
}
