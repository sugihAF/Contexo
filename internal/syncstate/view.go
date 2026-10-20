package syncstate

import (
	"sort"

	syncpkg "github.com/sugihAF/contexo/internal/sync"
)

// Side kinds. The values equal the contract LocalKind/BaseKind/RemoteKind
// literals, so contract.Side(x) converts a Side without mapping.
const (
	LocalAbsent  = "absent"
	LocalInvalid = "invalid"
	LocalValid   = "valid"

	BaseNone  = "none"
	BaseValid = "valid"
	BaseStale = "stale"

	RemoteUnknown = "unknown"
	RemoteAbsent  = "absent"
	RemotePresent = "present"
)

// Side is one input of the three-way state. The field order (Kind, SHA,
// Hash) is frozen and identical to contract.Side.
type Side struct {
	Kind string
	SHA  string
	Hash string
}

// PathFacts are the gathered inputs for one entry path. File, BaseRecord
// and Conflict are nil when absent. RemoteFromSidecar is true when Remote
// came from a conflict sidecar (offline rule E9) instead of a live listing.
type PathFacts struct {
	Path              string
	Local             Side
	Base              Side
	Remote            Side
	RemoteFromSidecar bool
	File              *LocalFile
	BaseRecord        *BaseRecord
	Conflict          *ConflictRecord
}

// View is the per-path three-sided snapshot of a hub. Corrupt lists the
// index files that existed but could not be parsed ("state.json",
// "bases.json", "conflicts.json"); each was treated as empty.
type View struct {
	Paths     []string // sorted in byte order
	Facts     map[string]*PathFacts
	State     *syncpkg.State
	Bases     *Bases
	Conflicts *Conflicts
	Scan      *LocalScan
	Corrupt   []string
}

// LoadView reads state.json, bases.json, conflicts.json and the working
// files and gathers PathFacts for every entry: working files ∪ page_shas
// keys with a non-empty value ∪ remote keys. remote maps path to the last
// commit that touched it; nil means no live listing, and an empty map means
// the repo is missing (every path absent). Base is valid per Bases.Valid,
// stale when page_shas has the path but the base is not valid, none
// otherwise. With remote == nil the E9 offline rule applies: Remote is
// present(rec.ServerSHA) with RemoteFromSidecar when a sidecar rec exists
// and rec.ServerSHA != page_shas[P], otherwise unknown. With a live map,
// Remote comes only from the map. LoadView only reads.
//
// A page_shas or remote key that fails ValidateRelPath, or is the root
// index.md or tags.md, names no page and makes no entry; every working file
// is an entry. A base record or a sidecar alone makes no entry. Paths and
// Corrupt are never nil. Errors are I/O errors of the index files or the
// scan, including a missing hub.
func LoadView(hub string, remote map[string]string) (*View, error) {
	st, stateCorrupt, err := syncpkg.LoadStateOrEmpty(hub)
	if err != nil {
		return nil, err
	}
	bases, err := OpenBases(hub)
	if err != nil {
		return nil, err
	}
	conflicts, err := OpenConflicts(hub)
	if err != nil {
		return nil, err
	}
	scan, err := ScanLocal(hub)
	if err != nil {
		return nil, err
	}
	v := &View{
		Facts:     map[string]*PathFacts{},
		State:     st,
		Bases:     bases,
		Conflicts: conflicts,
		Scan:      scan,
		Corrupt:   []string{},
	}
	for _, idx := range []struct {
		name    string
		corrupt bool
	}{
		{"state.json", stateCorrupt},
		{"bases.json", bases.Corrupt()},
		{"conflicts.json", conflicts.Corrupt()},
	} {
		if idx.corrupt {
			v.Corrupt = append(v.Corrupt, idx.name)
		}
	}
	v.Paths = viewEntries(scan, st.PageSHAs, remote)
	for _, rel := range v.Paths {
		v.Facts[rel] = v.gather(rel, remote)
	}
	return v, nil
}

// viewEntries returns the entry set, sorted in byte order: the working
// files, the page_shas keys with a non-empty value and the remote keys,
// the last two only where listedPage accepts them.
func viewEntries(scan *LocalScan, pageSHAs, remote map[string]string) []string {
	set := make(map[string]bool, len(scan.Paths)+len(pageSHAs)+len(remote))
	for _, rel := range scan.Paths {
		set[rel] = true
	}
	for rel, sha := range pageSHAs {
		if sha != "" && listedPage(rel) {
			set[rel] = true
		}
	}
	for rel := range remote {
		if listedPage(rel) {
			set[rel] = true
		}
	}
	paths := make([]string, 0, len(set))
	for rel := range set {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths
}

// listedPage reports whether rel, a page_shas or remote key, names a page:
// a valid page path other than the root index.md and tags.md, which the
// scan skips as well. Other keys were not written for a page and would
// only show up as phantom entries.
func listedPage(rel string) bool {
	return rel != "index.md" && rel != "tags.md" && ValidateRelPath(rel) == nil
}

// gather builds the PathFacts of the entry rel from the loaded view.
func (v *View) gather(rel string, remote map[string]string) *PathFacts {
	f := &PathFacts{Path: rel, Local: Side{Kind: LocalAbsent}, Base: Side{Kind: BaseNone}}
	if lf, ok := v.Scan.Files[rel]; ok {
		f.File = lf
		f.Local = Side{Kind: LocalInvalid}
		if lf.Invalid == nil {
			f.Local = Side{Kind: LocalValid, Hash: lf.Hash}
		}
	}

	sb := v.State.PageSHAs[rel]
	if rec, ok := v.Bases.Get(rel); ok {
		f.BaseRecord = &rec
	}
	switch {
	case sb == "":
	case v.Bases.Valid(rel, sb):
		f.Base = Side{Kind: BaseValid, SHA: sb, Hash: f.BaseRecord.Hash}
	default:
		f.Base = Side{Kind: BaseStale, SHA: sb}
	}

	if rec, ok := v.Conflicts.Get(rel); ok {
		f.Conflict = &rec
	}
	switch {
	case remote != nil:
		f.Remote = Side{Kind: RemoteAbsent}
		if sr, ok := remote[rel]; ok {
			f.Remote = Side{Kind: RemotePresent, SHA: sr}
		}
	case f.Conflict != nil && f.Conflict.ServerSHA != sb:
		// E9: offline, a sidecar whose server version is not the base
		// stands in for the listing.
		f.Remote = Side{Kind: RemotePresent, SHA: f.Conflict.ServerSHA}
		f.RemoteFromSidecar = true
	default:
		f.Remote = Side{Kind: RemoteUnknown}
	}
	return f
}
