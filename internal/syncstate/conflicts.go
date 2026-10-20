package syncstate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"time"

	"github.com/sugihAF/contexo/internal/fsutil"
)

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
// Other I/O errors are returned.
//
// Corrupt is decided as for bases.json (schema SchemaConflicts, records
// under "conflicts"), and also when a record fails the Put checks: an
// unknown kind or source, or an empty server_sha. Loaded records are
// normalized like Put: no base_sha for both_added, detected_at in UTC whole
// seconds.
func OpenConflicts(hub string) (*Conflicts, error) {
	c := &Conflicts{hub: hub, recs: map[string]ConflictRecord{}}
	data, err := os.ReadFile(ConflictsPath(hub))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return c, nil
	case err != nil:
		return nil, fmt.Errorf("syncstate: open conflicts: %w", err)
	}
	recs, ok := decodeIndexFile[ConflictRecord](data, SchemaConflicts, "conflicts")
	for rel, rec := range recs {
		rec.Path = rel
		if checkConflict(*rec) != nil {
			ok = false
			break
		}
	}
	if !ok {
		c.corrupt = true
		return c, nil
	}
	for rel, rec := range recs {
		c.recs[rel] = normalizeConflict(*rec)
	}
	return c, nil
}

// Corrupt reports whether conflicts.json existed but could not be parsed.
func (c *Conflicts) Corrupt() bool { return c.corrupt }

// Put records rec for rec.Path. serverContent (the caller passes the
// sanitized server bytes) is stored verbatim at conflicts/<P>, and
// HasContent = len(serverContent) > 0. The index is persisted by Save.
//
// rec.Path must pass ValidateRelPath (else a *PathError), Kind must be a
// Kind* constant, Source a Source* constant and ServerSHA non-empty; a
// rejected record writes nothing. BaseSHA is dropped for both_added, a zero
// DetectedAt means time.Now(), and DetectedAt is stored in UTC whole
// seconds. Without content, an older conflicts/<P> file is removed.
func (c *Conflicts) Put(rec ConflictRecord, serverContent []byte) error {
	if err := ValidateRelPath(rec.Path); err != nil {
		return err
	}
	if err := checkConflict(rec); err != nil {
		return err
	}
	if rec.DetectedAt.IsZero() {
		rec.DetectedAt = time.Now()
	}
	rec = normalizeConflict(rec)
	rec.HasContent = len(serverContent) > 0
	if err := EnsureSyncDir(c.hub); err != nil {
		return err
	}
	file := ConflictFile(c.hub, rec.Path)
	var err error
	if rec.HasContent {
		err = fsutil.WriteFileAtomic(file, serverContent, 0o644)
	} else {
		// ConflictFile(hub, "") is the .sync/conflicts directory itself.
		err = removeStoreFile(ConflictFile(c.hub, ""), file)
	}
	if err != nil {
		return err
	}
	if c.recs == nil {
		c.recs = map[string]ConflictRecord{}
	}
	c.recs[rec.Path] = rec
	return nil
}

// Get returns the record for rel.
func (c *Conflicts) Get(rel string) (ConflictRecord, bool) {
	rec, ok := c.recs[rel]
	return rec, ok
}

// Content returns the stored server bytes for rel. A rel failing
// ValidateRelPath gives a *PathError; a path without stored content gives
// an error wrapping fs.ErrNotExist.
func (c *Conflicts) Content(rel string) ([]byte, error) {
	if err := ValidateRelPath(rel); err != nil {
		return nil, err
	}
	return os.ReadFile(ConflictFile(c.hub, rel))
}

// List returns every record sorted by Path in byte order.
func (c *Conflicts) List() []ConflictRecord {
	out := make([]ConflictRecord, 0, len(c.recs))
	for _, rec := range c.recs {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Clear removes conflicts/<rel> and its record (persisted by Save). A
// missing file or record is not an error; a rel failing ValidateRelPath
// gives a *PathError.
func (c *Conflicts) Clear(rel string) error {
	if err := ValidateRelPath(rel); err != nil {
		return err
	}
	if err := removeStoreFile(ConflictFile(c.hub, ""), ConflictFile(c.hub, rel)); err != nil {
		return err
	}
	delete(c.recs, rel)
	return nil
}

// Save atomically writes conflicts.json:
// {"schema":"ctx.conflicts.v1","conflicts":{P:{...}}}.
func (c *Conflicts) Save() error {
	recs := c.recs
	if recs == nil {
		recs = map[string]ConflictRecord{}
	}
	return writeIndexFile(c.hub, ConflictsPath(c.hub), struct {
		Schema    string                    `json:"schema"`
		Conflicts map[string]ConflictRecord `json:"conflicts"`
	}{SchemaConflicts, recs})
}

// checkConflict reports a record whose fields fall outside E8/E10: the
// kind and source enums and a non-empty server sha. It does not check Path.
func checkConflict(rec ConflictRecord) error {
	switch rec.Kind {
	case KindBothModified, KindBothAdded:
	default:
		return fmt.Errorf("syncstate: conflict %s: unknown kind %q", rec.Path, rec.Kind)
	}
	switch rec.Source {
	case SourcePush, SourcePull, SourceRepair:
	default:
		return fmt.Errorf("syncstate: conflict %s: unknown source %q", rec.Path, rec.Source)
	}
	if rec.ServerSHA == "" {
		return fmt.Errorf("syncstate: conflict %s: empty server sha", rec.Path)
	}
	return nil
}

// normalizeConflict returns rec as it is indexed: no BaseSHA for
// both_added (it has no base) and DetectedAt in UTC whole seconds.
func normalizeConflict(rec ConflictRecord) ConflictRecord {
	if rec.Kind == KindBothAdded {
		rec.BaseSHA = ""
	}
	rec.DetectedAt = indexTime(rec.DetectedAt)
	return rec
}
