package syncops

import (
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// Remote is the server surface the engines use. *sync.Client implements it;
// syncopstest.FakeRemote is the in-memory test double.
type Remote interface {
	PushPages(repoID string, req *sync.PushRequest) (*sync.PushResponse, error)
	PullPages(repoID, since string) (*sync.PullResponse, error)
	ListPages(repoID string) ([]sync.PageMeta, error)
	ReadPage(repoID, path string) ([]byte, string, error)
	ReadPageAt(repoID, path, sha string) ([]byte, string, error)
	ServerInfo() (string, []contract.ServerCapability, bool)
}

var _ Remote = (*sync.Client)(nil)

// Author is the commit identity pushes are attributed to.
type Author struct {
	Name, Email string
}
