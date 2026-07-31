package ui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// fieldWidth is fixed rather than computed from content, so successive Field
// calls line up without the UI having to buffer them.
const fieldWidth = 11

// UI owns chrome only. Pure data lines keep using fmt.Fprintf -- routing
// everything through here would produce a second, worse fmt.
type UI struct {
	w      io.Writer
	r      *lipgloss.Renderer
	mode   Mode
	glyphs glyphSet
}

// New binds a renderer to THIS writer. The sink stays io.Writer so existing
// render helpers and their test call sites compile unchanged.
func New(w io.Writer, m Mode) *UI {
	return &UI{
		w:      w,
		r:      lipgloss.NewRenderer(w),
		mode:   m,
		glyphs: glyphsFor(unicodeOK()),
	}
}

// From builds a UI for cmd's stdout.
func From(cmd *cobra.Command) *UI {
	w := cmd.OutOrStdout()
	return New(w, DetectMode(cmd, w))
}

// ForStderr builds a UI for cmd's stderr, probing that stream for TTY-ness.
func ForStderr(cmd *cobra.Command) *UI {
	w := cmd.ErrOrStderr()
	return New(w, DetectMode(cmd, w))
}

// Plain builds a UI for w with no cobra context. Used by helpers that only
// receive an io.Writer.
func Plain(w io.Writer) *UI { return New(w, DetectMode(nil, w)) }

func (u *UI) Out() io.Writer { return u.w }
func (u *UI) Mode() Mode     { return u.mode }

// Glyph exposes a symbol so callers can build their own rows in the same
// vocabulary without reaching for a literal.
func (u *UI) Glyph() glyphSet { return u.glyphs }

func (u *UI) style(t tone) lipgloss.Style {
	return u.r.NewStyle().Foreground(t.pick(u.mode.Dark))
}

// Style renders s in the given tone. Exported for callers that compose their
// own lines (table cells, preview rows) but still want brand colour.
func (u *UI) Style(t tone, s string) string {
	if u.mode.Machine {
		return s
	}
	return u.style(t).Render(s)
}

func (u *UI) printf(format string, a ...any) {
	if u.mode.Machine {
		return
	}
	fmt.Fprintf(u.w, format, a...)
}

// Brand renders the wordmark plus a status dot: "contexo  ● connected".
//
// healthy drives the dot. It is a parameter rather than always-accent because
// a green status light beside the words "not authenticated" is exactly the
// misuse the palette rules out: accent means brand or success, nothing else.
func (u *UI) Brand(state string, healthy bool) {
	u.printf("%s  %s %s\n",
		u.style(Accent).Bold(true).Render("contexo"),
		u.Mark(healthy),
		u.style(FG2).Render(state))
}

// Eyebrow renders a section label: "── REMOTE".
func (u *UI) Eyebrow(label string) {
	u.printf("%s %s\n",
		u.style(FG4).Render(u.glyphs.Rule),
		u.style(FG3).Render(label))
}

// Field renders an aligned label/value pair.
func (u *UI) Field(name, value string) {
	u.printf("%s%s\n",
		u.style(FG3).Width(fieldWidth).Render(name),
		u.style(FG).Render(value))
}

func (u *UI) Success(format string, a ...any) {
	u.printf("%s %s\n", u.style(Accent).Render(u.glyphs.OK),
		u.style(FG2).Render(fmt.Sprintf(format, a...)))
}

func (u *UI) Warn(format string, a ...any) {
	u.printf("%s %s\n", u.style(Warm).Render(u.glyphs.Warn),
		u.style(Warm).Render(fmt.Sprintf(format, a...)))
}

func (u *UI) Fail(format string, a ...any) {
	u.printf("%s %s\n", u.style(RemInk).Render(u.glyphs.Fail),
		u.style(RemInk).Render(fmt.Sprintf(format, a...)))
}

// Hint renders an actionable next step: "try  ctx auth login".
func (u *UI) Hint(verb, command string) {
	u.printf("%s  %s\n", u.style(FG3).Render(verb), u.style(FG).Render(command))
}

// Step renders one line of running commentary: dim and indented, no glyph.
//
// It exists so a multi-step command reads as a single column. A ✔ marks a
// RESULT; the individual actions leading to it are log lines. Mixing the two
// weights for semantically identical steps is what makes a wiring log look
// arbitrary.
func (u *UI) Step(format string, a ...any) {
	u.printf("%s\n", u.style(FG3).Render("  "+fmt.Sprintf(format, a...)))
}

// Line writes an unstyled line. Convenience for data rows so callers do not
// need to reach for the writer.
func (u *UI) Line(format string, a ...any) {
	if u.mode.Machine {
		return
	}
	fmt.Fprintf(u.w, format+"\n", a...)
}
