package syncops

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// hubDirName is the project's knowledge-base directory under the root. It
// duplicates config.ContexoDir on purpose: syncops never imports config.
const hubDirName = ".contexo"

// Env is everything an engine needs about one project and its server. The
// client name (X-Contexo-Client) is a sync.Client property, never an Env
// field (E15).
type Env struct {
	Root, HubDir, RepoID, ServerURL string
	Author                          Author
	Remote                          Remote
	Now                             func() time.Time
}

// NewEnv builds an Env for root. A typed-nil *sync.Client becomes a nil
// Remote, so Online reports the truth; Now defaults to time.Now.
func NewEnv(root, repoID, serverURL string, a Author, r Remote) Env {
	if c, ok := r.(*sync.Client); ok && c == nil {
		r = nil
	}
	return Env{
		Root:      root,
		HubDir:    filepath.Join(root, hubDirName),
		RepoID:    repoID,
		ServerURL: serverURL,
		Author:    a,
		Remote:    r,
		Now:       time.Now,
	}
}

// Online reports whether the engine may talk to a server.
func (e Env) Online() bool { return e.Remote != nil }

// RequireHub returns <root>/.contexo, or not_initialized (advice "ctx init")
// when it is missing or not a directory.
func RequireHub(root string) (string, error) {
	hub := filepath.Join(root, hubDirName)
	fi, err := os.Stat(hub)
	if err != nil || !fi.IsDir() {
		e := contract.New(contract.CodeNotInitialized,
			fmt.Sprintf("no .contexo/ directory in %s", root), "ctx init")
		e.Err = err
		return "", e
	}
	return hub, nil
}
