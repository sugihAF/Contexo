// Package ui owns every piece of chrome the ctx CLI prints: colour, layout,
// live progress, and interactive prompts.
//
// Colours are the Contexo brand tokens from
// contexo-landing/packages/brand/tokens.css, converted oklch->hex. Light
// variants are the same hue at lowered lightness.
//
// IMPORTANT: this package never queries the terminal. lipgloss.AdaptiveColor
// and Renderer.HasDarkBackground() resolve by issuing an OSC 11 query, which
// writes an escape to the output AND consumes the reply from stdin. `ctx mcp`
// owns both streams for JSON-RPC, so a query there corrupts the protocol in
// both directions. Background is an explicit bool instead (see Mode.Dark).
package ui

import (
	"os"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// tone is a colour with a variant per terminal background. It is the
// query-free replacement for lipgloss.AdaptiveColor.
type tone struct {
	Light string
	Dark  string
}

func (t tone) hex(dark bool) string {
	if dark {
		return t.Dark
	}
	return t.Light
}

func (t tone) pick(dark bool) lipgloss.TerminalColor {
	return lipgloss.Color(t.hex(dark))
}

// Brand tokens. Keep in sync with contexo-landing/packages/brand/tokens.css.
var (
	FG     = tone{Light: "#1c1a18", Dark: "#f4efe7"} // --fg      cream
	FG2    = tone{Light: "#3c3a34", Dark: "#bbb7af"} // --fg-2    body
	FG3    = tone{Light: "#58554f", Dark: "#83807a"} // --fg-3    labels
	FG4    = tone{Light: "#83807a", Dark: "#5a5853"} // --fg-4    rules, SHAs
	Accent = tone{Light: "#437a00", Dark: "#a1ea5a"} // --accent  acid green
	Warm   = tone{Light: "#a05000", Dark: "#f0995b"} // --warm    warnings
	AddInk = tone{Light: "#297d22", Dark: "#92f388"} // --diff-add-ink
	RemInk = tone{Light: "#ba2b2e", Dark: "#ff9b92"} // --diff-rem-ink
)

// glyphSet is the symbol vocabulary. The ASCII set is the floor for consoles
// without a UTF-8 capable font -- legacy Windows conhost under a non-UTF-8
// codepage renders the unicode set as boxes.
type glyphSet struct {
	OK     string
	Warn   string
	Fail   string
	DotOn  string
	DotOff string
	Cursor string
	Rule   string
}

func glyphsFor(unicode bool) glyphSet {
	if unicode {
		return glyphSet{OK: "✔", Warn: "▲", Fail: "✖", DotOn: "●", DotOff: "○", Cursor: "▸", Rule: "──"}
	}
	return glyphSet{OK: "v", Warn: "!", Fail: "x", DotOn: "*", DotOff: "-", Cursor: ">", Rule: "--"}
}

// unicodeOK reports whether the console can render the unicode glyph set.
// CTX_ASCII=1 forces the ASCII floor. On Windows we require WT_SESSION
// (Windows Terminal) since legacy conhost is unreliable; elsewhere we take a
// UTF-8 locale as sufficient evidence.
func unicodeOK() bool {
	if os.Getenv("CTX_ASCII") != "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return os.Getenv("WT_SESSION") != ""
	}
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			up := strings.ToUpper(v)
			return strings.Contains(up, "UTF-8") || strings.Contains(up, "UTF8")
		}
	}
	return true
}
