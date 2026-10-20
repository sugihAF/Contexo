package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strings"

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

// The duck-typed methods through which packages ui cannot import
// (sync.HTTPError/NetworkError, config.RemoteError, syncstate.BusyError)
// hand their contract code and its extras to the envelope. Only ErrorCode is
// required; the others are read from the same error when present.
type (
	errorCoder    interface{ ErrorCode() string }
	errorAdvisor  interface{ ErrorAdvice() []string }
	errorDetailer interface{ ErrorDetails() any }
	httpStatuser  interface{ HTTPStatus() int }
)

// usagePrefixes are cobra/pflag parse-failure messages that reach Execute
// without a contract code (flag errors normally arrive coded through the
// root's FlagErrorFunc, Args errors through the wrapped validators; these
// cover legacyArgs' "unknown command", ValidateRequiredFlags and any path
// that bypasses both).
var usagePrefixes = []string{
	"unknown command",
	"unknown flag",
	"unknown shorthand flag",
	"flag needs an argument",
	"invalid argument",
	"required flag",
	"accepts ",
	"requires ",
}

// ToContractError maps any error chain to the contract error the envelope
// carries (nil for nil). The rules apply in order:
//
//  1. a *contract.Error anywhere in the chain supplies the code, advice,
//     HTTP status and details;
//  2. otherwise the first error in the chain (Unwrap and errors.Join are
//     both walked) whose ErrorCode() is non-empty supplies the code, plus
//     ErrorAdvice(), ErrorDetails() and HTTPStatus() when it has them;
//  3. otherwise *url.Error, any net.Error or context.DeadlineExceeded is
//     network;
//  4. otherwise a cobra parse message (matched as a prefix of the outermost
//     message) is usage;
//  5. otherwise internal.
//
// A code outside the stable table becomes internal, and details survive only
// on codes that define a details payload. The message is always the
// OUTERMOST err.Error() with ANSI escapes stripped and surrounding space
// trimmed. Advice is the first *CLIError's advice followed by the coded
// error's advice, de-duplicated in order and never nil. Retryable is derived
// from the code by contract.Error.Body.
func ToContractError(err error) *contract.Error {
	if err == nil {
		return nil
	}
	out := &contract.Error{Code: contract.CodeInternal, Message: cleanMessage(err.Error()), Err: err}

	var codedAdvice []string
	var ce *contract.Error
	if errors.As(err, &ce) {
		out.Code = ce.Code
		out.HTTPStatus = ce.HTTPStatus
		out.Details = ce.Details
		codedAdvice = ce.Advice
	} else if coded := firstCoded(err); coded != nil {
		out.Code = contract.ErrorCode(coded.(errorCoder).ErrorCode())
		if a, ok := coded.(errorAdvisor); ok {
			codedAdvice = a.ErrorAdvice()
		}
		if d, ok := coded.(errorDetailer); ok {
			out.Details = d.ErrorDetails()
		}
		if s, ok := coded.(httpStatuser); ok {
			out.HTTPStatus = s.HTTPStatus()
		}
	} else if isNetwork(err) {
		out.Code = contract.CodeNetwork
	} else if isUsageMessage(out.Message) {
		out.Code = contract.CodeUsage
	}

	if !contract.KnownCode(out.Code) {
		out.Code = contract.CodeInternal
	}
	if isNil(out.Details) || contract.NewDetails(out.Code) == nil {
		out.Details = nil
	}

	var cliAdvice []string
	var cli *CLIError
	if errors.As(err, &cli) {
		cliAdvice = cli.Advice
	}
	out.Advice = mergeAdvice(cliAdvice, codedAdvice)
	return out
}

// firstCoded returns the first error in err's chain, in errors.As order
// (depth-first through Unwrap() error and Unwrap() []error), whose
// ErrorCode() is non-empty.
func firstCoded(err error) error {
	var found error
	walkChain(err, func(e error) bool {
		if c, ok := e.(errorCoder); ok && c.ErrorCode() != "" {
			found = e
			return true
		}
		return false
	})
	return found
}

// walkChain visits err and everything it wraps, pre-order, until visit
// returns true. It reports whether visit stopped the walk.
func walkChain(err error, visit func(error) bool) bool {
	for err != nil {
		if visit(err) {
			return true
		}
		switch x := err.(type) {
		case interface{ Unwrap() error }:
			err = x.Unwrap()
		case interface{ Unwrap() []error }:
			for _, e := range x.Unwrap() {
				if walkChain(e, visit) {
					return true
				}
			}
			return false
		default:
			return false
		}
	}
	return false
}

func isNetwork(err error) bool {
	var ue *url.Error
	if errors.As(err, &ue) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// isUsageMessage matches msg (already ANSI-stripped and trimmed) against the
// cobra parse-failure prefixes.
func isUsageMessage(msg string) bool {
	for _, p := range usagePrefixes {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}

// ansiRE matches OSC strings (hyperlinks, titles), CSI sequences (colour,
// cursor) and the remaining two- and three-byte escapes (charset selection,
// save/restore cursor).
var ansiRE = regexp.MustCompile(
	"\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)" + // OSC ... BEL | ST
		"|\x1b\\[[0-?]*[ -/]*[@-~]" + // CSI
		"|\x1b[ -/]*[0-~]") // other escapes

// cleanMessage strips ANSI escapes (and any stray ESC byte) and trims
// surrounding whitespace: the envelope message is read by a program.
func cleanMessage(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\x1b", "")
	return strings.TrimSpace(s)
}

// mergeAdvice concatenates the lists, dropping empty entries and
// duplicates while keeping first-seen order. Never nil.
func mergeAdvice(lists ...[]string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, l := range lists {
		for _, a := range l {
			if a == "" || seen[a] {
				continue
			}
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// isNil reports whether v is nil or a nil pointer, map, slice or interface
// boxed in a non-nil any (which would encode as "details":null).
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return rv.IsNil()
	}
	return false
}

// Coded attaches a contract code (and advice) to err. The result is a
// *contract.Error whose message is err's and which still unwraps to err.
func Coded(err error, code contract.ErrorCode, advice ...string) error {
	if err == nil {
		return nil
	}
	return &contract.Error{Code: code, Message: err.Error(), Advice: advice, Err: err}
}

// EnvelopeMode reports whether a failure of cmd must be rendered as the
// ctx.error.v1 envelope with contract exit codes (E21): when cmd's json flag
// is true (which covers show's hidden default-true flag, E20), or when the
// raw args hold --json or --json=true before a "--" terminator, so a failure
// that happens before the flag is parsed (an unknown flag or command) still
// yields an envelope. cmd may be nil, leaving only the raw-args check.
//
// The stdio/hook/completion machine surfaces (bare `ctx mcp`,
// `ctx capture turn`, shell completion) never emit an envelope, whatever
// their args: their streams belong to another protocol.
func EnvelopeMode(cmd *cobra.Command, args []string) bool {
	if cmd != nil {
		if machineCommand(cmd.CommandPath()) {
			return false
		}
		if f := cmd.Flags().Lookup("json"); f != nil && f.Value.String() == "true" {
			return true
		}
	}
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--json" || a == "--json=true" {
			return true
		}
	}
	return false
}

// RenderEnvelope writes err as exactly one ctx.error.v1 line to the UI's
// writer, whatever the mode, and returns the envelope's exit code. A nil err
// writes nothing and returns 0.
func (u *UI) RenderEnvelope(err error) int {
	ce := ToContractError(err)
	if ce == nil {
		return contract.ExitOK
	}
	_ = contract.WriteEnvelope(u.w, ce)
	return contract.ExitCodeFor(ce.Code)
}
