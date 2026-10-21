package cli

import (
	"fmt"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// capRegistry holds the capabilities this build advertises through
// `ctx version --json`. Each capability is registered by an init() in the
// file of the WP that implements it (E19); the names are reserved for:
//
//	status.json, status.offline               WP-1.1 (status.go)
//	push.json, push.paths, push.distill_guard WP-1.2 (push.go)
//	pull.json                                 WP-1.3 (pull.go)
//	show.base, show.server, show.sha, resolve WP-1.4 (show.go, resolve.go)
//	repair                                    WP-1.5 (repair.go)
//
// A wave-0 build advertises only errors.envelope.
var capRegistry = map[contract.Capability]bool{}

func init() {
	registerCapability(contract.CapErrorEnvelope)
}

// registerCapability adds c to the build's advertised capabilities. It
// panics only on an unknown name (not in contract.Capabilities or
// contract.OptionalCapabilities) or a duplicate.
func registerCapability(c contract.Capability) { registerInto(capRegistry, c) }

// registerInto is the test seam behind registerCapability.
func registerInto(reg map[contract.Capability]bool, c contract.Capability) {
	if !contract.KnownCapability(c) {
		panic(fmt.Sprintf("cli: unknown capability %q", c))
	}
	if reg[c] {
		panic(fmt.Sprintf("cli: capability %q registered twice", c))
	}
	reg[c] = true
}

// advertisedCapabilities lists the registered capabilities in contract order:
// contract.Capabilities, then contract.OptionalCapabilities. Never nil.
func advertisedCapabilities() []contract.Capability {
	out := []contract.Capability{}
	for _, list := range [][]contract.Capability{contract.Capabilities, contract.OptionalCapabilities} {
		for _, c := range list {
			if capRegistry[c] {
				out = append(out, c)
			}
		}
	}
	return out
}
