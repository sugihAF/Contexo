// Package syncopstest provides test doubles for the syncops engines.
package syncopstest

import (
	"errors"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// FakeOptions selects server behaviours. The zero value is a current
// (WP-0.2) server.
type FakeOptions struct {
	// OldServer models a server without ?at support or capability headers:
	// ReadPageAt fails with sync.ErrServerUnsupported and ServerInfo reports
	// no capabilities.
	OldServer bool
	// LegacyNoopHeadSHA models the pre-fix gitstore bug: a no-op write
	// returns repo HEAD instead of the path's last commit.
	LegacyNoopHeadSHA bool
	// NoExpectAbsent makes the server ignore PushFile.ExpectAbsent.
	NoExpectAbsent bool
	// ServerVersion is the X-Contexo-Server-Version the fake reports.
	ServerVersion string
}

// FakeRemote is an in-memory, per-path commit model implementing
// syncops.Remote with deterministic 40-hex SHAs.
type FakeRemote struct {
	opts FakeOptions
}

var _ syncops.Remote = (*FakeRemote)(nil)

var errStub = errors.New("syncopstest: not implemented")

// NewFakeRemote returns an empty repo.
func NewFakeRemote(o FakeOptions) *FakeRemote { return &FakeRemote{opts: o} }

// PushPages implements syncops.Remote.
func (f *FakeRemote) PushPages(repoID string, req *sync.PushRequest) (*sync.PushResponse, error) {
	return nil, errStub
}

// PullPages implements syncops.Remote.
func (f *FakeRemote) PullPages(repoID, since string) (*sync.PullResponse, error) {
	return nil, errStub
}

// ListPages implements syncops.Remote.
func (f *FakeRemote) ListPages(repoID string) ([]sync.PageMeta, error) {
	return nil, errStub
}

// ReadPage implements syncops.Remote.
func (f *FakeRemote) ReadPage(repoID, path string) ([]byte, string, error) {
	return nil, "", errStub
}

// ReadPageAt implements syncops.Remote.
func (f *FakeRemote) ReadPageAt(repoID, path, sha string) ([]byte, string, error) {
	return nil, "", errStub
}

// ServerInfo implements syncops.Remote.
func (f *FakeRemote) ServerInfo() (string, []contract.ServerCapability, bool) {
	return "", []contract.ServerCapability{}, false
}

// Seed commits content at path (one commit) and returns the commit sha.
func (f *FakeRemote) Seed(path string, content []byte) string { return "" }

// Head returns the current HEAD sha ("" for an empty repo).
func (f *FakeRemote) Head() string { return "" }

// Content returns the HEAD content of path.
func (f *FakeRemote) Content(path string) ([]byte, bool) { return nil, false }

// Requests returns the number of calls per Remote method name
// ("PushPages", "PullPages", "ListPages", "ReadPage", "ReadPageAt").
func (f *FakeRemote) Requests() map[string]int { return map[string]int{} }

// FailNext makes the next call of method return err (the call still counts).
func (f *FakeRemote) FailNext(method string, err error) {}
