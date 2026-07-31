package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfirmYesNoVariants(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"y\n", true}, {"Y\n", true}, {"yes\n", true}, {"YES\n", true},
		{"n\n", false}, {"\n", false}, {"maybe\n", false},
	}
	for _, c := range cases {
		var out bytes.Buffer
		u := New(&out, Mode{TTY: false, Dark: true})
		got, err := u.Prompter(strings.NewReader(c.in)).Confirm("Proceed?")
		if err != nil {
			t.Fatalf("Confirm(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("Confirm(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// EOF must read as a decline, not an error -- a closed stdin declines.
func TestConfirmEOFDeclines(t *testing.T) {
	var out bytes.Buffer
	u := New(&out, Mode{TTY: false, Dark: true})
	got, err := u.Prompter(strings.NewReader("")).Confirm("Proceed?")
	if err != nil || got {
		t.Errorf("EOF confirm = (%v, %v), want (false, nil)", got, err)
	}
}

// THE regression test for the single-reader bug. Three successive prompts on
// one stream must each consume exactly one line.
func TestPrompterSharesOneReaderAcrossPrompts(t *testing.T) {
	var out bytes.Buffer
	u := New(&out, Mode{TTY: false, Dark: true})
	p := u.Prompter(strings.NewReader("y\nall\ny\n"))

	ok, err := p.Confirm("Install?")
	if err != nil || !ok {
		t.Fatalf("first confirm = (%v, %v), want (true, nil)", ok, err)
	}
	line, err := p.Line("Select items")
	if err != nil {
		t.Fatalf("Line: %v", err)
	}
	if strings.TrimSpace(line) != "all" {
		t.Fatalf("Line = %q, want \"all\"", line)
	}
	ok, err = p.Confirm("Stage?")
	if err != nil || !ok {
		t.Fatalf("second confirm = (%v, %v), want (true, nil) -- newline swallowed", ok, err)
	}
}

// Off a TTY, Select falls back to numeric entry rather than constructing a
// huh form (which would read the stream as key events and block).
func TestSelectNonTTYUsesNumericFallback(t *testing.T) {
	var out bytes.Buffer
	u := New(&out, Mode{TTY: false, Dark: true})
	idx, err := u.Prompter(strings.NewReader("2\n")).Select("Pick a repo",
		[]string{"contexo", "acme-web", "internal-tools"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if idx != 1 {
		t.Errorf("Select = %d, want 1 (0-based)", idx)
	}
	if !strings.Contains(out.String(), "acme-web") {
		t.Errorf("options not listed: %q", out.String())
	}
}

func TestSelectRejectsOutOfRange(t *testing.T) {
	var out bytes.Buffer
	u := New(&out, Mode{TTY: false, Dark: true})
	if _, err := u.Prompter(strings.NewReader("9\n")).Select("Pick", []string{"a", "b"}); err == nil {
		t.Error("out-of-range selection must error")
	}
}

// REGRESSION: `ctx detach` performs an irreversible os.RemoveAll. Unifying the
// five confirm implementations took the LENIENT EOF rule, which meant
// `printf 'y' | ctx detach` (no trailing newline) started deleting where it
// previously aborted. Destructive prompts require a complete line.
func TestConfirmDestructiveRejectsUnterminatedYes(t *testing.T) {
	var out bytes.Buffer
	u := New(&out, Mode{TTY: false, Dark: true})
	got, err := u.Prompter(strings.NewReader("y")).ConfirmDestructive("Proceed?")
	if err != nil {
		t.Fatalf("ConfirmDestructive: %v", err)
	}
	if got {
		t.Error("a truncated pipe must never read as consent to delete")
	}
}

// ...but a properly terminated yes still works.
func TestConfirmDestructiveAcceptsCompleteYes(t *testing.T) {
	var out bytes.Buffer
	u := New(&out, Mode{TTY: false, Dark: true})
	got, err := u.Prompter(strings.NewReader("y\n")).ConfirmDestructive("Proceed?")
	if err != nil || !got {
		t.Errorf("ConfirmDestructive(\"y\n\") = (%v, %v), want (true, nil)", got, err)
	}
}

// The non-destructive path keeps the lenient rule the old push confirm had,
// so scripts piping a bare "y" are not broken.
func TestConfirmAcceptsUnterminatedYes(t *testing.T) {
	var out bytes.Buffer
	u := New(&out, Mode{TTY: false, Dark: true})
	got, _ := u.Prompter(strings.NewReader("y")).Confirm("Proceed?")
	if !got {
		t.Error("non-destructive confirm should still accept an unterminated yes")
	}
}
