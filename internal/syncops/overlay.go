package syncops

import "github.com/sugihAF/contexo/internal/syncops/contract"

// SidecarFact is the part of a conflict sidecar (.sync/conflicts.json record)
// the online override needs. It takes primitives so syncops never imports
// syncstate.
type SidecarFact struct {
	ServerSHA string
	Kind      contract.ConflictKind
}

// ApplySidecar is the E9 online override, shared by status, push, pull and
// resolve after contract.Classify: a modified result becomes a conflict of
// kind sc.Kind (both_modified when empty) iff a sidecar exists whose
// ServerSHA differs from the base sha. Every other result is returned
// unchanged, so contract.Classify stays pure.
func ApplySidecar(res contract.ClassifyResult, baseSHA string, sc *SidecarFact) contract.ClassifyResult {
	if sc == nil || sc.ServerSHA == baseSHA || res.State != contract.StateModified {
		return res
	}
	res.State = contract.StateConflict
	res.ConflictKind = sc.Kind
	if res.ConflictKind == "" {
		res.ConflictKind = contract.ConflictBothModified
	}
	return res
}
