package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// typeDirs maps each --type value to the .contexo/ subdirectory that holds
// pages of that type. Order matters for ambiguity error messages (stable).
var typeDirs = []struct {
	typ string
	dir string
}{
	{"concept", "wiki/concepts"},
	{"entity", "wiki/entities"},
	{"analysis", "wiki/analyses"},
	{"source", "raw/sessions"},
}

// resolveSlugPath finds the .contexo/-relative path for a slug by scanning
// the local repo. If typ is non-empty, only that type's directory is checked.
// Returns the path on a unique match; an error listing all matches when more
// than one type contains a file named <slug>.md; or an error when none do.
//
// Callers pass the project root (typically GetRootDir()); .contexo/ is
// appended internally.
func resolveSlugPath(root, slug, typ string) (string, error) {
	if slug == "" {
		return "", fmt.Errorf("slug is required")
	}
	candidates := []string{}
	for _, td := range typeDirs {
		if typ != "" && td.typ != typ {
			continue
		}
		rel := td.dir + "/" + slug + ".md"
		abs := filepath.Join(root, ".contexo", filepath.FromSlash(rel))
		if _, err := os.Stat(abs); err == nil {
			candidates = append(candidates, rel)
		}
	}
	switch len(candidates) {
	case 0:
		if typ != "" {
			return "", fmt.Errorf("slug %q not found under .contexo/%s/", slug, dirForType(typ))
		}
		return "", fmt.Errorf("slug %q not found in .contexo/ (looked in wiki/{concepts,entities,analyses}/ and raw/sessions/)", slug)
	case 1:
		return candidates[0], nil
	default:
		sort.Strings(candidates)
		return "", fmt.Errorf("slug %q is ambiguous (%d matches: %v); pass --type to disambiguate", slug, len(candidates), candidates)
	}
}

func dirForType(typ string) string {
	for _, td := range typeDirs {
		if td.typ == typ {
			return td.dir
		}
	}
	return typ
}

// validTypes lists the accepted --type values for help text and validation.
func validTypes() []string {
	out := make([]string, len(typeDirs))
	for i, td := range typeDirs {
		out[i] = td.typ
	}
	return out
}
