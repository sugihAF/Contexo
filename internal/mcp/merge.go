package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/sugihAF/contexo/internal/diff"
)

// mergeItem is one page of a MERGE_REQUIRED brief: the three versions an
// agent needs to reconcile it. Ancestor is the base snapshot (syncops.Show
// At "base"), empty when there is none; Yours is the working file; Server
// is the sanitized server version at ServerSHA (the conflict sidecar's
// content, else syncops.Show At ServerSHA).
type mergeItem struct {
	Path, AncestorSHA string
	Ancestor          []byte
	Yours             []byte
	ServerSHA         string
	Server            []byte
}

// briefSHAs records, per project root and page path, the server sha of the
// last MERGE_REQUIRED this process showed the agent with the server
// version's content. ctx_push resolved=[...] re-bases only onto a sha the
// agent was shown, so this is the first place it looks.
var briefSHAs = struct {
	mu sync.Mutex
	m  map[string]string
}{m: map[string]string{}}

func briefKey(root, path string) string { return root + "\x00" + path }

// briefRecord remembers that the agent was shown path at sha.
func briefRecord(root, path, sha string) {
	briefSHAs.mu.Lock()
	defer briefSHAs.mu.Unlock()
	briefSHAs.m[briefKey(root, path)] = sha
}

// briefLookup returns the server sha of the last brief shown for path.
func briefLookup(root, path string) (string, bool) {
	briefSHAs.mu.Lock()
	defer briefSHAs.mu.Unlock()
	sha, ok := briefSHAs.m[briefKey(root, path)]
	return sha, ok
}

// briefForget drops path once it was resolved.
func briefForget(root, path string) {
	briefSHAs.mu.Lock()
	defer briefSHAs.mu.Unlock()
	delete(briefSHAs.m, briefKey(root, path))
}

// buildMergeBrief renders one MERGE_REQUIRED block for the pages ctx_push
// could not send or ctx_pull kept (origin "push" or "pull"): per page the
// ancestor and server shas, what each side changed, the sections both
// changed and the three full versions, then the steps that end in
// ctx_push(resolved=[...]). The local sync state is never touched for a
// conflict, so the brief never claims it was.
func buildMergeBrief(origin string, items []mergeItem) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<MERGE_REQUIRED origin=%s>\n", origin)
	if origin == "pull" {
		fmt.Fprintf(&sb, "%d page(s) changed both locally and on the server since your last sync. "+
			"Your local version was kept; nothing was overwritten.\n", len(items))
	} else {
		fmt.Fprintf(&sb, "%d page(s) changed both locally and on the server since your last sync. "+
			"They were not pushed; the server keeps the other version.\n", len(items))
	}
	sb.WriteString("Local sync state was NOT changed; the page stays a conflict until you resolve it.\n\n")

	paths := make([]string, len(items))
	for i, it := range items {
		paths[i] = it.Path
		fmt.Fprintf(&sb, "─── FILE %d/%d: %s ───\n", i+1, len(items), it.Path)
		// Both shas are printed in full, like every sha in the ctx_push and
		// ctx_pull text (WP-4.1 GFR-5).
		ancestor := "(none)"
		if it.AncestorSHA != "" {
			ancestor = it.AncestorSHA
		}
		fmt.Fprintf(&sb, "  ancestor (both diverged from):  %s\n", ancestor)
		fmt.Fprintf(&sb, "  server now:                     %s\n", it.ServerSHA)
		fmt.Fprintln(&sb)

		yourDiff := diff.PageSections(it.Ancestor, it.Yours, it.AncestorSHA, "local")
		writeSidedDiff(&sb, "YOUR changes vs ancestor:", &yourDiff)
		serverDiff := diff.PageSections(it.Ancestor, it.Server, it.AncestorSHA, it.ServerSHA)
		writeSidedDiff(&sb, "SERVER changes vs ancestor:", &serverDiff)

		conflicting, same := overlappingModifiedSections(&yourDiff, &serverDiff)
		switch {
		case len(conflicting) > 0:
			fmt.Fprintln(&sb, "  CONFLICTING regions (both sides changed):")
			for _, h := range conflicting {
				fmt.Fprintf(&sb, "    %s — synthesize both edits into one coherent version\n", h)
			}
		case len(same) > 0:
			fmt.Fprintln(&sb, "  (No section has conflicting edits — auto-mergeable in principle.)")
		default:
			fmt.Fprintln(&sb, "  (No section was modified by both sides — auto-mergeable in principle.)")
		}
		if len(same) > 0 {
			fmt.Fprintln(&sb, "  Same change on both sides (keep it once):")
			for _, h := range same {
				fmt.Fprintf(&sb, "    %s\n", h)
			}
		}
		fmt.Fprintln(&sb)

		// The versions are printed unindented, so "verbatim" means verbatim.
		writeVersion(&sb, "--- ANCESTOR VERSION ---", it.Ancestor,
			"(not available: no common base is stored for this page; compare YOUR and SERVER directly)")
		writeVersion(&sb, "--- YOUR VERSION (your local file) ---", it.Yours,
			"(the local file is missing)")
		writeVersion(&sb, "--- SERVER VERSION ---", it.Server,
			"(could not be fetched; call ctx_push again to get a fresh brief)")
		fmt.Fprintf(&sb, "--- END OF FILE %d/%d ---\n\n", i+1, len(items))
	}

	resolved, _ := json.Marshal(paths)
	sb.WriteString("STEPS:\n")
	sb.WriteString("  1. For each file above, call ctx_write_page (same slug and type) with merged content that\n")
	sb.WriteString("     keeps BOTH sides' changes. Where both sides changed the same section, synthesize one\n")
	sb.WriteString("     coherent version; don't just pick one.\n")
	sb.WriteString("     To accept the server version instead, write the SERVER VERSION verbatim.\n")
	fmt.Fprintf(&sb, "  2. Call ctx_push with the same filters plus resolved=%s.\n", resolved)
	sb.WriteString("     ctx_push keeps your merged content, re-bases it on the server version shown above and\n")
	sb.WriteString("     pushes it. If the server moved again meanwhile, you get a fresh MERGE_REQUIRED instead\n")
	sb.WriteString("     and nothing is overwritten.\n")
	sb.WriteString("</MERGE_REQUIRED>")
	return sb.String()
}

// writeVersion prints one full version between its marker line and the
// next, or missing when the version is empty.
func writeVersion(sb *strings.Builder, marker string, content []byte, missing string) {
	sb.WriteString(marker + "\n")
	if len(content) == 0 {
		sb.WriteString(missing + "\n")
		return
	}
	sb.Write(content)
	if content[len(content)-1] != '\n' {
		sb.WriteByte('\n')
	}
}

// writeSidedDiff prints a heading and a per-section change list for one side
// of a 3-way merge. Compact — full bodies live in the three-version dump
// below. Sections that are unchanged on this side are omitted.
func writeSidedDiff(sb *strings.Builder, label string, d *diff.SectionDiff) {
	fmt.Fprintf(sb, "  %s\n", label)
	any := false
	if hasFrontmatterDiff(d) {
		fmt.Fprintln(sb, "    ~ frontmatter changed")
		any = true
	}
	for _, s := range d.Sections {
		switch s.Status {
		case diff.StatusAdded:
			fmt.Fprintf(sb, "    + %s\n", s.Heading)
			any = true
		case diff.StatusRemoved:
			fmt.Fprintf(sb, "    - %s\n", s.Heading)
			any = true
		case diff.StatusModified:
			fmt.Fprintf(sb, "    ~ %s\n", s.Heading)
			any = true
		case diff.StatusRenamed:
			fmt.Fprintf(sb, "    ~> %s (was %q)\n", s.Heading, s.OldHeading)
			any = true
		}
	}
	if !any {
		fmt.Fprintln(sb, "    (no structured changes — content-level edits only)")
	}
	fmt.Fprintln(sb)
}

// overlappingModifiedSections splits the headings modified on BOTH sides
// of the merge: conflicting are the regions the two sides changed
// differently, which the agent must reconcile by synthesis rather than by
// picking one; same are the ones both sides changed to the same text
// (WP-4.1 GFR-6), which need no merge.
func overlappingModifiedSections(yours, theirs *diff.SectionDiff) (conflicting, same []string) {
	mine := map[string]string{}
	for _, s := range yours.Sections {
		if s.Status == diff.StatusModified || s.Status == diff.StatusRenamed {
			mine[s.Heading] = s.To
		}
	}
	for _, s := range theirs.Sections {
		if s.Status != diff.StatusModified && s.Status != diff.StatusRenamed {
			continue
		}
		to, ok := mine[s.Heading]
		switch {
		case !ok:
		case strings.TrimSpace(to) == strings.TrimSpace(s.To):
			same = append(same, s.Heading)
		default:
			conflicting = append(conflicting, s.Heading)
		}
	}
	return conflicting, same
}
