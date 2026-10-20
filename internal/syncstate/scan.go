package syncstate

import "github.com/sugihAF/contexo/internal/schema"

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
	return nil, errNotImplemented
}
