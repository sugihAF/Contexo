package contract

import "fmt"

// LocalKind / BaseKind / RemoteKind are the three inputs of the 3-way state.
type LocalKind string
type BaseKind string
type RemoteKind string

const (
	LocalAbsent  LocalKind = "absent"  // no working file at the path
	LocalInvalid LocalKind = "invalid" // file exists but fails parse/validate/path check
	LocalValid   LocalKind = "valid"   // file normalizes; Hash set

	BaseNone  BaseKind = "none"  // path not in state.json page_shas
	BaseValid BaseKind = "valid" // bases.json[path].sha == page_shas[path] and base file present; Hash set
	BaseStale BaseKind = "stale" // page_shas has the path but the base record is missing or for another sha

	RemoteUnknown RemoteKind = "unknown" // no live listing (and no sidecar, E9)
	RemoteAbsent  RemoteKind = "absent"  // the live listing does not contain the path / repo missing
	RemotePresent RemoteKind = "present" // SHA set
)

type ClassifyInput struct {
	Local  LocalInput  `json:"local"`
	Base   BaseInput   `json:"base"`
	Remote RemoteInput `json:"remote"`
}

type LocalInput struct {
	Kind LocalKind `json:"kind"`
	Hash string    `json:"hash,omitempty"`
}

type BaseInput struct {
	Kind BaseKind `json:"kind"`
	SHA  string   `json:"sha,omitempty"`
	Hash string   `json:"hash,omitempty"`
}

type RemoteInput struct {
	Kind RemoteKind `json:"kind"`
	SHA  string     `json:"sha,omitempty"`
}

type ClassifyResult struct {
	State        PageState    `json:"state"` // "" means: emit no entry
	ConflictKind ConflictKind `json:"conflict_kind,omitempty"`
	Notes        []NoteCode   `json:"notes"`
}

// Classify is THE page-state decision table. Pure; no I/O. The sync engines
// gather the inputs, status/push/pull/resolve all call this.
func Classify(in ClassifyInput) ClassifyResult {
	r := ClassifyResult{Notes: []NoteCode{}}
	hasBase := in.Base.Kind != BaseNone
	remoteMoved := in.Remote.Kind == RemotePresent && hasBase && in.Remote.SHA != in.Base.SHA

	switch in.Local.Kind {
	case LocalInvalid: // rows 1-2
		r.State = StateInvalid
		if remoteMoved {
			r.Notes = append(r.Notes, NoteRemoteChanged)
		}
		return r
	case LocalAbsent:
		switch {
		case hasBase: // rows 3-5
			r.State = StateDeletedLocal
			if in.Remote.Kind == RemoteAbsent {
				r.Notes = append(r.Notes, NoteMissingOnServer)
			} else if remoteMoved {
				r.Notes = append(r.Notes, NoteRemoteChanged)
			}
		case in.Remote.Kind == RemotePresent: // row 6
			r.State = StateIncomingNew
		}
		return r // row 7: State == "" (no entry)
	}

	// LocalValid.
	switch in.Base.Kind {
	case BaseNone:
		if in.Remote.Kind == RemotePresent { // row 8
			r.State, r.ConflictKind = StateConflict, ConflictBothAdded
		} else { // rows 9-10
			r.State = StateNew
		}
	case BaseStale:
		if in.Remote.Kind == RemoteAbsent { // row 11
			r.State = StateNew
			r.Notes = append(r.Notes, NoteMissingOnServer)
		} else { // row 12
			r.State = StateUnverified
		}
	case BaseValid:
		edited := in.Local.Hash != in.Base.Hash
		switch {
		case in.Remote.Kind == RemoteAbsent: // row 13
			r.State = StateNew
			r.Notes = append(r.Notes, NoteMissingOnServer)
		case !remoteMoved && !edited: // rows 14, 16
			r.State = StateUnchanged
		case !remoteMoved && edited: // rows 15, 17
			r.State = StateModified
		case remoteMoved && !edited: // row 18
			r.State = StateIncoming
		default: // row 19
			r.State, r.ConflictKind = StateConflict, ConflictBothModified
		}
	}
	return r
}

// SCMGroup is the presentation mapping the extension uses (shared fixture).
func SCMGroup(s PageState) string {
	switch s {
	case StateConflict:
		return "conflicts"
	case StateModified, StateDeletedLocal:
		return "changes"
	case StateNew:
		return "new"
	case StateIncoming, StateIncomingNew:
		return "incoming"
	case StateInvalid:
		return "invalid"
	}
	return "" // unchanged, unverified: not in an SCM group
}

// Side is one untyped input side, as gathered by the engines. Its field
// ORDER is frozen so that syncstate.Side converts with contract.Side(x).
// SHA is ignored for the local side and Hash for the remote side.
type Side struct {
	Kind string
	SHA  string
	Hash string
}

// ClassifySides is Classify over untyped sides. It returns an error when a
// side's Kind is not one of that side's kinds.
func ClassifySides(local, base, remote Side) (ClassifyResult, error) {
	switch LocalKind(local.Kind) {
	case LocalAbsent, LocalInvalid, LocalValid:
	default:
		return ClassifyResult{}, fmt.Errorf("contract: unknown local kind %q", local.Kind)
	}
	switch BaseKind(base.Kind) {
	case BaseNone, BaseValid, BaseStale:
	default:
		return ClassifyResult{}, fmt.Errorf("contract: unknown base kind %q", base.Kind)
	}
	switch RemoteKind(remote.Kind) {
	case RemoteUnknown, RemoteAbsent, RemotePresent:
	default:
		return ClassifyResult{}, fmt.Errorf("contract: unknown remote kind %q", remote.Kind)
	}
	return Classify(ClassifyInput{
		Local:  LocalInput{Kind: LocalKind(local.Kind), Hash: local.Hash},
		Base:   BaseInput{Kind: BaseKind(base.Kind), SHA: base.SHA, Hash: base.Hash},
		Remote: RemoteInput{Kind: RemoteKind(remote.Kind), SHA: remote.SHA},
	}), nil
}
