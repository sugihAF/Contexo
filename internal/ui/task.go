package ui

import (
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// Task reports progress on a long operation.
//
// Three behaviours, chosen by Mode:
//   - machine: silent. Never construct a bubbletea program on a machine path;
//     it writes cursor-control escapes unconditionally, which the colour
//     profile does NOT gate, and it would corrupt JSON-RPC or a hook payload.
//   - tty: an inline (not full-screen) spinner bound explicitly to the UI's
//     writer via tea.WithOutput.
//   - non-tty: exactly two plain lines. The docs capture harness records this
//     branch (stdout to a file, stdin from /dev/null), and tests assert on it.
type Task struct {
	u       *UI
	label   string
	started time.Time

	mu   sync.Mutex
	prog *tea.Program
	done chan struct{}
}

func (u *UI) Task(label string) *Task {
	t := &Task{u: u, label: label, started: time.Now()}
	if u.mode.Machine {
		return t
	}
	if !u.mode.TTY {
		fmt.Fprintf(u.w, "%s...\n", label)
		return t
	}
	t.start()
	return t
}

func (t *Task) start() {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = t.u.style(Accent)
	m := &spinModel{sp: sp, label: t.label, u: t.u}

	// WithInput(nil): the spinner reads no keys. On bubbletea's default input
	// a terminal stdin goes raw, so ^C arrives as a KeyMsg this model drops
	// instead of raising SIGINT; a non-terminal stdin makes it open /dev/tty
	// (CONIN$ on Windows), which tests must never touch.
	prog := tea.NewProgram(m,
		tea.WithOutput(t.u.w),
		tea.WithInput(nil),
		tea.WithoutSignalHandler(),
		tea.WithoutCatchPanics(),
	)
	done := make(chan struct{})

	t.mu.Lock()
	t.prog = prog
	t.done = done
	t.mu.Unlock()

	// The goroutine closes over the locals, never the fields: it may first be
	// scheduled after finish has already set t.prog to nil.
	go func() {
		defer close(done)
		_, _ = prog.Run()
	}()
}

// Detail updates the trailing context on the live line. No-op off a TTY --
// the non-TTY contract is exactly two lines, and a third would break the
// docs captures and the tests that assert on them.
func (t *Task) Detail(s string) {
	t.mu.Lock()
	p := t.prog
	t.mu.Unlock()
	if p != nil {
		p.Send(detailMsg(s))
	}
}

// Done ends the task with a success mark.
func (t *Task) Done(format string, a ...any) { t.finish(t.u.Success, format, a...) }

// Fail ends the task with a failure mark.
func (t *Task) Fail(format string, a ...any) { t.finish(t.u.Fail, format, a...) }

func (t *Task) finish(emit func(string, ...any), format string, a ...any) {
	t.mu.Lock()
	p := t.prog
	t.prog = nil
	done := t.done
	t.mu.Unlock()

	if p != nil {
		p.Quit()
		<-done
	}
	if t.u.mode.Machine {
		return
	}
	msg := fmt.Sprintf(format, a...)
	if el := time.Since(t.started); el >= time.Second {
		msg = fmt.Sprintf("%s  %s", msg, formatElapsed(el))
	}
	emit("%s", msg)
}

func formatElapsed(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

type detailMsg string

type spinModel struct {
	sp     spinner.Model
	label  string
	detail string
	u      *UI
}

func (m *spinModel) Init() tea.Cmd { return m.sp.Tick }

func (m *spinModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case detailMsg:
		m.detail = string(v)
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *spinModel) View() string {
	line := m.sp.View() + m.u.style(FG2).Render(m.label)
	if m.detail != "" {
		line += "  " + m.u.style(FG4).Render(m.detail)
	}
	return line
}
