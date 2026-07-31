package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestTonePicksByBackground(t *testing.T) {
	if got := Accent.hex(true); got != "#a1ea5a" {
		t.Errorf("Accent dark = %q, want #a1ea5a", got)
	}
	if got := Accent.hex(false); got != "#437a00" {
		t.Errorf("Accent light = %q, want #437a00", got)
	}
}

// The brand accent must survive a truecolor render verbatim. This is the
// canary that we never regress to a 256-colour approximation.
func TestAccentRendersTrueColor(t *testing.T) {
	var sb strings.Builder
	r := lipgloss.NewRenderer(&sb)
	r.SetColorProfile(termenv.TrueColor)
	out := r.NewStyle().Foreground(Accent.pick(true)).Render("x")
	if !strings.Contains(out, "161;234;89") {
		t.Errorf("accent render = %q, want rgb(161,234,89)", out)
	}
}

func TestGlyphsFallBackToASCII(t *testing.T) {
	u := glyphsFor(true)
	a := glyphsFor(false)
	if u.OK != "✔" || u.Warn != "▲" || u.Fail != "✖" {
		t.Errorf("unicode glyphs wrong: %+v", u)
	}
	if a.OK != "v" || a.Warn != "!" || a.Fail != "x" {
		t.Errorf("ascii glyphs wrong: %+v", a)
	}
	for _, g := range []string{a.OK, a.Warn, a.Fail, a.DotOn, a.DotOff, a.Cursor, a.Rule} {
		for _, r := range g {
			if r > 127 {
				t.Errorf("ascii glyph %q contains non-ASCII rune %q", g, r)
			}
		}
	}
}

func TestUnicodeOKRespectsEnvironment(t *testing.T) {
	t.Setenv("CTX_ASCII", "1")
	if unicodeOK() {
		t.Error("CTX_ASCII=1 must force ASCII glyphs")
	}
}
