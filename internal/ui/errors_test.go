package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSuggestWrapsAndPreservesMessage(t *testing.T) {
	err := Suggest(errors.New("no credentials"), "ctx auth login")
	if !strings.Contains(err.Error(), "no credentials") {
		t.Errorf("Error() = %q", err.Error())
	}
	var ce *CLIError
	if !errors.As(err, &ce) {
		t.Fatal("Suggest must produce a *CLIError")
	}
	if len(ce.Advice) != 1 || ce.Advice[0] != "ctx auth login" {
		t.Errorf("Advice = %v", ce.Advice)
	}
}

// Suggest must not break errors.Is/As on the original error.
func TestSuggestPreservesUnwrapChain(t *testing.T) {
	sentinel := errors.New("root cause")
	err := Suggest(sentinel, "ctx init")
	if !errors.Is(err, sentinel) {
		t.Error("Suggest broke the unwrap chain")
	}
}

func TestRenderErrorShowsHeadlineAndAdvice(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: false, Dark: true})
	u.RenderError(Suggest(errors.New("no credentials"), "ctx auth login"))
	out := buf.String()
	if !strings.Contains(out, "no credentials") {
		t.Errorf("missing headline: %q", out)
	}
	if !strings.Contains(out, "ctx auth login") {
		t.Errorf("missing advice: %q", out)
	}
}

func TestRenderErrorHandlesPlainError(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: false, Dark: true})
	u.RenderError(errors.New("boom"))
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("plain error not rendered: %q", buf.String())
	}
}

// Machine mode must still report the reason -- silently swallowing a failure
// would be worse than chrome.
func TestRenderErrorMachineModeStillReports(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{Machine: true})
	u.RenderError(Suggest(errors.New("no repo"), "ctx remote set-repo"))
	out := buf.String()
	if !strings.Contains(out, "no repo") {
		t.Errorf("machine mode swallowed the error: %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("machine mode emitted ANSI: %q", out)
	}
}
