package ui

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops/contract"
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

// ToContractError maps any error chain to the contract error the envelope
// carries. Stage-0 skeleton: implemented by WP-0.1 Task C.
func ToContractError(err error) *contract.Error {
	return contract.New(contract.CodeInternal, err.Error())
}

// Coded attaches a contract code (and advice) to err.
func Coded(err error, code contract.ErrorCode, advice ...string) error {
	if err == nil {
		return nil
	}
	return &contract.Error{Code: code, Message: err.Error(), Advice: advice, Err: err}
}

// EnvelopeMode reports whether a failure of cmd must be rendered as the
// ctx.error.v1 envelope (E21).
func EnvelopeMode(cmd *cobra.Command, args []string) bool { return false }

// RenderEnvelope writes err as exactly one envelope line and returns its
// exit code.
func (u *UI) RenderEnvelope(err error) int { return contract.ExitInternal }
