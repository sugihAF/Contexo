package mcp

import (
	"fmt"
	"strings"

	"github.com/sugihAF/contexo/internal/diff"
	syncpkg "github.com/sugihAF/contexo/internal/sync"
)

// buildMergeDirective renders a single MERGE_REQUIRED text block that lays
// out everything an agent needs to reconcile a 409 push: ancestor sha, the
// three versions, what each side changed vs the ancestor, conflicting
// sections (modified on both sides), and a numbered list of next steps.
//
// Inputs:
//
//	conflicts: the rejected files, as returned by the push response (carries
//	           ancestor + server-current content).
//	localBySlug: the bytes the agent just tried to push, keyed by path.
func buildMergeDirective(conflicts []syncpkg.Conflict, localByPath map[string][]byte) string {
	var sb strings.Builder
	sb.WriteString("<MERGE_REQUIRED>\n")
	fmt.Fprintf(&sb, "Push rejected: %d file(s) have moved on the server since your edit.\n\n",
		len(conflicts))
	sb.WriteString("Reconcile each file below by writing a merged version that\n")
	sb.WriteString("incorporates BOTH your changes AND the server's changes, then\n")
	sb.WriteString("re-invoke ctx_push. The state has been updated so the re-push\n")
	sb.WriteString("uses the server's current sha as the new parent.\n\n")

	for i, cf := range conflicts {
		yours := localByPath[cf.Path]
		fmt.Fprintf(&sb, "─── FILE %d/%d: %s ───\n", i+1, len(conflicts), cf.Path)
		fmt.Fprintf(&sb, "  ancestor (both diverged from):  %s\n", shortMergeSHA(cf.ExpectedParentSHA))
		fmt.Fprintf(&sb, "  server now:                     %s\n", shortMergeSHA(cf.CurrentSHA))
		fmt.Fprintln(&sb)

		// "You vs ancestor" diff — what your edit changed.
		yourDiff := diff.PageSections(cf.AncestorContent, yours, cf.ExpectedParentSHA, "local")
		writeSidedDiff(&sb, "YOUR changes vs ancestor:", &yourDiff)

		// "Server vs ancestor" diff — what the server's HEAD changed.
		serverDiff := diff.PageSections(cf.AncestorContent, cf.CurrentContent, cf.ExpectedParentSHA, cf.CurrentSHA)
		writeSidedDiff(&sb, "SERVER changes vs ancestor:", &serverDiff)

		// Conflicting sections: heading modified by BOTH sides.
		conflicting := overlappingModifiedSections(&yourDiff, &serverDiff)
		if len(conflicting) > 0 {
			fmt.Fprintln(&sb, "  CONFLICTING regions (both sides changed):")
			for _, h := range conflicting {
				fmt.Fprintf(&sb, "    %s — synthesize both edits into one coherent version\n", h)
			}
			fmt.Fprintln(&sb)
		} else {
			fmt.Fprintln(&sb, "  (No section was modified by both sides — auto-mergeable in principle.)")
			fmt.Fprintln(&sb)
		}

		fmt.Fprintln(&sb, "  --- ANCESTOR VERSION ---")
		writeIndented(&sb, "  ", string(cf.AncestorContent))
		fmt.Fprintln(&sb, "  --- YOUR VERSION (the one you tried to push) ---")
		writeIndented(&sb, "  ", string(yours))
		fmt.Fprintln(&sb, "  --- SERVER VERSION ---")
		writeIndented(&sb, "  ", string(cf.CurrentContent))
		fmt.Fprintln(&sb)
	}

	sb.WriteString("STEPS for the agent:\n")
	sb.WriteString("  1. For each file above: write a merged version via\n")
	sb.WriteString("     ctx_write_page(slug=..., type=..., body=<merged content>)\n")
	sb.WriteString("     The merged page must preserve BOTH sides' intent. For prose\n")
	sb.WriteString("     sections that conflict, synthesize — don't pick one.\n")
	sb.WriteString("  2. Re-invoke ctx_push with the SAME filters. The local sync state\n")
	sb.WriteString("     has been updated to point at the server's current sha for each\n")
	sb.WriteString("     conflicting file, so the next push will not 409 unless someone\n")
	sb.WriteString("     pushed again in the meantime.\n")
	sb.WriteString("</MERGE_REQUIRED>")
	return sb.String()
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

// overlappingModifiedSections returns the headings that were modified on
// BOTH sides of the merge — the regions the agent must reconcile by
// synthesis rather than by picking one.
func overlappingModifiedSections(yours, theirs *diff.SectionDiff) []string {
	mineMod := map[string]bool{}
	for _, s := range yours.Sections {
		if s.Status == diff.StatusModified || s.Status == diff.StatusRenamed {
			mineMod[s.Heading] = true
		}
	}
	var both []string
	for _, s := range theirs.Sections {
		if (s.Status == diff.StatusModified || s.Status == diff.StatusRenamed) && mineMod[s.Heading] {
			both = append(both, s.Heading)
		}
	}
	return both
}

func writeIndented(sb *strings.Builder, prefix, text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		fmt.Fprintf(sb, "%s%s\n", prefix, line)
	}
}

func shortMergeSHA(s string) string {
	if len(s) <= 7 {
		return s
	}
	return s[:7]
}
