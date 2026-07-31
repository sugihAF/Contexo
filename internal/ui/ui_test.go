package ui

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

func TestMachineCommandCoversEveryMachineSurface(t *testing.T) {
	machine := []string{
		"ctx mcp", "ctx capture turn", "ctx completion bash", "ctx __complete",
	}
	for _, p := range machine {
		if !machineCommand(p) {
			t.Errorf("machineCommand(%q) = false, want true", p)
		}
	}
	human := []string{"ctx status", "ctx push", "ctx init", "ctx members list"}
	for _, p := range human {
		if machineCommand(p) {
			t.Errorf("machineCommand(%q) = true, want false", p)
		}
	}
}

// A buffer-bound renderer must emit no ANSI, so the existing cobra-buffer
// tests keep passing unchanged.
func TestBufferSinkEmitsNoANSI(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: false, Dark: true})
	u.Success("pushed %d pages", 42)
	got := buf.String()
	if strings.Contains(got, "\x1b[") {
		t.Errorf("buffer output carries ANSI: %q", got)
	}
	if !strings.Contains(got, "pushed 42 pages") {
		t.Errorf("missing message: %q", got)
	}
}

// Forcing the profile proves styling is testable deterministically.
func TestForcedProfileEmitsANSI(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: true, Dark: true})
	u.r.SetColorProfile(termenv.TrueColor)
	u.Success("ok")
	if !strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("forced truecolor emitted no ANSI: %q", buf.String())
	}
}

// Machine mode must write nothing at all, on any primitive.
func TestMachineModeWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{Machine: true})
	u.Brand("connected", true)
	u.Eyebrow("REMOTE")
	u.Field("server", "api.contexo.live")
	u.Success("done")
	u.Warn("careful")
	u.Fail("broken")
	u.Hint("try", "ctx auth login")
	u.Line("data")
	if buf.Len() != 0 {
		t.Errorf("machine mode wrote %d bytes: %q", buf.Len(), buf.String())
	}
}

func TestFieldAlignsValues(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{Dark: true})
	u.Field("server", "api.contexo.live")
	u.Field("repo", "contexo")
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), buf.String())
	}
	if strings.Index(lines[0], "api.") != strings.Index(lines[1], "contexo") {
		t.Errorf("values not column-aligned:\n%q\n%q", lines[0], lines[1])
	}
}

func TestNewAcceptsAnyIOWriter(t *testing.T) {
	var w io.Writer = io.Discard
	if New(w, Mode{}) == nil {
		t.Error("New must accept a bare io.Writer")
	}
}

func TestDarkBackgroundHonoursCtxTheme(t *testing.T) {
	t.Setenv("CTX_THEME", "light")
	if darkBackground() {
		t.Error("CTX_THEME=light must select the light palette")
	}
	t.Setenv("CTX_THEME", "dark")
	if !darkBackground() {
		t.Error("CTX_THEME=dark must select the dark palette")
	}
}

// NO_COLOR is honoured by termenv automatically, but Mode must also stop
// claiming TTY so spinners and forms stay off.
func TestNoColorDisablesTTY(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if DetectMode(nil, io.Discard).TTY {
		t.Error("NO_COLOR must clear Mode.TTY")
	}
}

// REGRESSION: Brand always painted an accent dot, so a fresh project showed a
// green status light beside the words "not authenticated". Accent is reserved
// for brand and success.
func TestBrandDotFollowsHealth(t *testing.T) {
	render := func(healthy bool) string {
		var buf bytes.Buffer
		u := New(&buf, Mode{Dark: true, TTY: true})
		u.r.SetColorProfile(termenv.TrueColor)
		u.Brand("state", healthy)
		return buf.String()
	}
	accentRGB := "161;234;89" // #a1ea5a
	healthy, unhealthy := render(true), render(false)

	if !strings.Contains(healthy, accentRGB) {
		t.Errorf("healthy brand should use the accent dot: %q", healthy)
	}
	// The wordmark is always accent; the DOT must not be when unhealthy.
	if strings.Count(unhealthy, accentRGB) != 1 {
		t.Errorf("unhealthy brand paints the dot accent too: %q", unhealthy)
	}
}
