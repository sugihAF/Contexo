package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

func TestTableAlignsColumns(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{Dark: true})
	u.Table("AGENT", "MCP").
		Row("Claude Code", "wired").
		Row("Cursor", "not wired").
		Render()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), buf.String())
	}
	if strings.Index(lines[1], "wired") != strings.Index(lines[2], "not wired") {
		t.Errorf("columns misaligned:\n%q\n%q", lines[1], lines[2])
	}
}

// stripANSI removes SGR sequences so a styled line can be measured as a user
// sees it.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// REGRESSION: the header row is styled while body rows may not be. A width
// calculator that counts raw bytes (text/tabwriter does) counts the header's
// escape bytes toward its column width, so the header drifts left of the body.
// Tests using a plain buffer never see it, because the Ascii profile emits no
// escapes at all -- it only breaks for real users on a colour terminal.
func TestTableHeaderAlignsWithBodyWhenStyled(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{Dark: true, TTY: true})
	u.r.SetColorProfile(termenv.TrueColor)
	u.Table("AGENT", "MCP SERVER").
		Row("Claude Code", "wired").
		Row("Cursor", "not wired").
		Render()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "\x1b[") {
		t.Fatal("header is not styled; this test would be vacuous")
	}
	want := cellStarts(lines[0])
	for _, l := range lines[1:] {
		got := cellStarts(l)
		if len(got) != len(want) || got[1] != want[1] {
			t.Errorf("column starts %v in body vs %v in header:\nH %q\nB %q",
				got, want, stripANSI(lines[0]), stripANSI(l))
		}
	}
}

// cellStarts returns the start offset of each column on a rendered line.
// Columns are separated by two or more spaces, so this cannot be fooled by a
// cell value that is a substring of another ("wired" inside "not wired").
func cellStarts(line string) []int {
	s := stripANSI(line)
	var starts []int
	i := 0
	for i < len(s) {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) {
			break
		}
		starts = append(starts, i)
		// Advance to the next run of 2+ spaces.
		for i < len(s) && !(s[i] == ' ' && i+1 < len(s) && s[i+1] == ' ') {
			i++
		}
	}
	return starts
}

// Alignment must also survive a caller pre-toning a body cell.
func TestTableAlignsPreStyledCells(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{Dark: true, TTY: true})
	u.r.SetColorProfile(termenv.TrueColor)
	u.Table("A", "B").
		Row(u.Style(Accent, "yes"), "first").
		Row("no", "second").
		Render()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if strings.Index(stripANSI(lines[1]), "first") != strings.Index(stripANSI(lines[2]), "second") {
		t.Errorf("pre-styled cell broke alignment:\n%q\n%q",
			stripANSI(lines[1]), stripANSI(lines[2]))
	}
}

func TestTableMachineModeWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{Machine: true})
	u.Table("A").Row("b").Render()
	if buf.Len() != 0 {
		t.Errorf("machine mode wrote %q", buf.String())
	}
}
