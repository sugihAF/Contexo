package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	goSync "sync"

	"github.com/sugihAF/contexo/internal/diff"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/ui"
)

// previewConcurrency bounds in-flight server reads during a push preview.
const previewConcurrency = 8

// pushPreview describes one file in a push batch alongside how it would
// change the server-side version (or "new page" if the path isn't there yet).
type pushPreview struct {
	Path     string
	Status   pushStatus
	Diff     *diff.SectionDiff // nil for new pages and read errors
	ReadErr  error             // non-nil if we couldn't reach the server
	LineHint string            // pre-rendered one-line summary for the row
}

type pushStatus int

const (
	pushStatusNew pushStatus = iota
	pushStatusEdit
	pushStatusNoChange
	pushStatusError
)

// computePushPreview previews the batch syncops.Push is about to send. A
// create file has no server version, so it is NEW without a request; every
// update file's current server content is fetched and diffed against the
// to-be-pushed bytes. Failures to read are surfaced as pushStatusError, not
// fatal — the user still sees they tried to push that file (the actual push
// will succeed or 409 as before).
// Reads run concurrently: this used to be one blocking round trip per file,
// so a 40-page push waited on 40 sequential requests. Results are written to
// their own index rather than appended, so the output order stays the plan
// order regardless of which response lands first.
func computePushPreview(client *sync.Client, repoID string, plan []syncops.PushPlanFile) []pushPreview {
	out := make([]pushPreview, len(plan))
	sem := make(chan struct{}, previewConcurrency)
	var wg goSync.WaitGroup

	for i, pf := range plan {
		f := pf.File
		if pf.Kind == contract.WriteCreate {
			item := pushPreview{Path: f.Path, Status: pushStatusNew}
			item.LineHint = renderPreviewLine(item)
			out[i] = item
			continue
		}
		wg.Add(1)
		go func(i int, f sync.PushFile) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			previewItem := pushPreview{Path: f.Path}
			serverBytes, serverSHA, err := client.ReadPage(repoID, f.Path)
			switch {
			case errors.Is(err, sync.ErrPageNotFound):
				previewItem.Status = pushStatusNew
			case err != nil:
				previewItem.Status = pushStatusError
				previewItem.ReadErr = err
			default:
				d := diff.PageSections(serverBytes, []byte(f.Content), serverSHA, "local")
				previewItem.Diff = &d
				if isEmptyDiff(&d) {
					previewItem.Status = pushStatusNoChange
				} else {
					previewItem.Status = pushStatusEdit
				}
			}
			previewItem.LineHint = renderPreviewLine(previewItem)
			out[i] = previewItem
		}(i, f)
	}
	wg.Wait()
	return out
}

func isEmptyDiff(d *diff.SectionDiff) bool {
	if d.ParseFallback {
		return false
	}
	if len(d.Frontmatter.Changed)+len(d.Frontmatter.Added)+len(d.Frontmatter.Removed) > 0 {
		return false
	}
	if d.Preamble != nil && d.Preamble.Status != diff.StatusUnchanged {
		return false
	}
	for _, s := range d.Sections {
		if s.Status != diff.StatusUnchanged {
			return false
		}
	}
	return true
}

func renderPreviewLine(p pushPreview) string {
	switch p.Status {
	case pushStatusNew:
		return fmt.Sprintf("  [NEW]   %s", p.Path)
	case pushStatusEdit:
		summary := summarizeDiff(p.Diff)
		if summary == "" {
			return fmt.Sprintf("  [EDIT]  %s", p.Path)
		}
		return fmt.Sprintf("  [EDIT]  %s  (%s)", p.Path, summary)
	case pushStatusNoChange:
		return fmt.Sprintf("  [SAME]  %s", p.Path)
	case pushStatusError:
		return fmt.Sprintf("  [?]     %s  (preview unavailable: %v)", p.Path, p.ReadErr)
	}
	return "  " + p.Path
}

// summarizeDiff produces a comma-joined per-edit summary like
// "~ Decision, + Refund handling, frontmatter". Capped to keep one-line rows
// readable; full per-section detail is in --show-diff.
func summarizeDiff(d *diff.SectionDiff) string {
	if d == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	if hasFrontmatterChange(d) {
		parts = append(parts, "frontmatter")
	}
	for _, s := range d.Sections {
		switch s.Status {
		case diff.StatusAdded:
			parts = append(parts, "+ "+trimHeading(s.Heading))
		case diff.StatusRemoved:
			parts = append(parts, "- "+trimHeading(s.Heading))
		case diff.StatusModified:
			parts = append(parts, "~ "+trimHeading(s.Heading))
		case diff.StatusRenamed:
			parts = append(parts, "~> "+trimHeading(s.Heading))
		}
	}
	if d.Preamble != nil && d.Preamble.Status != diff.StatusUnchanged {
		parts = append(parts, "preamble")
	}
	const maxParts = 4
	if len(parts) > maxParts {
		extra := len(parts) - maxParts
		parts = append(parts[:maxParts], fmt.Sprintf("+%d more", extra))
	}
	return strings.Join(parts, ", ")
}

func hasFrontmatterChange(d *diff.SectionDiff) bool {
	return len(d.Frontmatter.Changed)+len(d.Frontmatter.Added)+len(d.Frontmatter.Removed) > 0
}

func trimHeading(h string) string {
	return strings.TrimPrefix(strings.TrimSpace(h), "## ")
}

// renderPreview writes the preview block to out and reports whether the batch
// includes any edits to existing pages (the case that warrants confirmation).
func renderPreview(out io.Writer, repoID string, previews []pushPreview, showDiff bool) (hasEdits bool) {
	fmt.Fprintf(out, "About to push %d page(s) to %s:\n", len(previews), repoID)
	for _, p := range previews {
		fmt.Fprintln(out, p.LineHint)
		if showDiff && p.Status == pushStatusEdit && p.Diff != nil {
			text := p.Diff.ToText("")
			for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
				fmt.Fprintln(out, "    "+line)
			}
		}
		if p.Status == pushStatusEdit {
			hasEdits = true
		}
	}
	return hasEdits
}

// confirm reads a y/N response. Defaults to N; EOF declines.
//
// This is a thin wrapper over ui.Prompter, kept so callers retain an
// injectable io.Reader seam. It is the single confirm implementation for the
// CLI -- there used to be five, with four different prompt strings and three
// different EOF behaviours.
func confirm(in io.Reader, out io.Writer, prompt string) (bool, error) {
	return ui.Plain(out).Prompter(in).Confirm(prompt)
}
