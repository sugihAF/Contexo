package syncops

// Owner: WP-1.5. TestRepairStubNotImplemented is the wave-0 stub assertion
// (replace it when Repair is implemented). TestRepairResultJSONKeyOrder pins
// the E18 wire shape of `ctx repair --json` and stays. Tests that need
// syncopstest.FakeRemote must live in package syncops_test (syncopstest
// imports syncops).

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

func TestRepairStubNotImplemented(t *testing.T) {
	env := wp01StubEnv(t)
	for _, dry := range []bool{false, true} {
		res, err := Repair(context.Background(), env, RepairOptions{DryRun: dry})
		wp01AssertNotImplemented(t, err, "repair")
		if res != nil {
			t.Fatalf("Repair(dry_run=%v) returned a result with the stub error: %+v", dry, res)
		}
	}
}

func TestRepairResultJSONKeyOrder(t *testing.T) {
	const (
		shaA = "1111111111111111111111111111111111111111"
		shaB = "2222222222222222222222222222222222222222"
	)
	arrays := []string{"server_capabilities", "backfilled", "repaired", "skipped", "unverified", "warnings"}

	t.Run("full", func(t *testing.T) {
		r := NewRepairResult(true)
		r.ServerCapabilities = append(r.ServerCapabilities, contract.SrvPagesAt)
		r.Backfilled = append(r.Backfilled, RepairBackfilled{Path: "wiki/concepts/a.md", SHA: shaA})
		r.Repaired = append(r.Repaired, RepairedPage{Path: "wiki/concepts/b.md", OldSHA: shaA, NewSHA: shaB, Reason: "adopted"})
		r.Skipped = append(r.Skipped, RepairSkipped{Path: "wiki/concepts/c.md", Reason: "invalid"})
		r.Unverified = append(r.Unverified, "wiki/concepts/d.md")
		r.Warnings = append(r.Warnings, contract.Warning{
			Code: contract.WarnBaseRepairSkipped, Message: "1 page left unverified", Paths: []string{"wiki/concepts/d.md"},
		})

		raw := wp01WriteDoc(t, r)
		wp01AssertKeys(t, "top level", raw, "schema", "dry_run", "server_capabilities",
			"backfilled", "repaired", "skipped", "unverified", "warnings")

		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		if string(top["schema"]) != `"ctx.repair.v1"` || string(top["dry_run"]) != "true" {
			t.Fatalf("schema = %s, dry_run = %s", top["schema"], top["dry_run"])
		}
		wp01AssertKeys(t, "backfilled[0]", wp01First(t, top["backfilled"]), "path", "sha")
		wp01AssertKeys(t, "repaired[0]", wp01First(t, top["repaired"]), "path", "old_sha", "new_sha", "reason")
		wp01AssertKeys(t, "skipped[0]", wp01First(t, top["skipped"]), "path", "reason")
		wp01AssertKeys(t, "warnings[0]", wp01First(t, top["warnings"]), "code", "message", "paths")

		// It strictly round-trips.
		var back RepairResult
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&back); err != nil {
			t.Fatalf("strict decode: %v", err)
		}
		if !reflect.DeepEqual(&back, r) {
			t.Fatalf("round trip = %+v, want %+v", back, *r)
		}
	})

	t.Run("empty arrays are [] not null", func(t *testing.T) {
		r := NewRepairResult(false)
		raw := wp01WriteDoc(t, r)
		wp01AssertKeys(t, "top level", raw, "schema", "dry_run", "server_capabilities",
			"backfilled", "repaired", "skipped", "unverified", "warnings")
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		if string(top["schema"]) != `"ctx.repair.v1"` || string(top["dry_run"]) != "false" {
			t.Fatalf("schema = %s, dry_run = %s", top["schema"], top["dry_run"])
		}
		for _, k := range arrays {
			if got := string(top[k]); got != "[]" {
				t.Errorf("%s = %s, want []", k, got)
			}
		}
	})
}

func wp01WriteDoc(t *testing.T, doc any) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := contract.WriteDoc(&buf, doc); err != nil {
		t.Fatalf("WriteDoc: %v", err)
	}
	return buf.Bytes()
}

// wp01AssertKeys asserts raw is a JSON object whose keys are exactly want,
// in order.
func wp01AssertKeys(t *testing.T, what string, raw []byte, want ...string) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		t.Fatalf("%s: not a JSON object (%v, %v): %s", what, tok, err, raw)
	}
	var got []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		got = append(got, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s keys = %q, want %q", what, got, want)
	}
}

// wp01First returns the first element of a JSON array.
func wp01First(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil || len(arr) == 0 {
		t.Fatalf("not a non-empty array (%v): %s", err, raw)
	}
	return arr[0]
}
