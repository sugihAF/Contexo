package syncstate

import syncpkg "github.com/sugihAF/contexo/internal/sync"

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
func LoadView(hub string, remote map[string]string) (*View, error) {
	return nil, errNotImplemented
}
