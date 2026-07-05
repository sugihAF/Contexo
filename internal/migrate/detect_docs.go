// internal/migrate/detect_docs.go
package migrate

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sugihAF/contexo/internal/schema"
)

type docsDetector struct{}

func newDocsDetector() Detector { return docsDetector{} }

func (docsDetector) Name() string { return "docs" }

func (d docsDetector) Detect(root string) ([]Candidate, error) {
	var out []Candidate

	// Root-level *.md (README, CHANGELOG, stray docs).
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		if isAgentInstructionFile(e.Name()) {
			continue // owned by the agentfiles detector
		}
		abs := filepath.Join(root, e.Name())
		c, err := mkCandidate(d.Name(), root, abs, suggestDocType(abs))
		if err == nil {
			out = append(out, c)
		}
	}

	// docs/ and documentation/ trees.
	for _, sub := range []string{"docs", "documentation"} {
		dir := filepath.Join(root, sub)
		if !isDir(dir) {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if de.IsDir() {
				if ignoredDir(de.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.EqualFold(filepath.Ext(path), ".md") {
				return nil
			}
			c, err := mkCandidate(d.Name(), root, path, suggestDocType(path))
			if err == nil {
				out = append(out, c)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// suggestDocType maps a doc path to its best-guess Contexo type.
func suggestDocType(absPath string) schema.PageType {
	base := strings.ToLower(filepath.Base(absPath))
	lowPath := strings.ToLower(filepath.ToSlash(absPath))
	switch {
	case strings.HasPrefix(base, "readme"):
		return schema.TypeEntity
	case strings.HasPrefix(base, "changelog"):
		return schema.TypeSource
	case strings.Contains(lowPath, "/adr/") || strings.Contains(lowPath, "/decisions/"):
		return schema.TypeAnalysis
	default:
		return schema.TypeConcept
	}
}
