// Package syncops holds the sync engines shared by `ctx ... --json` and the
// MCP tools: status, push (with the D4 distill guard), safe pull, show,
// resolve, and base repair (EnsureBase / ctx repair). They are free functions
// over Env and sit on internal/syncstate and internal/fsutil (E3).
//
// The wire contract lives in the leaf package syncops/contract; see
// CONTRACT.md for the normative summary and the precedence rule.
//
// Frozen after wave 0 (additive only): doc.go, types.go, env.go, errors.go,
// overlay.go and the contract package. Each engine has one stub file plus one
// stub test that its wave-1 owner inherits:
//
//	status.go                  WP-1.1
//	push.go, guard.go          WP-1.2
//	pull.go                    WP-1.3
//	show.go, resolve.go        WP-1.4 (also internal/cli show.go, resolve.go)
//	repair.go, ensurebase.go   WP-1.5 (also internal/cli repair.go)
//
// syncops never imports internal/cli or internal/mcp: both call the engines
// (internal/cli from wave 0, internal/mcp from WP-2.1), so the import would
// be a cycle. Wave 0 also keeps syncops off internal/config and
// internal/syncstate (parallel WPs write them) and off internal/ui; the
// wave-1 engines may add all three (E3; WP-1.1's status.go takes a
// *config.Remote and calls ui.ToContractError).
package syncops
