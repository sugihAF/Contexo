// Package contract defines the versioned JSON contract between the ctx CLI
// and programmatic callers (the Contexo VS Code extension first). It holds
// ONLY wire types, enums, the error/exit-code table and the pure page-state
// decision function. It imports nothing but the standard library so that
// internal/ui, internal/cli, internal/mcp and the sync engines can all import
// it without cycles.
//
// Import path: github.com/sugihAF/contexo/internal/syncops/contract.
// Source of truth: the wiki contract page (contexo-vscode-mvp-contract) plus
// the plan page's "Contract errata" E1-E41, which override it. Frozen after
// wave 0: changes are additive only (see ../CONTRACT.md).
package contract

import "time"

// ContractVersion is the MAJOR version of this contract. It is bumped only by
// a breaking change (field removed/renamed/retyped, semantics changed, a new
// PageState value). Additive changes advertise a new Capability instead.
const ContractVersion = 1

// Schema identifiers. Every stdout document and the stderr error envelope
// carries one in its "schema" field (mirrors the existing ctx.page.v1 style).
const (
	SchemaVersion = "ctx.version.v1"
	SchemaStatus  = "ctx.status.v1"
	SchemaPush    = "ctx.push.v1"
	SchemaPull    = "ctx.pull.v1"
	SchemaResolve = "ctx.resolve.v1"
	SchemaError   = "ctx.error.v1"
	// SchemaRepair is stdout of `ctx repair --json` (E18). The extension never
	// parses it; it is not part of the TS SCHEMAS list.
	SchemaRepair = "ctx.repair.v1"
)

// Capability is a ctx-side feature advertised by `ctx version --json`.
type Capability string

const (
	CapStatusJSON       Capability = "status.json"
	CapStatusOffline    Capability = "status.offline"
	CapShowBase         Capability = "show.base"
	CapShowServer       Capability = "show.server"
	CapShowSHA          Capability = "show.sha"
	CapPullJSON         Capability = "pull.json" // implies safe pull
	CapPushJSON         Capability = "push.json"
	CapPushPaths        Capability = "push.paths"
	CapPushDistillGuard Capability = "push.distill_guard"
	CapResolve          Capability = "resolve"
	CapErrorEnvelope    Capability = "errors.envelope"
	// CapRepair advertises `ctx repair` (E18). Optional: the extension never
	// requires or calls it.
	CapRepair Capability = "repair"
)

// Capabilities is the contract-v1 capability list, in stable contract order.
// `ctx version --json` advertises the registered subset of it, in this order,
// followed by the registered OptionalCapabilities (E19).
var Capabilities = []Capability{
	CapStatusJSON, CapStatusOffline, CapShowBase, CapShowServer, CapShowSHA,
	CapPullJSON, CapPushJSON, CapPushPaths, CapPushDistillGuard, CapResolve,
	CapErrorEnvelope,
}

// OptionalCapabilities are known capabilities outside the v1 list (E18).
var OptionalCapabilities = []Capability{CapRepair}

// KnownCapability reports whether c is in Capabilities or
// OptionalCapabilities.
func KnownCapability(c Capability) bool {
	for _, k := range Capabilities {
		if k == c {
			return true
		}
	}
	for _, k := range OptionalCapabilities {
		if k == c {
			return true
		}
	}
	return false
}

// ServerCapability is advertised by a Contexo server in the
// X-Contexo-Capabilities response header (comma-separated). Old servers send
// no header, which means "none of these".
type ServerCapability string

const (
	SrvPagesAt ServerCapability = "pages.at" // GET /pages/*path?at=<sha>, echoes X-Contexo-At
	// SrvPagesETag is reserved; v1 never emits it (E13): no server advertises
	// it and ctx never sends If-None-Match.
	SrvPagesETag    ServerCapability = "pages.etag"
	SrvExpectAbsent ServerCapability = "push.expect_absent" // PushFile.expect_absent create-only guard
	SrvWritePathSHA ServerCapability = "write.path_sha"     // unchanged write returns the path's last commit
)

// VersionDoc is stdout of `ctx version --json`.
type VersionDoc struct {
	Schema          string       `json:"schema"`
	ContractVersion int          `json:"contract_version"`
	Version         string       `json:"version"`
	Commit          string       `json:"commit"`
	Date            string       `json:"date"`
	DevBuild        bool         `json:"dev_build"`
	OS              string       `json:"os"`
	Arch            string       `json:"arch"`
	Capabilities    []Capability `json:"capabilities"`
}

// Warning is a non-fatal condition attached to a document.
type Warning struct {
	Code    WarningCode `json:"code"`
	Message string      `json:"message"`
	Paths   []string    `json:"paths"`
}

type WarningCode string

const (
	WarnServerURLMismatch WarningCode = "server_url_mismatch" // config server_url != the URL the credentials belong to
	WarnServerOutdated    WarningCode = "server_outdated"     // server lacks one or more ServerCapability
	// WarnRemoteStale is reserved; v1 never emits it (E8: there is no remote
	// cache in contract v1).
	WarnRemoteStale       WarningCode = "remote_stale"
	WarnBaseRepairSkipped WarningCode = "base_repair_skipped" // unverified pages left (lock busy / server lacks pages.at)
	WarnCaseCollision     WarningCode = "case_collision"      // paths differ only by case; pull skips them
	WarnReindexFailed     WarningCode = "reindex_failed"      // index.md/tags.md regeneration failed after pull
	WarnStateUnreadable   WarningCode = "state_unreadable"    // .sync/state.json or bases.json corrupt; treated as empty
)

// Time is always UTC, truncated to whole seconds, RFC 3339 ("2026-11-03T09:14:07Z").
// Build every contract time through TimeOf or TimePtr (E16): the alias alone
// cannot enforce UTC whole seconds.
type Time = time.Time

// TimeOf returns t as a contract time: UTC, truncated to whole seconds.
func TimeOf(t time.Time) Time { return t.UTC().Truncate(time.Second) }

// TimePtr returns a pointer to TimeOf(t), for optional time fields.
func TimePtr(t time.Time) *Time { v := TimeOf(t); return &v }
