package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops/contract"
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

// wp01DuckErr stands in for errors the ui package cannot import (sync.HTTPError,
// config.RemoteError, syncstate.BusyError): they are reached only through the
// duck-typed ErrorCode/ErrorAdvice/ErrorDetails/HTTPStatus methods.
type wp01DuckErr struct {
	msg     string
	code    string
	advice  []string
	details any
	status  int
	inner   error
}

func (e *wp01DuckErr) Error() string         { return e.msg }
func (e *wp01DuckErr) Unwrap() error         { return e.inner }
func (e *wp01DuckErr) ErrorCode() string     { return e.code }
func (e *wp01DuckErr) ErrorAdvice() []string { return e.advice }
func (e *wp01DuckErr) ErrorDetails() any     { return e.details }
func (e *wp01DuckErr) HTTPStatus() int       { return e.status }

// wp01CodeOnlyErr has ErrorCode and nothing else: the optional methods are
// optional.
type wp01CodeOnlyErr struct{ code string }

func (e wp01CodeOnlyErr) Error() string     { return "code only" }
func (e wp01CodeOnlyErr) ErrorCode() string { return e.code }

func TestToContractErrorWalksWrappedChain(t *testing.T) {
	inner := contract.New(contract.CodeConflict, "c", "b")
	err := Suggest(fmt.Errorf("x: %w", inner), "a")

	ce := ToContractError(err)
	if ce == nil {
		t.Fatal("nil contract error")
	}
	if ce.Code != contract.CodeConflict {
		t.Errorf("code = %q, want conflict", ce.Code)
	}
	if ce.Message != "x: c" {
		t.Errorf("message = %q, want the outermost text %q", ce.Message, "x: c")
	}
	// CLIError advice first, then the coded error's advice.
	if want := []string{"a", "b"}; !reflect.DeepEqual(ce.Advice, want) {
		t.Errorf("advice = %q, want %q", ce.Advice, want)
	}
	if !errors.Is(ce, inner) {
		t.Error("the mapped error must still unwrap to the original chain")
	}

	// HTTP status and details travel from a *contract.Error found deep in
	// the chain.
	deep := &contract.Error{Code: contract.CodeStale, Message: "stale", HTTPStatus: 409,
		Details: &contract.StaleDetails{Path: "wiki/a.md"}}
	got := ToContractError(fmt.Errorf("resolve: %w", fmt.Errorf("y: %w", deep)))
	if got.Code != contract.CodeStale || got.HTTPStatus != 409 {
		t.Errorf("got code %q status %d", got.Code, got.HTTPStatus)
	}
	if d, ok := got.Details.(*contract.StaleDetails); !ok || d.Path != "wiki/a.md" {
		t.Errorf("details = %#v", got.Details)
	}

	// Duplicate advice collapses, keeping first-seen order.
	dup := ToContractError(Suggest(contract.New(contract.CodeNotInitialized, "m", "ctx init", "x"), "ctx init"))
	if want := []string{"ctx init", "x"}; !reflect.DeepEqual(dup.Advice, want) {
		t.Errorf("advice = %q, want %q", dup.Advice, want)
	}

	// A *contract.Error wins over a duck-typed or network error deeper down.
	coded := Coded(&url.Error{Op: "Get", URL: "http://x", Err: errors.New("refused")}, contract.CodeUsage)
	if c := ToContractError(coded).Code; c != contract.CodeUsage {
		t.Errorf("Coded(url.Error, usage) = %q, want usage", c)
	}

	if ToContractError(nil) != nil {
		t.Error("ToContractError(nil) must be nil")
	}
}

func TestToContractErrorDuckTyped(t *testing.T) {
	qd := &contract.QuotaDetails{QuotaCode: "repo_limit", Limit: 3, UpgradeURL: "https://acme.dev/upgrade"}
	duck := &wp01DuckErr{
		msg: "sync: quota exceeded", code: "quota_exceeded",
		advice: []string{"ctx upgrade"}, details: qd, status: 402,
	}

	for name, err := range map[string]error{
		"direct":  duck,
		"wrapped": fmt.Errorf("push: %w", duck),
		"joined":  errors.Join(errors.New("first"), fmt.Errorf("second: %w", duck)),
		"suggest": Suggest(fmt.Errorf("push: %w", duck), "ctx status"),
	} {
		t.Run(name, func(t *testing.T) {
			ce := ToContractError(err)
			if ce.Code != contract.CodeQuotaExceeded {
				t.Errorf("code = %q", ce.Code)
			}
			if ce.HTTPStatus != 402 {
				t.Errorf("http status = %d", ce.HTTPStatus)
			}
			if ce.Details != qd {
				t.Errorf("details = %#v, want %#v", ce.Details, qd)
			}
			if ce.Message != strings.TrimSpace(err.Error()) {
				t.Errorf("message = %q, want outermost %q", ce.Message, err.Error())
			}
			want := []string{"ctx upgrade"}
			if name == "suggest" {
				want = []string{"ctx status", "ctx upgrade"}
			}
			if !reflect.DeepEqual(ce.Advice, want) {
				t.Errorf("advice = %q, want %q", ce.Advice, want)
			}
		})
	}

	// The first error that reports a NON-EMPTY code wins; an empty code is
	// skipped and the walk continues.
	inner := &wp01DuckErr{msg: "inner", code: "busy", status: 0}
	outer := &wp01DuckErr{msg: "outer", code: "", advice: []string{"ignored"}, status: 500, inner: inner}
	ce := ToContractError(outer)
	if ce.Code != contract.CodeBusy {
		t.Errorf("code = %q, want busy from the first non-empty ErrorCode", ce.Code)
	}
	if ce.HTTPStatus != 0 || len(ce.Advice) != 0 {
		t.Errorf("optional fields must come from the coded error only: status %d advice %q",
			ce.HTTPStatus, ce.Advice)
	}

	// The optional methods are optional.
	only := ToContractError(fmt.Errorf("w: %w", wp01CodeOnlyErr{code: "forbidden"}))
	if only.Code != contract.CodeForbidden || only.HTTPStatus != 0 || only.Details != nil {
		t.Errorf("code-only error mapped to %#v", only)
	}
	if only.Advice == nil {
		t.Error("advice must never be nil")
	}

	// A duck code beats the network rule: sync.NetworkError and HTTPError
	// wrap transport errors but carry their own code.
	srv := &wp01DuckErr{msg: "sync: 502", code: "server_error",
		inner: &url.Error{Op: "Get", URL: "http://x", Err: errors.New("eof")}}
	if c := ToContractError(srv).Code; c != contract.CodeServerError {
		t.Errorf("code = %q, want server_error", c)
	}

	// A typed-nil details pointer is no details at all (no "details":null).
	nilDetails := ToContractError(&wp01DuckErr{msg: "m", code: "busy", details: (*contract.BusyDetails)(nil)})
	if nilDetails.Details != nil {
		t.Errorf("typed-nil details leaked: %#v", nilDetails.Details)
	}
}

func TestToContractErrorUnknownDuckCodeIsInternal(t *testing.T) {
	err := &wp01DuckErr{msg: "old code", code: "capture_pending", advice: []string{"ctx push"},
		details: &contract.ConflictDetails{Paths: []string{"wiki/a.md"}}, status: 409}
	ce := ToContractError(err)
	if ce.Code != contract.CodeInternal {
		t.Errorf("code = %q, want internal for an unknown code", ce.Code)
	}
	if ce.Details != nil {
		t.Errorf("internal carries no details, got %#v", ce.Details)
	}
	if !reflect.DeepEqual(ce.Advice, []string{"ctx push"}) {
		t.Errorf("advice = %q", ce.Advice)
	}
	if ce.Message != "old code" {
		t.Errorf("message = %q", ce.Message)
	}
	// The same normalisation applies to a *contract.Error with a code
	// outside the stable table.
	if c := ToContractError(contract.New("bogus_code", "m")).Code; c != contract.CodeInternal {
		t.Errorf("unknown contract code = %q, want internal", c)
	}
	// Details on a code that has none are dropped.
	f := ToContractError(&contract.Error{Code: contract.CodeForbidden, Message: "m",
		Details: &contract.ConflictDetails{}})
	if f.Details != nil {
		t.Errorf("forbidden carries no details, got %#v", f.Details)
	}
}

func TestToContractErrorNetwork(t *testing.T) {
	cases := map[string]error{
		"url.Error":           &url.Error{Op: "Get", URL: "https://api.acme.dev/v1/repos", Err: errors.New("connection refused")},
		"wrapped url.Error":   fmt.Errorf("pull: %w", &url.Error{Op: "Post", URL: "https://api.acme.dev", Err: errors.New("eof")}),
		"deadline":            context.DeadlineExceeded,
		"wrapped deadline":    fmt.Errorf("status: %w", context.DeadlineExceeded),
		"net.OpError":         &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
		"suggested net error": Suggest(&net.DNSError{Err: "no such host", Name: "api.acme.dev"}, "ctx auth status"),
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			ce := ToContractError(err)
			if ce.Code != contract.CodeNetwork {
				t.Errorf("code = %q, want network", ce.Code)
			}
			if !ce.Body().Retryable {
				t.Error("network must be retryable")
			}
		})
	}
}

func TestToContractErrorCobraUsage(t *testing.T) {
	for _, msg := range []string{
		`unknown command "bogus" for "ctx"`,
		"unknown flag: --bogus",
		"unknown shorthand flag: 'z' in -z",
		"flag needs an argument: --at",
		`invalid argument "x" for "--limit" flag: strconv.ParseInt: parsing "x": invalid syntax`,
		`required flag(s) "at" not set`,
		"accepts 1 arg(s), received 0",
		"requires at least 1 arg(s), only received 0",
	} {
		if c := ToContractError(errors.New(msg)).Code; c != contract.CodeUsage {
			t.Errorf("%q -> %q, want usage", msg, c)
		}
	}
	// Only the OUTERMOST message is matched, and only as a prefix.
	for _, msg := range []string{
		"push: unknown flag in config",
		"the server requires a newer client",
	} {
		if c := ToContractError(errors.New(msg)).Code; c != contract.CodeInternal {
			t.Errorf("%q -> %q, want internal", msg, c)
		}
	}
	if c := ToContractError(fmt.Errorf("x: %w", errors.New("unknown flag: --y"))).Code; c != contract.CodeInternal {
		t.Errorf("wrapped parse message -> %q, want internal", c)
	}

	// A real cobra parse failure, unwrapped by any FlagErrorFunc.
	cmd := &cobra.Command{Use: "x", SilenceErrors: true, SilenceUsage: true,
		RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.SetArgs([]string{"--nope"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("cobra accepted an unknown flag")
	}
	if c := ToContractError(err).Code; c != contract.CodeUsage {
		t.Errorf("cobra %q -> %q, want usage", err, c)
	}
}

func TestToContractErrorUnknownIsInternal(t *testing.T) {
	ce := ToContractError(errors.New("boom"))
	if ce.Code != contract.CodeInternal {
		t.Errorf("code = %q", ce.Code)
	}
	if ce.Message != "boom" {
		t.Errorf("message = %q", ce.Message)
	}
	if ce.Advice == nil || len(ce.Advice) != 0 {
		t.Errorf("advice = %#v, want empty non-nil", ce.Advice)
	}
	if ce.HTTPStatus != 0 || ce.Details != nil {
		t.Errorf("unexpected status/details: %d %#v", ce.HTTPStatus, ce.Details)
	}
	if b := ce.Body(); b.Retryable || b.Advice == nil {
		t.Errorf("body = %#v", b)
	}
	// A CLIError with no code underneath is internal but keeps its advice.
	s := ToContractError(Suggest(errors.New("no credentials"), "ctx auth login"))
	if s.Code != contract.CodeInternal || !reflect.DeepEqual(s.Advice, []string{"ctx auth login"}) {
		t.Errorf("suggested plain error = %#v", s)
	}
}

func TestToContractErrorStripsANSI(t *testing.T) {
	err := errors.New("  \x1b[1;31mpush failed\x1b[0m: see \x1b]8;;https://acme.dev\x1b\\docs\x1b]8;;\x1b\\ now\x1b(B \n")
	ce := ToContractError(err)
	if ce.Message != "push failed: see docs now" {
		t.Errorf("message = %q", ce.Message)
	}
	if strings.ContainsRune(ce.Message, '\x1b') {
		t.Errorf("ESC survived: %q", ce.Message)
	}
	// A stray lone ESC is removed too.
	if m := ToContractError(errors.New("a\x1bb")).Message; strings.ContainsRune(m, '\x1b') {
		t.Errorf("lone ESC survived: %q", m)
	}
}

func TestExitCodeTable(t *testing.T) {
	want := map[contract.ErrorCode]int{
		contract.CodeInternal:          1,
		contract.CodeUsage:             3,
		contract.CodeInvalidPath:       3,
		contract.CodeNotAuthenticated:  4,
		contract.CodeAuthRejected:      4,
		contract.CodeForbidden:         4,
		contract.CodeNotInitialized:    5,
		contract.CodeRepoNotConfigured: 5,
		contract.CodeServerUnresolved:  5,
		contract.CodeConflict:          6,
		contract.CodeStale:             6,
		contract.CodeNetwork:           7,
		contract.CodeServerError:       7,
		contract.CodeRateLimited:       7,
		contract.CodeRepoNotFound:      8,
		contract.CodePageNotFound:      8,
		contract.CodeBaseUnavailable:   8,
		contract.CodeSHANotFound:       8,
		contract.CodeDistillRequired:   9,
		contract.CodeNotResolvable:     9,
		contract.CodeServerUnsupported: 10,
		contract.CodeNotImplemented:    10,
		contract.CodeQuotaExceeded:     11,
		contract.CodeBusy:              12,
	}
	if len(want) != 24 {
		t.Fatalf("table has %d codes, want 24", len(want))
	}
	for code, exit := range want {
		if !contract.KnownCode(code) {
			t.Errorf("%q is not a known code", code)
		}
		err := fmt.Errorf("wrapped: %w", contract.New(code, "m"))
		if got := ExitCode(err, true); got != exit {
			t.Errorf("ExitCode(%s, envelope) = %d, want %d", code, got, exit)
		}
		if got := ExitCode(err, false); got != 1 {
			t.Errorf("ExitCode(%s, human) = %d, want 1", code, got)
		}
		duck := &wp01DuckErr{msg: "m", code: string(code)}
		if got := ExitCode(duck, true); got != exit {
			t.Errorf("ExitCode(duck %s, envelope) = %d, want %d", code, got, exit)
		}
	}
	if ExitCode(nil, true) != 0 || ExitCode(nil, false) != 0 {
		t.Error("nil error must exit 0")
	}
	if got := ExitCode(errors.New("boom"), true); got != 1 {
		t.Errorf("plain error in envelope mode = %d, want 1", got)
	}
	if got := ExitCode(errors.New("unknown flag: --x"), true); got != 3 {
		t.Errorf("parse error in envelope mode = %d, want 3", got)
	}
}

func TestRenderEnvelopeIsOneLine(t *testing.T) {
	for _, m := range []Mode{{}, {Machine: true}, {TTY: true, Dark: true}} {
		var buf bytes.Buffer
		u := New(&buf, m)
		err := fmt.Errorf("\x1b[31mshow\x1b[0m: two\nlines <&> %w", contract.New(contract.CodeBaseUnavailable, "no base"))
		exit := u.RenderEnvelope(err)
		out := buf.String()

		if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
			t.Fatalf("mode %+v: want exactly one newline-terminated line, got %q", m, out)
		}
		if strings.ContainsRune(out, '\x1b') {
			t.Errorf("mode %+v: ESC in envelope: %q", m, out)
		}
		if !strings.Contains(out, `"advice":[]`) {
			t.Errorf("mode %+v: advice must be [] not null/omitted: %q", m, out)
		}
		if !strings.Contains(out, "lines <&> no base") {
			t.Errorf("mode %+v: envelope is HTML-escaped: %q", m, out)
		}

		dec := json.NewDecoder(strings.NewReader(out))
		dec.DisallowUnknownFields()
		var env contract.ErrorEnvelope
		if err := dec.Decode(&env); err != nil {
			t.Fatalf("mode %+v: envelope does not strictly decode: %v (%q)", m, err, out)
		}
		if env.Schema != contract.SchemaError {
			t.Errorf("schema = %q", env.Schema)
		}
		if env.Error.Code != contract.CodeBaseUnavailable || env.ExitCode != 8 || exit != 8 {
			t.Errorf("code %q exit_code %d returned %d", env.Error.Code, env.ExitCode, exit)
		}
		if env.Error.Message != "show: two\nlines <&> no base" {
			t.Errorf("message = %q", env.Error.Message)
		}
	}

	var buf bytes.Buffer
	if got := New(&buf, Mode{}).RenderEnvelope(nil); got != 0 || buf.Len() != 0 {
		t.Errorf("RenderEnvelope(nil) = %d, wrote %q", got, buf.String())
	}
}

// wp01Tree builds a small command tree mirroring the real machine surfaces.
func wp01Tree() map[string]*cobra.Command {
	noop := func(*cobra.Command, []string) {}
	root := &cobra.Command{Use: "ctx"}
	diff := &cobra.Command{Use: "diff", Run: noop}
	diff.Flags().Bool("json", false, "")
	show := &cobra.Command{Use: "show", Run: noop}
	show.Flags().Bool("json", true, "")
	plain := &cobra.Command{Use: "init", Run: noop}
	mcp := &cobra.Command{Use: "mcp", Run: noop}
	mcp.Flags().Bool("json", false, "")
	mcpStatus := &cobra.Command{Use: "status", Run: noop}
	mcpStatus.Flags().Bool("json", false, "")
	mcp.AddCommand(mcpStatus)
	capture := &cobra.Command{Use: "capture"}
	turn := &cobra.Command{Use: "turn", Run: noop}
	turn.Flags().Bool("json", false, "")
	capture.AddCommand(turn)
	completion := &cobra.Command{Use: "completion", Run: noop}
	complete := &cobra.Command{Use: "__complete", Run: noop}
	root.AddCommand(diff, show, plain, mcp, capture, completion, complete)
	return map[string]*cobra.Command{
		"root": root, "diff": diff, "show": show, "init": plain, "mcp": mcp,
		"mcp status": mcpStatus, "capture turn": turn, "completion": completion,
		"__complete": complete,
	}
}

func TestEnvelopeModeRules(t *testing.T) {
	type tc struct {
		name string
		cmd  string // key into wp01Tree, "" for a nil command
		set  bool   // set the json flag to true on the command
		args []string
		want bool
	}
	cases := []tc{
		{"json flag true", "diff", true, []string{"diff"}, true},
		{"json flag false", "diff", false, []string{"diff"}, false},
		{"raw --json", "diff", false, []string{"diff", "--bogus", "--json"}, true},
		{"raw --json=true", "diff", false, []string{"diff", "--json=true"}, true},
		{"raw --json=false", "diff", false, []string{"diff", "--json=false"}, false},
		{"raw --json after --", "diff", false, []string{"diff", "--", "--json"}, false},
		{"raw --json on a command without the flag", "init", false, []string{"init", "--json"}, true},
		{"command without a json flag", "init", false, []string{"init"}, false},
		{"show defaults to json", "show", false, []string{"show", "wiki/a.md", "--at", "base"}, true},
		{"root after an unknown command", "root", false, []string{"bogus", "--json"}, true},
		{"root without --json", "root", false, []string{"bogus"}, false},
		{"nil cmd raw --json", "", false, []string{"--json"}, true},
		{"nil cmd no args", "", false, nil, false},
		{"bare mcp never", "mcp", true, []string{"mcp", "--json"}, false},
		{"mcp status is not the stdio server", "mcp status", true, []string{"mcp", "status", "--json"}, true},
		{"capture turn never", "capture turn", true, []string{"capture", "turn", "--json"}, false},
		{"completion never", "completion", false, []string{"completion", "bash", "--json"}, false},
		{"__complete never", "__complete", false, []string{"__complete", "--json"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tree := wp01Tree()
			var cmd *cobra.Command
			if c.cmd != "" {
				cmd = tree[c.cmd]
				if c.set {
					if err := cmd.Flags().Set("json", "true"); err != nil {
						t.Fatal(err)
					}
				}
			}
			if got := EnvelopeMode(cmd, c.args); got != c.want {
				t.Errorf("EnvelopeMode(%q, %q) = %v, want %v", c.cmd, c.args, got, c.want)
			}
		})
	}
}
