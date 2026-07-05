// internal/migrate/detect_agentfiles_test.go
package migrate

import (
	"path/filepath"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
)

func TestAgentFilesDetector(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "# Project rules\n\nUse tabs.")
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# Agents\n\nGuidance.")
	writeFile(t, filepath.Join(root, ".cursor", "rules", "style.mdc"), "# Style\n\nRules.")
	writeFile(t, filepath.Join(root, "notes.md"), "# not an agent file")

	got, err := newAgentFilesDetector().Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, c := range got {
		names[filepath.Base(c.AbsPath)] = true
		if c.SuggestedType != schema.TypeConcept {
			t.Errorf("%s should be concept, got %q", filepath.Base(c.AbsPath), c.SuggestedType)
		}
	}
	for _, want := range []string{"CLAUDE.md", "AGENTS.md", "style.mdc"} {
		if !names[want] {
			t.Errorf("expected to find %s", want)
		}
	}
	if names["notes.md"] {
		t.Errorf("notes.md is not an agent-instruction file")
	}
}
