package syncops

// Frozen after wave 0 (WP-0.1): the E9 online sidecar override shared by
// status, push, pull and resolve.

import (
	"reflect"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

func TestApplySidecarOnlyOverridesModified(t *testing.T) {
	const (
		baseSHA  = "1111111111111111111111111111111111111111"
		otherSHA = "2222222222222222222222222222222222222222"
	)

	// Every page state, plus "" (emit no entry). Conflict carries a kind;
	// each input has a note so we can see notes survive.
	inputs := []contract.ClassifyResult{
		{State: contract.StateUnchanged, Notes: []contract.NoteCode{}},
		{State: contract.StateModified, Notes: []contract.NoteCode{contract.NoteCaseCollision}},
		{State: contract.StateNew, Notes: []contract.NoteCode{contract.NoteMissingOnServer}},
		{State: contract.StateIncoming, Notes: []contract.NoteCode{}},
		{State: contract.StateIncomingNew, Notes: []contract.NoteCode{}},
		{State: contract.StateConflict, ConflictKind: contract.ConflictBothAdded, Notes: []contract.NoteCode{}},
		{State: contract.StateDeletedLocal, Notes: []contract.NoteCode{contract.NoteRemoteChanged}},
		{State: contract.StateInvalid, Notes: []contract.NoteCode{contract.NoteRemoteChanged}},
		{State: contract.StateUnverified, Notes: []contract.NoteCode{}},
		{State: "", Notes: []contract.NoteCode{}},
	}
	states := map[contract.PageState]bool{}
	for _, in := range inputs {
		if in.State != "" {
			states[in.State] = true
		}
	}
	if len(states) != 9 {
		t.Fatalf("table covers %d page states, want all 9", len(states))
	}

	sidecars := []struct {
		name string
		sc   *SidecarFact
	}{
		{"nil", nil},
		{"same sha", &SidecarFact{ServerSHA: baseSHA, Kind: contract.ConflictBothModified}},
		{"other sha both_modified", &SidecarFact{ServerSHA: otherSHA, Kind: contract.ConflictBothModified}},
		{"other sha both_added", &SidecarFact{ServerSHA: otherSHA, Kind: contract.ConflictBothAdded}},
		{"other sha empty kind", &SidecarFact{ServerSHA: otherSHA}},
	}

	for _, in := range inputs {
		for _, s := range sidecars {
			name := string(in.State)
			if name == "" {
				name = "no-entry"
			}
			t.Run(name+"/"+s.name, func(t *testing.T) {
				orig := wp01CloneResult(in)
				got := ApplySidecar(in, baseSHA, s.sc)

				want := wp01CloneResult(in)
				if in.State == contract.StateModified && s.sc != nil && s.sc.ServerSHA != baseSHA {
					want.State = contract.StateConflict
					want.ConflictKind = s.sc.Kind
					if want.ConflictKind == "" {
						want.ConflictKind = contract.ConflictBothModified
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("ApplySidecar(%+v, base, %+v) = %+v, want %+v", in, s.sc, got, want)
				}
				if !reflect.DeepEqual(in, orig) {
					t.Fatalf("ApplySidecar mutated its input: %+v, was %+v", in, orig)
				}
			})
		}
	}
}

// TestApplySidecarEmptyBaseSHA: a both_added sidecar recorded for a path
// with no base (base sha "") still overrides a modified result.
func TestApplySidecarEmptyBaseSHA(t *testing.T) {
	in := contract.ClassifyResult{State: contract.StateModified, Notes: []contract.NoteCode{}}
	sc := &SidecarFact{ServerSHA: "3333333333333333333333333333333333333333", Kind: contract.ConflictBothAdded}
	got := ApplySidecar(in, "", sc)
	if got.State != contract.StateConflict || got.ConflictKind != contract.ConflictBothAdded {
		t.Fatalf("got %+v, want conflict/both_added", got)
	}
}

func wp01CloneResult(r contract.ClassifyResult) contract.ClassifyResult {
	c := r
	if r.Notes != nil {
		c.Notes = append([]contract.NoteCode{}, r.Notes...)
	}
	return c
}
