package ui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
)

// ErrAborted is returned when the user cancels a prompt (Ctrl-C / Esc).
var ErrAborted = errors.New("cancelled")

// Prompter is the single interactive-input implementation for the CLI. It
// replaces five separate y/N readers and the survey picker.
//
// It holds ONE bufio.Reader for its whole life. Two readers over the same
// stdin let the first buffer past its newline and swallow the next answer --
// a real bug previously memorialized by a comment in cli/migrate.go. Create
// one Prompter per command run and reuse it for every prompt.
type Prompter struct {
	u  *UI
	br *bufio.Reader
}

func (u *UI) Prompter(in io.Reader) *Prompter {
	return &Prompter{u: u, br: bufio.NewReader(in)}
}

// Confirm asks a y/N question. Defaults to No.
//
// An unterminated final line still counts: "y" with no trailing newline is a
// yes. That matches how the pre-existing push confirmation behaved, and
// scripts that pipe a bare "y" rely on it.
func (p *Prompter) Confirm(question string) (bool, error) {
	return p.confirm(question, false)
}

// ConfirmDestructive is Confirm for irreversible actions. It additionally
// requires a COMPLETE line: if the reader hits EOF mid-answer, it declines
// even when the partial text says yes.
//
// This is not pedantry. `ctx detach` performs os.RemoveAll on the knowledge
// directory, and a truncated pipe should never be read as consent. The two
// prompts genuinely differed before they were unified here, and the strict
// rule is the one the destructive path had.
func (p *Prompter) ConfirmDestructive(question string) (bool, error) {
	return p.confirm(question, true)
}

func (p *Prompter) confirm(question string, requireCompleteLine bool) (bool, error) {
	if p.u.mode.Machine {
		return false, nil
	}
	if p.u.mode.TTY {
		var v bool
		err := p.form(huh.NewConfirm().Title(question).
			Affirmative("Yes").Negative("No").Value(&v))
		return v, err
	}
	fmt.Fprintf(p.u.w, "%s [y/N]: ", question)
	line, err := p.br.ReadString('\n')
	if err != nil {
		if requireCompleteLine || line == "" {
			return false, nil
		}
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes", nil
}

// Line asks a free-text question and returns the raw answer line, newline
// included, matching bufio.Reader.ReadString semantics that callers expect.
func (p *Prompter) Line(question string) (string, error) {
	if p.u.mode.Machine {
		return "", nil
	}
	if p.u.mode.TTY {
		var v string
		if err := p.form(huh.NewInput().Title(question).Value(&v)); err != nil {
			return "", err
		}
		return v + "\n", nil
	}
	fmt.Fprintf(p.u.w, "%s: ", question)
	return p.br.ReadString('\n')
}

// Select returns the 0-based index of the chosen option.
//
// Off a TTY it prints a numbered list and reads an index. Constructing a huh
// form there would read the stream as KEY EVENTS, not lines -- "all\n" becomes
// keystrokes a,l,l,Enter, which is type-ahead filtering, not a selection.
func (p *Prompter) Select(title string, labels []string) (int, error) {
	if len(labels) == 0 {
		return 0, errors.New("no options")
	}
	if p.u.mode.Machine {
		return 0, errors.New("cannot prompt in non-interactive mode")
	}
	if p.u.mode.TTY {
		var v int
		opts := make([]huh.Option[int], len(labels))
		for i, l := range labels {
			opts[i] = huh.NewOption(l, i)
		}
		err := p.form(huh.NewSelect[int]().Title(title).Options(opts...).Value(&v))
		return v, err
	}

	p.u.Eyebrow(strings.ToUpper(title))
	for i, l := range labels {
		fmt.Fprintf(p.u.w, "  %d  %s\n", i+1, l)
	}
	fmt.Fprintf(p.u.w, "Select [1-%d]: ", len(labels))
	line, err := p.br.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return 0, ErrAborted
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(line))
	if convErr != nil || n < 1 || n > len(labels) {
		return 0, fmt.Errorf("invalid selection %q", strings.TrimSpace(line))
	}
	return n - 1, nil
}

// form runs a single-field huh form against the real terminal. huh reads
// os.Stdin directly; that is correct on a TTY and is exactly why every caller
// must take the non-TTY branch otherwise.
func (p *Prompter) form(f huh.Field) error {
	err := huh.NewForm(huh.NewGroup(f)).WithTheme(p.theme()).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrAborted
	}
	return err
}

func (p *Prompter) theme() *huh.Theme {
	t := huh.ThemeBase()
	accent := Accent.pick(p.u.mode.Dark)
	t.Focused.Title = t.Focused.Title.Foreground(accent).Bold(true)
	t.Focused.Base = t.Focused.Base.BorderForeground(accent)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(accent)
	t.Focused.SelectSelector = t.Focused.SelectSelector.
		Foreground(accent).SetString(p.u.glyphs.Cursor + " ")
	return t
}
