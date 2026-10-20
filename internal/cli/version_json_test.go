package cli

// Frozen `ctx version --json` and capability-registry tests (WP-0.1, E19).
// They must survive wave 1: every WP that implements a capability registers
// it from an init() in its own file, so these tests assert membership and
// ORDER, never equality with a fixed list.

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/version"
)

// wp01ContractOrder is the advertised order: contract.Capabilities, then
// contract.OptionalCapabilities.
func wp01ContractOrder() map[contract.Capability]int {
	pos := map[contract.Capability]int{}
	for _, c := range append(append([]contract.Capability{}, contract.Capabilities...), contract.OptionalCapabilities...) {
		pos[c] = len(pos)
	}
	return pos
}

// wp01CheckCapabilities asserts the E19 rules on an advertised list.
func wp01CheckCapabilities(t *testing.T, caps []contract.Capability) {
	t.Helper()
	pos := wp01ContractOrder()
	seen := map[contract.Capability]bool{}
	last := -1
	for _, c := range caps {
		if !contract.KnownCapability(c) {
			t.Errorf("unknown capability %q advertised", c)
			continue
		}
		if seen[c] {
			t.Errorf("capability %q advertised twice", c)
		}
		seen[c] = true
		if pos[c] < last {
			t.Errorf("capability %q out of contract order in %q", c, caps)
		}
		last = pos[c]
	}
	if !seen[contract.CapErrorEnvelope] {
		t.Errorf("errors.envelope missing from %q", caps)
	}
}

// wp01DecodeVersionDoc strictly decodes stdout as exactly one VersionDoc.
func wp01DecodeVersionDoc(t *testing.T, stdout string) contract.VersionDoc {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	var doc contract.VersionDoc
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not a strict VersionDoc: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("stdout holds more than one document: %q", stdout)
	}
	return doc
}

func TestVersionJSON(t *testing.T) {
	code, stdout, stderr := wp01Run(t, "version", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, stderr %q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr must be empty on success, got %q", stderr)
	}
	doc := wp01DecodeVersionDoc(t, stdout)

	if doc.Schema != "ctx.version.v1" {
		t.Errorf("schema = %q", doc.Schema)
	}
	if doc.ContractVersion != 1 {
		t.Errorf("contract_version = %d", doc.ContractVersion)
	}
	if doc.Version != version.Version || doc.Commit != version.Commit || doc.Date != version.Date {
		t.Errorf("identity = %q/%q/%q, want %q/%q/%q", doc.Version, doc.Commit, doc.Date,
			version.Version, version.Commit, version.Date)
	}
	if doc.DevBuild != version.IsDevBuild() {
		t.Errorf("dev_build = %v", doc.DevBuild)
	}
	if doc.OS != runtime.GOOS || doc.Arch != runtime.GOARCH {
		t.Errorf("os/arch = %s/%s", doc.OS, doc.Arch)
	}
	if doc.Capabilities == nil || !strings.Contains(stdout, `"capabilities": [`) {
		t.Errorf("capabilities must be an array, got %q", stdout)
	}
	wp01CheckCapabilities(t, doc.Capabilities)

	// Byte-for-byte the WriteDoc form: 2-space indent, trailing newline.
	var want bytes.Buffer
	if err := contract.WriteDoc(&want, doc); err != nil {
		t.Fatal(err)
	}
	if stdout != want.String() {
		t.Errorf("stdout is not WriteDoc output:\n got %q\nwant %q", stdout, want.String())
	}
}

// --json wins over --short, in either order.
func TestVersionJSONBeatsShort(t *testing.T) {
	for _, args := range [][]string{
		{"version", "--short", "--json"},
		{"version", "--json", "--short"},
	} {
		code, stdout, stderr := wp01Run(t, args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%q: exit %d stderr %q", args, code, stderr)
		}
		doc := wp01DecodeVersionDoc(t, stdout)
		if doc.Schema != contract.SchemaVersion {
			t.Errorf("%q: schema = %q", args, doc.Schema)
		}
	}
	// --short alone is unchanged: just the version and a newline.
	code, stdout, _ := wp01Run(t, "version", "--short")
	if code != 0 || stdout != version.Version+"\n" {
		t.Errorf("version --short = %d %q", code, stdout)
	}
}

// wp01MustPanic reports whether f panicked.
func wp01MustPanic(f func()) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	f()
	return false
}

func TestRegisterCapabilityRejectsUnknownAndDuplicate(t *testing.T) {
	reg := map[contract.Capability]bool{}

	// Every v1 capability and the optional ones register once.
	for _, c := range append(append([]contract.Capability{}, contract.Capabilities...), contract.OptionalCapabilities...) {
		if wp01MustPanic(func() { registerInto(reg, c) }) {
			t.Errorf("registerInto(%q) panicked on first registration", c)
		}
	}
	if !reg[contract.CapRepair] {
		t.Error("optional capability repair was not registered")
	}

	// A second registration panics.
	for _, c := range []contract.Capability{contract.CapStatusJSON, contract.CapErrorEnvelope, contract.CapRepair} {
		if !wp01MustPanic(func() { registerInto(reg, c) }) {
			t.Errorf("duplicate registerInto(%q) did not panic", c)
		}
	}

	// Unknown names panic, and leave the registry untouched.
	for _, c := range []contract.Capability{"pages.at", "errors.json", "show", "", "Status.JSON"} {
		fresh := map[contract.Capability]bool{}
		if !wp01MustPanic(func() { registerInto(fresh, c) }) {
			t.Errorf("unknown registerInto(%q) did not panic", c)
		}
		if len(fresh) != 0 {
			t.Errorf("unknown %q was recorded: %v", c, fresh)
		}
	}

	// The package registry already holds errors.envelope (registered by
	// capabilities.go's init), so registering it again panics.
	if !wp01MustPanic(func() { registerCapability(contract.CapErrorEnvelope) }) {
		t.Error("registerCapability(errors.envelope) twice did not panic")
	}
}

func TestAdvertisedCapabilitiesOrderAndNonNil(t *testing.T) {
	// The live registry (whatever wave 1 has added) obeys the rules.
	live := advertisedCapabilities()
	if live == nil {
		t.Fatal("advertisedCapabilities returned nil")
	}
	wp01CheckCapabilities(t, live)

	saved := capRegistry
	t.Cleanup(func() { capRegistry = saved })

	// Registration order does not matter; contract order does.
	capRegistry = map[contract.Capability]bool{}
	for _, c := range []contract.Capability{contract.CapRepair, contract.CapErrorEnvelope,
		contract.CapResolve, contract.CapStatusJSON, contract.CapShowSHA} {
		registerInto(capRegistry, c)
	}
	got := advertisedCapabilities()
	want := []contract.Capability{contract.CapStatusJSON, contract.CapShowSHA,
		contract.CapResolve, contract.CapErrorEnvelope, contract.CapRepair}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}

	// An empty registry still yields [] (never null on the wire).
	capRegistry = map[contract.Capability]bool{}
	empty := advertisedCapabilities()
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty registry gave %#v", empty)
	}
	b, err := json.Marshal(empty)
	if err != nil || string(b) != "[]" {
		t.Errorf("empty list encodes as %s (%v)", b, err)
	}
}
