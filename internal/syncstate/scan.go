package syncstate

import (
	"fmt"
	"os"
	"sort"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
)

// Invalid stages (contract InvalidStage literals).
const (
	StageParse        = "parse"
	StageValidate     = "validate"
	StagePathMismatch = "path_mismatch"
)

// InvalidInfo says why a working file is invalid. ExpectedPath is set only
// for path_mismatch (the frontmatter RelPath()).
type InvalidInfo struct {
	Stage        string
	Message      string
	ExpectedPath string
}

// LocalFile is one working page file. Hash is Hash(Data), or "" when the
// file is invalid; Page is nil when it did not parse.
type LocalFile struct {
	Path    string
	Data    []byte
	Page    *schema.Page
	Hash    string
	Invalid *InvalidInfo
}

// LocalScan is every working page file under the hub.
type LocalScan struct {
	Files map[string]*LocalFile
	Paths []string // sorted in byte order
}

// ScanLocal walks the hub with pagestore.WalkAll (*.md, no dot-dirs, no root
// index.md/tags.md) and classifies each file: parse when ParsePage of the
// BOM-stripped bytes fails, validate when Frontmatter.Validate() fails,
// path_mismatch when Frontmatter.RelPath() differs from the file path,
// otherwise valid with Hash set.
func ScanLocal(hub string) (*LocalScan, error) {
	info, err := os.Stat(hub)
	if err != nil {
		return nil, fmt.Errorf("syncstate: scan %s: %w", hub, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("syncstate: scan %s: not a directory", hub)
	}
	scan := &LocalScan{Files: map[string]*LocalFile{}, Paths: []string{}}
	err = (&pagestore.Store{Root: hub}).WalkAll(func(f pagestore.WalkedFile) error {
		lf := &LocalFile{Path: f.Rel, Data: f.Data, Page: f.Page}
		lf.Invalid = classifyLocal(f)
		if lf.Invalid == nil {
			lf.Hash = Hash(f.Data)
		}
		scan.Files[f.Rel] = lf
		scan.Paths = append(scan.Paths, f.Rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("syncstate: scan %s: %w", hub, err)
	}
	sort.Strings(scan.Paths)
	return scan, nil
}

// classifyLocal returns why a walked file is invalid, or nil when it is a
// valid page at its own path. The stages are checked in contract order.
func classifyLocal(f pagestore.WalkedFile) *InvalidInfo {
	if f.ParseErr != nil {
		return &InvalidInfo{Stage: StageParse, Message: f.ParseErr.Error()}
	}
	fm := &f.Page.Frontmatter
	if err := fm.Validate(); err != nil {
		return &InvalidInfo{Stage: StageValidate, Message: err.Error()}
	}
	if want := fm.RelPath(); want != f.Rel {
		return &InvalidInfo{
			Stage:        StagePathMismatch,
			Message:      "frontmatter type and slug place this page at " + want,
			ExpectedPath: want,
		}
	}
	return nil
}
