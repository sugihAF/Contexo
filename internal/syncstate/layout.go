// Package syncstate is the shared local-state layer of the three-way sync
// (decision D2): the on-disk layout under <hub>/.sync, atomic index stores
// for base snapshots and conflict sidecars, the cross-process lock, the one
// comparison rule (Normalize/Hash/Equal), page-path rules, the local scan
// and the per-path three-sided View. The CLI and MCP sync engines both build
// on it, so humans and agents update base, state and conflicts the same way.
//
// # On-disk layout
//
// Everything lives under <root>/.contexo/.sync/ and is written only by ctx.
// This file is the source of truth for the paths:
//
//	.gitignore           "*\n", created before the first write (E41)
//	state.json           internal/sync.State, format unchanged; page_shas[P] is the BASE sha
//	bases.json           {"schema":"ctx.bases.v1","pages":{P:{sha,hash,synced_at}}}
//	base/<P>             base bytes, stored verbatim
//	conflicts.json       {"schema":"ctx.conflicts.v1","conflicts":{P:{server_sha,base_sha?,kind,source,detected_at,has_content}}}
//	conflicts/<P>        sanitized server bytes, present iff has_content
//	lock                 {"pid","host","command","started_at"}, created O_CREATE|O_EXCL
//	lock.guard           transient O_CREATE|O_EXCL guard held (well under 1 s) while
//	                     removing lock (stale reclaim or unlock); cleared when older than 5 s
//	backup/<P>.<YYYYMMDDTHHMMSSZ>  the working file saved before resolve --theirs overwrites it
//
// The remote listing cache (remote.json) is RESERVED in contract v1 and is
// never written or read (E8).
//
// # Write order
//
// Each operation writes, in this order: working file(s), base file(s),
// conflicts/<P> files, bases.json, conflicts.json, state.json. Each index
// file (bases.json, conflicts.json, state.json) is written at most once per
// operation, every write goes through fsutil.WriteFileAtomic, and EnsureSyncDir
// runs before the first write under .sync. push, pull, resolve and repair
// write only while holding Lock(hub, command, DefaultLockWait()). status
// never waits on the lock: --offline never locks and writes nothing, and
// online status writes only through EnsureBase under TryLock (E5). A crash
// between two steps leaves a base that is stale or unverified, which the
// next online EnsureBase repairs; it never leaves a torn file.
package syncstate

import (
	"path/filepath"
	"time"

	"github.com/sugihAF/contexo/internal/fsutil"
)

// Index schema identifiers.
const (
	SchemaBases     = "ctx.bases.v1"
	SchemaConflicts = "ctx.conflicts.v1"
)

// backupTimeLayout is the UTC suffix of a backup file name.
const backupTimeLayout = "20060102T150405Z"

// SyncDir returns <hub>/.sync.
func SyncDir(hub string) string {
	return filepath.Join(hub, ".sync")
}

// EnsureSyncDir creates <hub>/.sync and its self-ignoring .gitignore. Every
// writer calls it before its first write under .sync.
func EnsureSyncDir(hub string) error {
	return fsutil.EnsureSelfIgnoringDir(SyncDir(hub))
}

// BasesPath returns <hub>/.sync/bases.json.
func BasesPath(hub string) string {
	return filepath.Join(SyncDir(hub), "bases.json")
}

// BaseFile returns <hub>/.sync/base/<rel>, rel being a '/'-separated page path.
func BaseFile(hub, rel string) string {
	return filepath.Join(SyncDir(hub), "base", filepath.FromSlash(rel))
}

// ConflictsPath returns <hub>/.sync/conflicts.json.
func ConflictsPath(hub string) string {
	return filepath.Join(SyncDir(hub), "conflicts.json")
}

// ConflictFile returns <hub>/.sync/conflicts/<rel>.
func ConflictFile(hub, rel string) string {
	return filepath.Join(SyncDir(hub), "conflicts", filepath.FromSlash(rel))
}

// LockPath returns <hub>/.sync/lock.
func LockPath(hub string) string {
	return filepath.Join(SyncDir(hub), "lock")
}

// BackupFile returns <hub>/.sync/backup/<rel>.<YYYYMMDDTHHMMSSZ> for t in UTC.
func BackupFile(hub, rel string, t time.Time) string {
	return filepath.Join(hub, filepath.FromSlash(BackupRel(rel, t)))
}

// BackupRel returns the BackupFile path relative to the hub, '/'-separated
// (".sync/backup/<rel>.<YYYYMMDDTHHMMSSZ>"), as ResolveDoc.backup_file shows it.
func BackupRel(rel string, t time.Time) string {
	return ".sync/backup/" + rel + "." + t.UTC().Format(backupTimeLayout)
}
