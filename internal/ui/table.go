package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// colGap is the space between columns, matching the padding the hand-rolled
// tabwriter tables used before (tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)).
const colGap = 2

// Table renders aligned columns.
//
// Widths are measured with lipgloss.Width, which counts DISPLAY width and
// ignores ANSI escape sequences. That is the whole point: a plain
// text/tabwriter counts escape bytes toward the column width, so the moment
// any cell is styled the columns drift -- and the drift is invisible to tests,
// which see the Ascii profile where the escapes are absent, while being
// obvious to a user on a colour terminal. Measuring display width lets callers
// pass pre-toned cells (via UI.Style or UI.Mark) and still get alignment.
type Table struct {
	u       *UI
	headers []string
	rows    [][]string
}

func (u *UI) Table(headers ...string) *Table {
	return &Table{u: u, headers: headers}
}

// Row appends a row. Cell count need not match the header count; short rows
// simply end early.
func (t *Table) Row(cells ...string) *Table {
	t.rows = append(t.rows, cells)
	return t
}

// Render writes the table. No-op in machine mode.
func (t *Table) Render() {
	if t.u.mode.Machine {
		return
	}

	all := make([][]string, 0, len(t.rows)+1)
	if len(t.headers) > 0 {
		styled := make([]string, len(t.headers))
		for i, h := range t.headers {
			styled[i] = t.u.Style(FG3, h)
		}
		all = append(all, styled)
	}
	all = append(all, t.rows...)

	widths := columnWidths(all)
	for _, r := range all {
		var b strings.Builder
		b.WriteString("  ")
		for i, c := range r {
			b.WriteString(c)
			if i < len(r)-1 {
				pad := widths[i] - lipgloss.Width(c) + colGap
				if pad < 1 {
					pad = 1
				}
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
		// Trailing padding would show up as invisible whitespace in captured
		// docs output and in tests; drop it.
		fmt.Fprintln(t.u.w, strings.TrimRight(b.String(), " "))
	}
}

// columnWidths returns the display width of the widest cell per column.
func columnWidths(rows [][]string) []int {
	var widths []int
	for _, r := range rows {
		for i, c := range r {
			w := lipgloss.Width(c)
			switch {
			case i >= len(widths):
				widths = append(widths, w)
			case w > widths[i]:
				widths[i] = w
			}
		}
	}
	return widths
}

// Mark returns a status dot: accent when on, dim when off.
//
// "Off" is deliberately FG4, not Warm. An unwired integration is a neutral
// fact, not a warning -- on a fresh project every row would otherwise light up
// as a caution. It matters more than it looks: on a 16-colour terminal Warm
// (#f0995b) and RemInk (#ba2b2e) both degrade to ANSI 91, so a "not wired" dot
// would render in the same red as an irreversible DELETE.
func (u *UI) Mark(on bool) string {
	if on {
		return u.Style(Accent, u.glyphs.DotOn)
	}
	return u.Style(FG4, u.glyphs.DotOff)
}
