package ui

import (
	"errors"
	"fmt"
)

// CLIError carries a failure plus the commands that fix it, so advice stops
// being baked into message strings.
//
// Before this existed, 18+ error sites hardcoded their advice, which drifted:
// "not authenticated" had five different phrasings naming three different
// commands, and two sites advised `ctx remote add`, which does not exist.
type CLIError struct {
	Msg     string
	Advice  []string
	wrapped error
}

func (e *CLIError) Error() string { return e.Msg }
func (e *CLIError) Unwrap() error { return e.wrapped }

// Suggest attaches actionable commands to err. Calling it twice on the same
// error appends rather than replacing, so a wrapper can add context.
func Suggest(err error, cmds ...string) error {
	if err == nil {
		return nil
	}
	var ce *CLIError
	if errors.As(err, &ce) {
		ce.Advice = append(ce.Advice, cmds...)
		return err
	}
	return &CLIError{Msg: err.Error(), Advice: cmds, wrapped: err}
}

// RenderError prints a failure. In machine mode it degrades to a single bare
// line so scripts still see the reason on stderr.
func (u *UI) RenderError(err error) {
	if err == nil {
		return
	}
	var ce *CLIError
	if !errors.As(err, &ce) {
		if u.mode.Machine {
			fmt.Fprintf(u.w, "ctx: %v\n", err)
			return
		}
		u.Fail("%v", err)
		return
	}
	if u.mode.Machine {
		fmt.Fprintf(u.w, "ctx: %s\n", ce.Msg)
		return
	}
	u.Fail("%s", ce.Msg)
	for _, c := range ce.Advice {
		u.Hint("try", c)
	}
}
