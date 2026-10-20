package syncops

import (
	"context"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// Owner: WP-1.5. Fields may be added, never renamed or removed.

// RepairOptions configures Repair.
type RepairOptions struct {
	DryRun bool
}

// RepairResult is stdout of `ctx repair --json` (schema ctx.repair.v1). Keys
// are in E18 order; arrays are never null (build it with NewRepairResult).
type RepairResult struct {
	Schema             string                      `json:"schema"`
	DryRun             bool                        `json:"dry_run"`
	ServerCapabilities []contract.ServerCapability `json:"server_capabilities"`
	Backfilled         []RepairBackfilled          `json:"backfilled"`
	Repaired           []RepairedPage              `json:"repaired"`
	Skipped            []RepairSkipped             `json:"skipped"`
	Unverified         []string                    `json:"unverified"`
	Warnings           []contract.Warning          `json:"warnings"`
}

// RepairBackfilled is a path whose missing base was written.
type RepairBackfilled struct {
	Path string `json:"path"`
	SHA  string `json:"sha"`
}

// RepairedPage is a path whose state sha was rewritten.
type RepairedPage struct {
	Path   string `json:"path"`
	OldSHA string `json:"old_sha"`
	NewSHA string `json:"new_sha"`
	Reason string `json:"reason"`
}

// RepairSkipped is a path repair left alone.
type RepairSkipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// NewRepairResult returns an empty result with every array non-nil.
func NewRepairResult(dryRun bool) *RepairResult {
	return &RepairResult{
		Schema:             contract.SchemaRepair,
		DryRun:             dryRun,
		ServerCapabilities: []contract.ServerCapability{},
		Backfilled:         []RepairBackfilled{},
		Repaired:           []RepairedPage{},
		Skipped:            []RepairSkipped{},
		Unverified:         []string{},
		Warnings:           []contract.Warning{},
	}
}

// Repair runs E11/E12 base repair over every stale path.
func Repair(ctx context.Context, env Env, o RepairOptions) (*RepairResult, error) {
	return nil, NotImplemented("repair")
}
