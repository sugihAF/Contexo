package migrate

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sugihAF/contexo/internal/schema"
)

type kbTreeDetector struct{}

func newKBTreeDetector() Detector { return kbTreeDetector{} }

func (kbTreeDetector) Name() string { return "kbtree" }

func (d kbTreeDetector) Detect(root string) ([]Candidate, error) {
	if isDir(filepath.Join(root, "wiki")) {
		return d.detectWiki(root)
	}
	if isDir(filepath.Join(root, ".obsidian")) {
		return d.detectObsidian(root)
	}
	return nil, nil
}

// detectWiki maps the llm-wiki layout: wiki/{concepts,entities,analyses} and
// raw/ pages onto their Contexo types.
func (d kbTreeDetector) detectWiki(root string) ([]Candidate, error) {
	subs := []struct {
		rel string
		typ schema.PageType
	}{
		{filepath.Join("wiki", "concepts"), schema.TypeConcept},
		{filepath.Join("wiki", "entities"), schema.TypeEntity},
		{filepath.Join("wiki", "analyses"), schema.TypeAnalysis},
		{"raw", schema.TypeSource},
	}
	var out []Candidate
	for _, s := range subs {
		dir := filepath.Join(root, s.rel)
		if !isDir(dir) {
			continue
		}
		cs, err := d.collectMarkdown(root, dir, s.typ)
		if err != nil {
			return nil, err
		}
		out = append(out, cs...)
	}
	return out, nil
}

func (d kbTreeDetector) detectObsidian(root string) ([]Candidate, error) {
	return d.collectMarkdown(root, root, schema.TypeConcept)
}

// collectMarkdown walks dir for *.md files, skipping noise directories, and
// builds a candidate of typ for each.
func (d kbTreeDetector) collectMarkdown(sourceRoot, dir string, typ schema.PageType) ([]Candidate, error) {
	var out []Candidate
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if e.IsDir() {
			if ignoredDir(e.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		c, err := mkCandidate(d.Name(), sourceRoot, path, typ)
		if err != nil {
			return nil
		}
		out = append(out, c)
		return nil
	})
	return out, err
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// ignoredDir reports whether a directory name should never be walked.
func ignoredDir(name string) bool {
	switch name {
	case ".git", ".contexo", "node_modules", "vendor", ".obsidian",
		"dist", "build", ".next", ".migrate-cache":
		return true
	}
	return false
}
