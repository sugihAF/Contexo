package syncstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sugihAF/contexo/internal/fsutil"
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
//
// The file is corrupt when it is not a JSON object, its "schema" is not
// SchemaBases, "pages" is missing, null or not an object, a record is null
// or mistyped, a key fails ValidateRelPath, or a record is one Put never
// writes (an empty sha, or a hash not of the form Hash produces). Unknown
// fields are ignored.
func OpenBases(hub string) (*Bases, error) {
	b := &Bases{hub: hub, pages: map[string]BaseRecord{}}
	data, err := os.ReadFile(BasesPath(hub))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return b, nil
	case err != nil:
		return nil, fmt.Errorf("syncstate: open bases: %w", err)
	}
	recs, ok := decodeIndexFile[BaseRecord](data, SchemaBases, "pages")
	for _, rec := range recs {
		if !ok {
			break
		}
		ok = rec.SHA != "" && validHashForm(rec.Hash)
	}
	if !ok {
		b.corrupt = true
		return b, nil
	}
	for rel, rec := range recs {
		rec.SyncedAt = indexTime(rec.SyncedAt)
		b.pages[rel] = *rec
	}
	return b, nil
}

// validHashForm reports whether h has the form Hash produces: "sha256:" or
// "raw-sha256:" followed by 64 lowercase hex digits.
func validHashForm(h string) bool {
	hexPart, ok := strings.CutPrefix(h, normHashPrefix)
	if !ok {
		hexPart, ok = strings.CutPrefix(h, rawHashPrefix)
	}
	if !ok || len(hexPart) != 64 {
		return false
	}
	for i := 0; i < len(hexPart); i++ {
		if c := hexPart[i]; !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// Corrupt reports whether bases.json existed but could not be parsed.
func (b *Bases) Corrupt() bool { return b.corrupt }

// Get returns the index record for rel.
func (b *Bases) Get(rel string) (BaseRecord, bool) {
	rec, ok := b.pages[rel]
	return rec, ok
}

// Valid reports whether rel has a valid base for stateSHA (the state.json
// page_shas value): stateSHA != "", the record's SHA == stateSHA and
// base/<rel> exists.
func (b *Bases) Valid(rel, stateSHA string) bool {
	rec, ok := b.pages[rel]
	if !ok || stateSHA == "" || rec.SHA != stateSHA {
		return false
	}
	// Indexed paths have passed ValidateRelPath, so BaseFile stays in base/.
	fi, err := os.Stat(BaseFile(b.hub, rel))
	return err == nil && fi.Mode().IsRegular()
}

// Read returns the base bytes of rel verbatim. A rel failing
// ValidateRelPath gives a *PathError.
func (b *Bases) Read(rel string) ([]byte, error) {
	if err := ValidateRelPath(rel); err != nil {
		return nil, err
	}
	return os.ReadFile(BaseFile(b.hub, rel))
}

// ReadVerified is Read that also checks Hash(bytes) == the record's Hash.
// Every failure wraps ErrBaseUnavailable; a rel failing ValidateRelPath
// also wraps its *PathError.
func (b *Bases) ReadVerified(rel string) ([]byte, error) {
	if err := ValidateRelPath(rel); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBaseUnavailable, err)
	}
	rec, ok := b.pages[rel]
	if !ok {
		return nil, fmt.Errorf("%w: %s has no base record", ErrBaseUnavailable, rel)
	}
	data, err := os.ReadFile(BaseFile(b.hub, rel))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBaseUnavailable, err)
	}
	if Hash(data) != rec.Hash {
		return nil, fmt.Errorf("%w: %s does not match its recorded hash", ErrBaseUnavailable, rel)
	}
	return data, nil
}

// Put atomically writes content verbatim to base/<rel> and indexes it as
// {sha, Hash(content), now in UTC whole seconds}. The index is persisted by
// Save. A zero now means time.Now(). A rel failing ValidateRelPath gives a
// *PathError and an empty sha (never a valid base) an error; both write
// nothing.
func (b *Bases) Put(rel string, content []byte, sha string, now time.Time) error {
	if err := ValidateRelPath(rel); err != nil {
		return err
	}
	if sha == "" {
		return fmt.Errorf("syncstate: base of %s: empty sha", rel)
	}
	if now.IsZero() {
		now = time.Now()
	}
	if err := EnsureSyncDir(b.hub); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(BaseFile(b.hub, rel), content, 0o644); err != nil {
		return err
	}
	if b.pages == nil {
		b.pages = map[string]BaseRecord{}
	}
	b.pages[rel] = BaseRecord{SHA: sha, Hash: Hash(content), SyncedAt: indexTime(now)}
	return nil
}

// Delete removes base/<rel> and its index record (persisted by Save). A
// missing file or record is not an error; a rel failing ValidateRelPath
// gives a *PathError.
func (b *Bases) Delete(rel string) error {
	if err := ValidateRelPath(rel); err != nil {
		return err
	}
	// BaseFile(hub, "") is the .sync/base directory itself.
	if err := removeStoreFile(BaseFile(b.hub, ""), BaseFile(b.hub, rel)); err != nil {
		return err
	}
	delete(b.pages, rel)
	return nil
}

// Paths returns the indexed paths sorted in byte order.
func (b *Bases) Paths() []string {
	paths := make([]string, 0, len(b.pages))
	for rel := range b.pages {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths
}

// Save atomically writes bases.json:
// {"schema":"ctx.bases.v1","pages":{P:{"sha","hash","synced_at"}}}.
func (b *Bases) Save() error {
	pages := b.pages
	if pages == nil {
		pages = map[string]BaseRecord{}
	}
	return writeIndexFile(b.hub, BasesPath(b.hub), struct {
		Schema string                `json:"schema"`
		Pages  map[string]BaseRecord `json:"pages"`
	}{SchemaBases, pages})
}

// indexTime is t as the index files store it: UTC, whole seconds.
func indexTime(t time.Time) time.Time {
	return t.UTC().Truncate(time.Second)
}

// decodeIndexFile parses an index file shared by bases.json and
// conflicts.json: a JSON object whose "schema" member is schema and whose
// key member is an object of records keyed by page path. It reports false
// (corrupt) for anything else, including a null record or a key that fails
// ValidateRelPath; such a file was not written by this layer.
func decodeIndexFile[T any](data []byte, schema, key string) (map[string]*T, bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil || top == nil {
		return nil, false
	}
	var got string
	if err := json.Unmarshal(top["schema"], &got); err != nil || got != schema {
		return nil, false
	}
	var recs map[string]*T
	if err := json.Unmarshal(top[key], &recs); err != nil || recs == nil {
		return nil, false
	}
	for rel, rec := range recs {
		if rec == nil || ValidateRelPath(rel) != nil {
			return nil, false
		}
	}
	return recs, true
}

// writeIndexFile atomically writes v as an index file: two-space indented
// JSON plus a trailing newline, after EnsureSyncDir.
func writeIndexFile(hub, path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := EnsureSyncDir(hub); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, append(data, '\n'), 0o644)
}

// removeStoreFile removes path, a file below the store directory root
// (.sync/base or .sync/conflicts), then every directory between them that
// the removal left empty, so a later page can reuse the name. root itself
// is kept. A missing file is not an error. The file removal is retried like
// the lock's (removeRetry): on Windows a reader that has it open makes the
// delete fail transiently.
func removeStoreFile(root, path string) error {
	if err := removeRetry(os.Remove, isTransientLockErr, path); err != nil {
		return err
	}
	prefix := root + string(filepath.Separator)
	for dir := filepath.Dir(path); strings.HasPrefix(dir, prefix); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break // not empty, so no directory above it is empty either
		}
	}
	return nil
}
