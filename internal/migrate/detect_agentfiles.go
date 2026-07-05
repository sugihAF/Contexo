// internal/migrate/detect_agentfiles.go
package migrate

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sugihAF/contexo/internal/schema"
)

type agentFilesDetector struct{}

func newAgentFilesDetector() Detector { return agentFilesDetector{} }

func (agentFilesDetector) Name() string { return "agentfiles" }

func (d agentFilesDetector) Detect(root string) ([]Candidate, error) {
	var out []Candidate

	for _, name := range agentInstructionFiles {
		abs := filepath.Join(root, name)
		if fileExists(abs) {
			if c, err := mkCandidate(d.Name(), root, abs, schema.TypeConcept); err == nil {
				out = append(out, c)
			}
		}
	}

	rulesDir := filepath.Join(root, ".cursor", "rules")
	if isDir(rulesDir) {
		entries, _ := os.ReadDir(rulesDir)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext != ".md" && ext != ".mdc" {
				continue
			}
			abs := filepath.Join(rulesDir, e.Name())
			if c, err := mkCandidate(d.Name(), root, abs, schema.TypeConcept); err == nil {
				out = append(out, c)
			}
		}
	}
	return out, nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
