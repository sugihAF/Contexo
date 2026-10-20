package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// update regenerates testdata/v1/manifest.json from the fixture files on disk:
//
//	go test ./internal/syncops/contract/ -run TestManifest -update
var update = flag.Bool("update", false, "rewrite testdata/v1/manifest.json from the fixture files on disk")

const (
	wp01FixtureDir   = "testdata/v1"
	wp01ManifestFile = "manifest.json"
	wp01CasesFile    = "classify.cases.json"

	// Manifest streams (E32).
	wp01StreamStdout = "stdout"
	wp01StreamStderr = "stderr"
	wp01StreamRaw    = "raw"
	wp01StreamData   = "data"
)

var (
	wp01TimeRE  = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`)
	wp01SHARE   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	wp01HashRE  = regexp.MustCompile(`^(raw-)?sha256:[0-9a-f]{64}$`)
	wp01EmailRE = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`)
)

// wp01TimeKeys and wp01SHAKeys are the JSON keys whose values must be contract
// times (E16: UTC, whole seconds) and full 40-hex SHAs in every fixture.
var wp01TimeKeys = map[string]bool{
	"generated_at": true, "fetched_at": true, "synced_at": true, "updated": true,
	"time": true, "updated_at": true, "started_at": true, "detected_at": true,
}

var wp01SHAKeys = map[string]bool{
	"sha": true, "server_sha": true, "base_sha": true, "new_head": true, "head": true,
	"last_pull_sha": true, "old_sha": true, "new_sha": true, "expected_sha": true,
}

// wp01ErrorRaiser is the manifest "command" of each error.<code>.json: a
// representative command that raises the code.
var wp01ErrorRaiser = map[ErrorCode]string{
	CodeInternal:          "status",
	CodeUsage:             "resolve",
	CodeInvalidPath:       "show",
	CodeNotAuthenticated:  "push",
	CodeAuthRejected:      "pull",
	CodeForbidden:         "push",
	CodeNotInitialized:    "status",
	CodeRepoNotConfigured: "push",
	CodeServerUnresolved:  "pull",
	CodeConflict:          "push",
	CodeStale:             "resolve",
	CodeNetwork:           "push",
	CodeServerError:       "pull",
	CodeRateLimited:       "pull",
	CodeRepoNotFound:      "show",
	CodePageNotFound:      "show",
	CodeBaseUnavailable:   "show",
	CodeSHANotFound:       "show",
	CodeDistillRequired:   "push",
	CodeNotResolvable:     "resolve",
	CodeServerUnsupported: "show",
	CodeNotImplemented:    "show",
	CodeQuotaExceeded:     "push",
	CodeBusy:              "push",
}

// wp01HTTPStatus is the http_status each error.<code>.json carries; codes not
// listed were not caused by an HTTP response and omit it.
var wp01HTTPStatus = map[ErrorCode]int{
	CodeAuthRejected:  401,
	CodeQuotaExceeded: 402,
	CodeForbidden:     403,
	CodeRepoNotFound:  404,
	CodePageNotFound:  404,
	CodeSHANotFound:   404,
	CodeConflict:      409,
	CodeRateLimited:   429,
	CodeServerError:   502,
}

// wp01DocCommands maps the stdout fixture prefix (the subcommand) to the
// schema its documents carry.
var wp01DocCommands = map[string]string{
	"version": SchemaVersion,
	"status":  SchemaStatus,
	"push":    SchemaPush,
	"pull":    SchemaPull,
	"resolve": SchemaResolve,
}

// wp01RequiredFixtures is the canonical v1 set (E31) apart from the 24
// error.<code>.json files, which TestEveryCodeHasFixture pins to codeTable.
var wp01RequiredFixtures = []string{
	"version.release.json", "version.dev.json", "version.missing-caps.json", "version.contract2.json",
	"status.all-states.json", "status.short.json", "status.captures-blocking.json",
	"status.offline-sidecar.json", "status.offline-nocache.json", "status.not-authenticated.json",
	"status.auth-rejected.json", "status.network-error.json", "status.old-server.json",
	"status.repo-missing.json", "status.url-mismatch.json", "status.empty.json",
	"push.ok.json", "push.dry-run.json", "push.partial-conflict.json",
	"push.partial-conflict.stderr.json", "push.nothing.json", "push.distill-bypassed.json",
	"pull.ok.json", "pull.skipped-and-adopted.json", "pull.case-collision.json", "pull.up-to-date.json",
	"resolve.ours.json", "resolve.theirs.json",
	"classify.cases.json",
	"raw/retry-policy.base.md", "raw/retry-policy.server.md",
}

// wp01ContractExamples are the literal examples on the wiki contract page
// (contexo-vscode-mvp-contract.md), keyed by the fixture that carries them.
// status.short.json additionally drops pages.etag from server.capabilities
// (E13); see TestFixturesMatchContractExamples.
var wp01ContractExamples = map[string]string{
	"status.short.json":                 `{"schema":"ctx.status.v1","generated_at":"2026-11-03T09:14:07Z","offline":false,"root":"/Users/alice/code/payments","contexo_dir":"/Users/alice/code/payments/.contexo","project":{"repo_id":"acme-payments","config_server_url":"https://api.contexo.live","dashboard_url":"https://app.contexo.live","dashboard_page_url_template":"https://app.contexo.live/repos/acme-payments/page/{path}","last_pull_sha":"c41b7e09d2f85a36e1b94c07d5a28f6e3b19c0d4"},"server":{"url":"https://api.contexo.live","url_source":"credentials","version":"0.6.0","capabilities_known":true,"capabilities":["pages.at","pages.etag","push.expect_absent","write.path_sha"]},"auth":{"state":"authenticated","credentials_source":"global","token_kind":"pat","user_name":"Alice Tan","user_email":"alice@acme.dev"},"remote":{"source":"live","fetched_at":"2026-11-03T09:14:07Z","repo_exists":true,"head":"f0e1d2c3b4a5968778695a4b3c2d1e0f9a8b7c6d","page_count":3},"counts":{"unchanged":0,"modified":1,"new":0,"incoming":0,"incoming_new":0,"conflict":1,"deleted_local":0,"invalid":1,"unverified":0,"ahead":1,"behind":0},"pages":[{"path":"wiki/concepts/retry-policy.md","state":"conflict","conflict_kind":"both_modified","type":"concept","slug":"retry-policy","title":"Retry policy","tags":["payments","retries"],"author":"alice","agent":"claude-code","updated":"2026-11-03T09:10:00Z","local":{"file":"wiki/concepts/retry-policy.md","hash":"sha256:9c8b7a6f5e4d3c2b1a09f8e7d6c5b4a39281706f5e4d3c2b1a0f9e8d7c6b5a4f"},"base":{"sha":"2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e","hash":"sha256:1e2d3c4b5a69788796a5b4c3d2e1f0a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3","synced_at":"2026-10-30T14:22:09Z","stale":false},"remote":{"sha":"f0e1d2c3b4a5968778695a4b3c2d1e0f9a8b7c6d","author":"Bob Lee","email":"bob@acme.dev","time":"2026-11-03T07:55:30Z","message":"Tighten retry ceilings"},"notes":[]},{"path":"wiki/entities/ledger-service.md","state":"modified","type":"entity","slug":"ledger-service","title":"Ledger service","tags":["ledger"],"author":"alice","updated":"2026-11-03T09:01:00Z","local":{"file":"wiki/entities/ledger-service.md","hash":"sha256:8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c9d8e7f"},"base":{"sha":"3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f","hash":"sha256:6f5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e","synced_at":"2026-10-31T11:12:13Z","stale":false},"remote":{"sha":"3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f","author":"Alice Tan","email":"alice@acme.dev","time":"2026-10-31T11:12:10Z","message":"ctx push (2 pages)"},"notes":[]},{"path":"wiki/entities/payment-gateway.md","state":"invalid","tags":[],"local":{"file":"wiki/entities/payment-gateway.md"},"base":{"sha":"4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f70","hash":"sha256:3b2a1f0e9d8c7b6a5f4e3d2c1b0a9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c3b2a","synced_at":"2026-10-25T15:00:00Z","stale":false},"remote":{"sha":"4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f70","author":"Alice Tan","email":"alice@acme.dev","time":"2026-10-25T14:59:58Z","message":"ctx push (1 pages)"},"invalid":{"stage":"parse","message":"page: parse frontmatter: yaml: line 4: did not find expected key"},"notes":[]}],"captures":{"pending":1,"fresh":1,"blocks_push":false,"sessions":[{"session_id":"5d0c6a3e-1f2b-4c7d-9e8f-0a1b2c3d4e5f","turns":14,"updated_at":"2026-11-03T09:12:40Z","fresh":true}]},"warnings":[]}`,
	"pull.skipped-and-adopted.json":     `{"schema":"ctx.pull.v1","server_url":"https://api.contexo.live","repo_id":"acme-payments","head":"e5f60a1b2c3d4e5f60718293a4b5c6d7e8f90a1b","written":[{"path":"wiki/analyses/q3-chargebacks.md","sha":"e5f60a1b2c3d4e5f60718293a4b5c6d7e8f90a1b","kind":"create"},{"path":"wiki/concepts/idempotency-keys.md","sha":"e5f60a1b2c3d4e5f60718293a4b5c6d7e8f90a1b","kind":"update"}],"adopted":[{"path":"wiki/concepts/ledger-reconciliation.md","sha":"b7c8d9e0f1a2b3c4d5e6f708192a3b4c5d6e7f80"}],"skipped":[{"path":"wiki/concepts/retry-policy.md","state":"conflict","reason":"conflict","server_sha":"f0e1d2c3b4a5968778695a4b3c2d1e0f9a8b7c6d"},{"path":"wiki/concepts/webhook-signing.md","state":"conflict","reason":"conflict","server_sha":"6a7b8c9d0e1f2a3b4c5d6e7f8091a2b3c4d5e6f7"},{"path":"wiki/entities/fraud-service.md","state":"deleted_local","reason":"deleted_local","server_sha":"0f1e2d3c4b5a69788796a5b4c3d2e1f0a9b8c7d6"},{"path":"wiki/entities/ledger-service.md","state":"modified","reason":"modified"}],"reindexed":true,"warnings":[]}`,
	"push.partial-conflict.json":        `{"schema":"ctx.push.v1","dry_run":false,"message":"Clarify ledger ownership","server_url":"https://api.contexo.live","repo_id":"acme-payments","new_head":"7a8b9c0d1e2f3a4b5c6d7e8f90a1b2c3d4e5f607","pushed":[{"path":"raw/sessions/2026-11-02-retry-budget.md","kind":"create","sha":"6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192"},{"path":"wiki/entities/ledger-service.md","kind":"update","sha":"7a8b9c0d1e2f3a4b5c6d7e8f90a1b2c3d4e5f607"}],"conflicts":[{"path":"wiki/entities/settlement-batch.md","kind":"both_modified","base_sha":"8192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4","server_sha":"92a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5"}],"skipped":[{"path":"wiki/concepts/retry-policy.md","state":"conflict","reason":"conflict","server_sha":"f0e1d2c3b4a5968778695a4b3c2d1e0f9a8b7c6d"},{"path":"wiki/entities/payment-gateway.md","state":"invalid","reason":"invalid"}],"distill":{"required":false,"bypassed":false},"warnings":[]}`,
	"push.partial-conflict.stderr.json": `{"schema":"ctx.error.v1","exit_code":6,"error":{"code":"conflict","message":"push: 1 conflict(s) remain; other pages were pushed","advice":["ctx resolve wiki/entities/settlement-batch.md --ours","ctx resolve wiki/entities/settlement-batch.md --theirs"],"retryable":false,"http_status":409,"details":{"paths":["wiki/entities/settlement-batch.md"]}}}`,
	"error.conflict.json":               `{"schema":"ctx.error.v1","exit_code":6,"error":{"code":"conflict","message":"push: 1 conflict(s) remain; other pages were pushed","advice":["ctx resolve wiki/entities/settlement-batch.md --ours","ctx resolve wiki/entities/settlement-batch.md --theirs"],"retryable":false,"http_status":409,"details":{"paths":["wiki/entities/settlement-batch.md"]}}}`,
	"error.not_initialized.json":        `{"schema":"ctx.error.v1","exit_code":5,"error":{"code":"not_initialized","message":"status: no .contexo/ directory in /Users/alice/code/scratch","advice":["ctx init"],"retryable":false}}`,
	"error.distill_required.json":       `{"schema":"ctx.error.v1","exit_code":9,"error":{"code":"distill_required","message":"push: 1 un-distilled agent session is pending; share these pages through your agent so the reasoning trail is captured","advice":[],"retryable":false,"details":{"pages":["wiki/concepts/settlement-windows.md"],"sessions":[{"session_id":"5d0c6a3e-1f2b-4c7d-9e8f-0a1b2c3d4e5f","turns":14,"updated_at":"2026-11-03T09:12:40Z","fresh":true}],"agent_prompt":"Push my pending Contexo changes with the ctx_push tool (wiki/concepts/settlement-windows.md). Distill the pending session into a source page first when ctx_push asks you to."}}}`,
	"resolve.ours.json":                 `{"schema":"ctx.resolve.v1","path":"wiki/concepts/retry-policy.md","strategy":"ours","previous_state":"conflict","state":"modified","base_sha":"f0e1d2c3b4a5968778695a4b3c2d1e0f9a8b7c6d","file_written":false,"warnings":[]}`,
	"resolve.theirs.json":               `{"schema":"ctx.resolve.v1","path":"wiki/concepts/retry-policy.md","strategy":"theirs","previous_state":"conflict","state":"unchanged","base_sha":"f0e1d2c3b4a5968778695a4b3c2d1e0f9a8b7c6d","file_written":true,"backup_file":".sync/backup/wiki/concepts/retry-policy.md.20261103T091530Z","warnings":[]}`,
}

// ---------------------------------------------------------------------------
// manifest (E32)

type wp01Manifest struct {
	ContractVersion int                 `json:"contract_version"`
	Fixtures        []wp01ManifestEntry `json:"fixtures"`
}

type wp01ManifestEntry struct {
	File     string  `json:"file"`
	Schema   *string `json:"schema"`
	Command  *string `json:"command"`
	Stream   string  `json:"stream"`
	ExitCode *int    `json:"exit_code"`
	SHA256   string  `json:"sha256"`
}

func wp01Ptr[T any](v T) *T { return &v }

func wp01ReadFixture(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(wp01FixtureDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return data
}

// wp01FixtureFiles lists every file under testdata/v1 except manifest.json,
// as sorted '/'-separated paths relative to testdata/v1.
func wp01FixtureFiles(t *testing.T) []string {
	t.Helper()
	files := []string{}
	err := filepath.WalkDir(wp01FixtureDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(wp01FixtureDir, p)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); rel != wp01ManifestFile {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", wp01FixtureDir, err)
	}
	sort.Strings(files)
	return files
}

func wp01CommandOf(file string) string {
	cmd, _, _ := strings.Cut(file, ".")
	return cmd
}

// wp01BuildManifest derives manifest.json from the files on disk.
func wp01BuildManifest(t *testing.T) wp01Manifest {
	t.Helper()
	files := wp01FixtureFiles(t)
	onDisk := map[string]bool{}
	for _, f := range files {
		onDisk[f] = true
	}
	m := wp01Manifest{ContractVersion: ContractVersion, Fixtures: []wp01ManifestEntry{}}
	for _, f := range files {
		data := wp01ReadFixture(t, f)
		sum := sha256.Sum256(data)
		e := wp01ManifestEntry{File: f, SHA256: hex.EncodeToString(sum[:])}
		switch {
		case strings.HasPrefix(f, "raw/") && strings.HasSuffix(f, ".md"):
			e.Stream, e.Command, e.ExitCode = wp01StreamRaw, wp01Ptr("show"), wp01Ptr(ExitOK)
		case f == wp01CasesFile:
			e.Stream = wp01StreamData
		case strings.Contains(f, "/"):
			t.Fatalf("unexpected fixture file %s", f)
		case strings.HasPrefix(f, "error.") || strings.HasSuffix(f, ".stderr.json"):
			env := wp01DecodeEnvelope(t, f, data)
			cmd := wp01CommandOf(f)
			if strings.HasPrefix(f, "error.") {
				raiser, ok := wp01ErrorRaiser[env.Error.Code]
				if !ok {
					t.Fatalf("%s: no representative command for code %q", f, env.Error.Code)
				}
				cmd = raiser
			}
			e.Stream = wp01StreamStderr
			e.Schema = wp01Ptr(env.Schema)
			e.Command = wp01Ptr(cmd)
			e.ExitCode = wp01Ptr(ExitCodeFor(env.Error.Code))
		case strings.HasSuffix(f, ".json"):
			var head struct {
				Schema string `json:"schema"`
			}
			if err := json.Unmarshal(data, &head); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			exit := ExitOK
			if sib := strings.TrimSuffix(f, ".json") + ".stderr.json"; onDisk[sib] {
				exit = ExitCodeFor(wp01DecodeEnvelope(t, sib, wp01ReadFixture(t, sib)).Error.Code)
			}
			e.Stream = wp01StreamStdout
			e.Schema = wp01Ptr(head.Schema)
			e.Command = wp01Ptr(wp01CommandOf(f))
			e.ExitCode = wp01Ptr(exit)
		default:
			t.Fatalf("unexpected fixture file %s", f)
		}
		m.Fixtures = append(m.Fixtures, e)
	}
	return m
}

func wp01EncodeManifest(t *testing.T, m wp01Manifest) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteDoc(&buf, m); err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	return buf.Bytes()
}

func wp01LoadManifest(t *testing.T) wp01Manifest {
	t.Helper()
	var m wp01Manifest
	wp01StrictDecode(t, wp01ManifestFile, wp01ReadFixture(t, wp01ManifestFile), &m)
	return m
}

// ---------------------------------------------------------------------------
// decoding helpers

func wp01Strict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("strict decode: %w", err)
	}
	if err := dec.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing data after the JSON document")
	}
	return nil
}

func wp01StrictDecode(t *testing.T, name string, data []byte, v any) {
	t.Helper()
	if err := wp01Strict(data, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

type wp01EnvelopeBody struct {
	Code       ErrorCode       `json:"code"`
	Message    string          `json:"message"`
	Advice     []string        `json:"advice"`
	Retryable  bool            `json:"retryable"`
	HTTPStatus int             `json:"http_status,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
}

type wp01Envelope struct {
	Schema   string           `json:"schema"`
	ExitCode int              `json:"exit_code"`
	Error    wp01EnvelopeBody `json:"error"`
}

// wp01DecodeEnvelope strictly decodes a stderr fixture, which must be
// exactly one '\n'-terminated line. details stays raw.
func wp01DecodeEnvelope(t *testing.T, name string, data []byte) wp01Envelope {
	t.Helper()
	if !bytes.HasSuffix(data, []byte("\n")) || bytes.Count(data, []byte("\n")) != 1 {
		t.Fatalf("%s: a stderr fixture must be exactly one '\\n'-terminated line", name)
	}
	var env wp01Envelope
	wp01StrictDecode(t, name, data, &env)
	return env
}

func wp01Compact(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		t.Fatalf("%s: compact: %v", name, err)
	}
	return buf.Bytes()
}

func wp01LoadStatus(t *testing.T, name string) StatusDoc {
	t.Helper()
	var d StatusDoc
	wp01StrictDecode(t, name, wp01ReadFixture(t, name), &d)
	return d
}

// ---------------------------------------------------------------------------
// round trip

func TestFixturesRoundTrip(t *testing.T) {
	m := wp01LoadManifest(t)
	n := 0
	for _, e := range m.Fixtures {
		if e.Stream != wp01StreamStdout && e.Stream != wp01StreamStderr {
			continue
		}
		n++
		t.Run(e.File, func(t *testing.T) {
			data := wp01ReadFixture(t, e.File)
			var got []byte
			if e.Stream == wp01StreamStderr {
				got = wp01RoundTripEnvelope(t, e.File, data)
			} else {
				if e.Schema == nil {
					t.Fatalf("stdout fixture without a schema")
				}
				got = wp01RoundTripDoc(t, e.File, *e.Schema, data)
			}
			if !bytes.Equal(wp01Compact(t, e.File, got), wp01Compact(t, e.File, data)) {
				t.Fatalf("re-encoded document differs after compaction\n got: %s\nwant: %s",
					wp01Compact(t, e.File, got), wp01Compact(t, e.File, data))
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("fixture is not byte-identical to WriteDoc/WriteEnvelope output\n got: %q\nwant: %q", got, data)
			}
		})
	}
	if n == 0 {
		t.Fatal("manifest lists no stdout/stderr fixtures")
	}
}

// wp01RoundTripEnvelope decodes an error envelope strictly (details through
// NewDetails) and rebuilds it with WriteEnvelope.
func wp01RoundTripEnvelope(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	env := wp01DecodeEnvelope(t, name, data)
	if env.Schema != SchemaError {
		t.Fatalf("schema = %q, want %q", env.Schema, SchemaError)
	}
	if !KnownCode(env.Error.Code) {
		t.Fatalf("unknown error code %q", env.Error.Code)
	}
	if env.Error.Advice == nil {
		t.Fatalf("error.advice must be [] rather than null or absent")
	}
	var details any
	proto := NewDetails(env.Error.Code)
	switch {
	case proto == nil && len(env.Error.Details) != 0:
		t.Fatalf("code %q carries no details, fixture has %s", env.Error.Code, env.Error.Details)
	case proto != nil && len(env.Error.Details) != 0:
		wp01StrictDecode(t, name+" details", env.Error.Details, proto)
		details = proto
	}
	var buf bytes.Buffer
	err := WriteEnvelope(&buf, &Error{
		Code:       env.Error.Code,
		Message:    env.Error.Message,
		Advice:     env.Error.Advice,
		HTTPStatus: env.Error.HTTPStatus,
		Details:    details,
	})
	if err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	return buf.Bytes()
}

// wp01RoundTripDoc decodes a stdout document strictly into the type its
// schema selects and rebuilds it with WriteDoc.
func wp01RoundTripDoc(t *testing.T, name, schema string, data []byte) []byte {
	t.Helper()
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		t.Fatalf("%v", err)
	}
	if head.Schema != schema {
		t.Fatalf("document schema %q != manifest schema %q", head.Schema, schema)
	}
	var doc any
	switch schema {
	case SchemaVersion:
		doc = &VersionDoc{}
	case SchemaStatus:
		doc = &StatusDoc{}
	case SchemaPush:
		doc = &PushDoc{}
	case SchemaPull:
		doc = &PullDoc{}
	case SchemaResolve:
		doc = &ResolveDoc{}
	default:
		t.Fatalf("no document type for schema %q", schema)
	}
	wp01StrictDecode(t, name, data, doc)
	if sd, ok := doc.(*StatusDoc); ok && sd.Remote.Error != nil && sd.Remote.Error.Details != nil {
		// ErrorBody.Details is `any`: re-type it so field order is kept.
		raw, err := json.Marshal(sd.Remote.Error.Details)
		if err != nil {
			t.Fatalf("%v", err)
		}
		proto := NewDetails(sd.Remote.Error.Code)
		if proto == nil {
			t.Fatalf("remote.error code %q carries no details", sd.Remote.Error.Code)
		}
		wp01StrictDecode(t, name+" remote.error.details", raw, proto)
		sd.Remote.Error.Details = proto
	}
	var buf bytes.Buffer
	if err := WriteDoc(&buf, doc); err != nil {
		t.Fatalf("WriteDoc: %v", err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// manifest

func TestManifest(t *testing.T) {
	built := wp01BuildManifest(t)
	want := wp01EncodeManifest(t, built)
	mpath := filepath.Join(wp01FixtureDir, wp01ManifestFile)
	if *update {
		if err := os.WriteFile(mpath, want, 0o644); err != nil {
			t.Fatalf("write %s: %v", mpath, err)
		}
		t.Logf("rewrote %s (%d fixtures)", mpath, len(built.Fixtures))
	}

	m := wp01LoadManifest(t)
	if m.ContractVersion != ContractVersion {
		t.Errorf("contract_version = %d, want %d", m.ContractVersion, ContractVersion)
	}
	if len(m.Fixtures) == 0 {
		t.Fatal("manifest lists no fixtures")
	}
	listed := []string{}
	for i, e := range m.Fixtures {
		if i > 0 && m.Fixtures[i-1].File >= e.File {
			t.Errorf("entries not sorted by file (or duplicated): %q then %q", m.Fixtures[i-1].File, e.File)
		}
		listed = append(listed, e.File)
		data, err := os.ReadFile(filepath.Join(wp01FixtureDir, filepath.FromSlash(e.File)))
		if err != nil {
			t.Errorf("%s: %v", e.File, err)
			continue
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); e.SHA256 != got {
			t.Errorf("%s: sha256 %s, file hashes to %s", e.File, e.SHA256, got)
		}
		wp01CheckManifestEntry(t, e, data)
	}
	if disk := wp01FixtureFiles(t); !reflect.DeepEqual(listed, disk) {
		t.Errorf("manifest files differ from files on disk\nmanifest: %v\n    disk: %v", listed, disk)
	}
	if got := wp01ReadFixture(t, wp01ManifestFile); !bytes.Equal(got, want) {
		t.Errorf("manifest.json is out of date; regenerate with: go test ./internal/syncops/contract/ -run TestManifest -update")
	}
}

func wp01CheckManifestEntry(t *testing.T, e wp01ManifestEntry, data []byte) {
	t.Helper()
	switch e.Stream {
	case wp01StreamData:
		if e.File != wp01CasesFile {
			t.Errorf("%s: only %s is a data fixture", e.File, wp01CasesFile)
		}
		if e.Schema != nil || e.Command != nil || e.ExitCode != nil {
			t.Errorf("%s: a data entry has null schema, command and exit_code", e.File)
		}
	case wp01StreamRaw:
		if !strings.HasPrefix(e.File, "raw/") || !strings.HasSuffix(e.File, ".md") {
			t.Errorf("%s: raw fixtures live under raw/ as .md", e.File)
		}
		if e.Schema != nil || e.Command == nil || *e.Command != "show" || e.ExitCode == nil || *e.ExitCode != ExitOK {
			t.Errorf("%s: a raw entry has schema null, command \"show\" and exit_code 0", e.File)
		}
	case wp01StreamStdout:
		if e.Schema == nil || e.Command == nil || e.ExitCode == nil {
			t.Errorf("%s: a stdout entry has schema, command and exit_code", e.File)
			return
		}
		if want, ok := wp01DocCommands[*e.Command]; !ok || want != *e.Schema {
			t.Errorf("%s: command %q / schema %q", e.File, *e.Command, *e.Schema)
		}
		var head struct {
			Schema string `json:"schema"`
		}
		if err := json.Unmarshal(data, &head); err != nil || head.Schema != *e.Schema {
			t.Errorf("%s: document schema %q (err %v) != manifest schema %q", e.File, head.Schema, err, *e.Schema)
		}
		want := ExitOK
		if e.File == "push.partial-conflict.json" {
			want = ExitConflict
		}
		if *e.ExitCode != want {
			t.Errorf("%s: exit_code %d, want %d", e.File, *e.ExitCode, want)
		}
	case wp01StreamStderr:
		if e.Schema == nil || *e.Schema != SchemaError || e.Command == nil || *e.Command == "" || e.ExitCode == nil {
			t.Errorf("%s: a stderr entry has schema %q, a command and an exit_code", e.File, SchemaError)
			return
		}
		env := wp01DecodeEnvelope(t, e.File, data)
		if *e.ExitCode != ExitCodeFor(env.Error.Code) || *e.ExitCode != env.ExitCode {
			t.Errorf("%s: exit_code %d, envelope %d, ExitCodeFor %d", e.File, *e.ExitCode, env.ExitCode, ExitCodeFor(env.Error.Code))
		}
		if strings.HasPrefix(e.File, "error.") && *e.Command != wp01ErrorRaiser[env.Error.Code] {
			t.Errorf("%s: command %q, want %q", e.File, *e.Command, wp01ErrorRaiser[env.Error.Code])
		}
	default:
		t.Errorf("%s: stream %q is not stdout|stderr|raw|data", e.File, e.Stream)
	}
}

// TestFixtureSet pins the exact canonical file set (E31): nothing missing and
// nothing extra, so the vendored copy (WP-0.I) is exactly this set.
func TestFixtureSet(t *testing.T) {
	want := append([]string{}, wp01RequiredFixtures...)
	for code := range codeTable {
		want = append(want, "error."+string(code)+".json")
	}
	sort.Strings(want)
	if got := wp01FixtureFiles(t); !reflect.DeepEqual(got, want) {
		t.Errorf("fixture set differs\n got: %v\nwant: %v", got, want)
	}
}

// TestFixtureGitattributes keeps fixture bytes (and so their sha256) stable
// on Windows checkouts: no CRLF conversion under testdata/.
func TestFixtureGitattributes(t *testing.T) {
	got, err := os.ReadFile(filepath.Join("testdata", ".gitattributes"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if string(got) != "* -text\n" {
		t.Errorf("testdata/.gitattributes = %q, want %q", got, "* -text\n")
	}
}

// ---------------------------------------------------------------------------
// classify.cases.json

type wp01ClassifyCases struct {
	ContractVersion int                `json:"contract_version"`
	Cases           []wp01ClassifyCase `json:"cases"`
}

type wp01ClassifyCase struct {
	ID       string         `json:"id"`
	Desc     string         `json:"desc"`
	Input    ClassifyInput  `json:"input"`
	Expected ClassifyResult `json:"expected"`
	SCMGroup *string        `json:"scm_group"`
}

// wp01RowShape pins what each decision-table row exercises: the three input
// kinds and, where the row depends on them, hL vs hB and sr vs sb.
type wp01RowShape struct {
	L   LocalKind
	B   BaseKind
	R   RemoteKind
	Rel string
}

var wp01RowShapes = map[string]wp01RowShape{
	"r01":  {LocalInvalid, BaseValid, RemotePresent, "sr==sb"},
	"r02":  {LocalInvalid, BaseValid, RemotePresent, "sr!=sb"},
	"r03":  {LocalAbsent, BaseValid, RemotePresent, "sr==sb"},
	"r03b": {LocalAbsent, BaseStale, RemoteUnknown, ""},
	"r04":  {LocalAbsent, BaseValid, RemotePresent, "sr!=sb"},
	"r05":  {LocalAbsent, BaseValid, RemoteAbsent, ""},
	"r06":  {LocalAbsent, BaseNone, RemotePresent, ""},
	"r07":  {LocalAbsent, BaseNone, RemoteAbsent, ""},
	"r08":  {LocalValid, BaseNone, RemotePresent, ""},
	"r09":  {LocalValid, BaseNone, RemoteAbsent, ""},
	"r10":  {LocalValid, BaseNone, RemoteUnknown, ""},
	"r11":  {LocalValid, BaseStale, RemoteAbsent, ""},
	"r12":  {LocalValid, BaseStale, RemotePresent, "sr!=sb"},
	"r12b": {LocalValid, BaseStale, RemoteUnknown, ""},
	"r13":  {LocalValid, BaseValid, RemoteAbsent, "hL==hB"},
	"r14":  {LocalValid, BaseValid, RemotePresent, "hL==hB sr==sb"},
	"r15":  {LocalValid, BaseValid, RemotePresent, "hL!=hB sr==sb"},
	"r16":  {LocalValid, BaseValid, RemoteUnknown, "hL==hB"},
	"r17":  {LocalValid, BaseValid, RemoteUnknown, "hL!=hB"},
	"r18":  {LocalValid, BaseValid, RemotePresent, "hL==hB sr!=sb"},
	"r19":  {LocalValid, BaseValid, RemotePresent, "hL!=hB sr!=sb"},
}

func wp01LoadClassifyCases(t *testing.T) (wp01ClassifyCases, []byte) {
	t.Helper()
	data := wp01ReadFixture(t, wp01CasesFile)
	var cs wp01ClassifyCases
	wp01StrictDecode(t, wp01CasesFile, data, &cs)
	return cs, data
}

// wp01ShapeOf derives a row shape from a case input and checks that each
// side carries exactly the fields its kind implies.
func wp01ShapeOf(in ClassifyInput) (wp01RowShape, error) {
	s := wp01RowShape{L: in.Local.Kind, B: in.Base.Kind, R: in.Remote.Kind}
	if (in.Local.Kind == LocalValid) != (in.Local.Hash != "") {
		return s, fmt.Errorf("local.hash must be set iff local is valid")
	}
	if (in.Base.Kind != BaseNone) != (in.Base.SHA != "") {
		return s, fmt.Errorf("base.sha must be set iff base is valid or stale")
	}
	if (in.Base.Kind == BaseValid) != (in.Base.Hash != "") {
		return s, fmt.Errorf("base.hash must be set iff base is valid")
	}
	if (in.Remote.Kind == RemotePresent) != (in.Remote.SHA != "") {
		return s, fmt.Errorf("remote.sha must be set iff remote is present")
	}
	var rel []string
	if in.Local.Kind == LocalValid && in.Base.Kind == BaseValid {
		if in.Local.Hash == in.Base.Hash {
			rel = append(rel, "hL==hB")
		} else {
			rel = append(rel, "hL!=hB")
		}
	}
	if in.Remote.Kind == RemotePresent && in.Base.Kind != BaseNone {
		if in.Remote.SHA == in.Base.SHA {
			rel = append(rel, "sr==sb")
		} else {
			rel = append(rel, "sr!=sb")
		}
	}
	s.Rel = strings.Join(rel, " ")
	return s, nil
}

func TestClassifyCases(t *testing.T) {
	cs, data := wp01LoadClassifyCases(t)
	if cs.ContractVersion != ContractVersion {
		t.Errorf("contract_version = %d, want %d", cs.ContractVersion, ContractVersion)
	}
	if len(cs.Cases) != 21 {
		t.Fatalf("%d cases, want 21 (r01..r19 + r03b + r12b)", len(cs.Cases))
	}
	seen := map[string]bool{}
	for _, c := range cs.Cases {
		if seen[c.ID] {
			t.Errorf("duplicate case id %q", c.ID)
		}
		seen[c.ID] = true
		want, ok := wp01RowShapes[c.ID]
		if !ok {
			t.Errorf("unexpected case id %q", c.ID)
			continue
		}
		if c.Desc == "" {
			t.Errorf("%s: empty desc", c.ID)
		}
		shape, err := wp01ShapeOf(c.Input)
		if err != nil {
			t.Errorf("%s: %v", c.ID, err)
		} else if shape != want {
			t.Errorf("%s: input shape %+v, want %+v", c.ID, shape, want)
		}
		if c.Expected.Notes == nil {
			t.Errorf("%s: expected.notes must be [] rather than null or absent", c.ID)
		}
		if got := Classify(c.Input); !reflect.DeepEqual(got, c.Expected) {
			t.Errorf("%s: Classify = %+v, want %+v", c.ID, got, c.Expected)
		}
		group := SCMGroup(c.Expected.State)
		switch {
		case group == "" && c.SCMGroup != nil:
			t.Errorf("%s: scm_group = %q, want null (SCMGroup(%q) is \"\")", c.ID, *c.SCMGroup, c.Expected.State)
		case group != "" && (c.SCMGroup == nil || *c.SCMGroup != group):
			t.Errorf("%s: scm_group = %v, want %q", c.ID, c.SCMGroup, group)
		}
	}
	for id := range wp01RowShapes {
		if !seen[id] {
			t.Errorf("missing case %s", id)
		}
	}
	var buf bytes.Buffer
	if err := WriteDoc(&buf, cs); err != nil {
		t.Fatalf("WriteDoc: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("%s is not in canonical WriteDoc form", wp01CasesFile)
	}
}

func TestClassifySidesMatchesClassify(t *testing.T) {
	cs, _ := wp01LoadClassifyCases(t)
	for _, c := range cs.Cases {
		in := c.Input
		got, err := ClassifySides(
			Side{Kind: string(in.Local.Kind), Hash: in.Local.Hash},
			Side{Kind: string(in.Base.Kind), SHA: in.Base.SHA, Hash: in.Base.Hash},
			Side{Kind: string(in.Remote.Kind), SHA: in.Remote.SHA},
		)
		if err != nil {
			t.Errorf("%s: ClassifySides: %v", c.ID, err)
			continue
		}
		if want := Classify(in); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: ClassifySides = %+v, Classify = %+v", c.ID, got, want)
		}
	}

	const h = "sha256:5f0c6b1a9e2d4c3b8a7f6e5d4c3b2a1908f7e6d5c4b3a2918f7e6d5c4b3a2910"
	const sha = "8d1f2e3c4b5a69788796a5b4c3d2e1f0a9b8c7d6"
	good := [3]Side{{Kind: "valid", Hash: h}, {Kind: "valid", SHA: sha, Hash: h}, {Kind: "present", SHA: sha}}
	if _, err := ClassifySides(good[0], good[1], good[2]); err != nil {
		t.Fatalf("known kinds: %v", err)
	}
	bad := [3][]string{
		{"", "bogus", "Valid", "present", "none"}, // local
		{"", "bogus", "absent", "present"},        // base
		{"", "bogus", "none", "valid"},            // remote
	}
	for side, kinds := range bad {
		for _, k := range kinds {
			sides := good
			sides[side].Kind = k
			if _, err := ClassifySides(sides[0], sides[1], sides[2]); err == nil {
				t.Errorf("side %d kind %q: want an error", side, k)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// error fixtures

func TestEveryCodeHasFixture(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(wp01FixtureDir, "error.*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 24 {
		t.Errorf("%d error.<code>.json files, want 24", len(files))
	}
	if len(codeTable) != 24 {
		t.Errorf("codeTable has %d codes, want 24", len(codeTable))
	}
	seen := map[ErrorCode]bool{}
	for _, f := range files {
		name := filepath.Base(f)
		code := ErrorCode(strings.TrimSuffix(strings.TrimPrefix(name, "error."), ".json"))
		if !KnownCode(code) {
			t.Errorf("%s: %q is not a known code", name, code)
			continue
		}
		seen[code] = true
		env := wp01DecodeEnvelope(t, name, wp01ReadFixture(t, name))
		if env.Error.Code != code {
			t.Errorf("%s: error.code = %q", name, env.Error.Code)
		}
		if env.ExitCode != ExitCodeFor(code) {
			t.Errorf("%s: exit_code = %d, want ExitCodeFor = %d", name, env.ExitCode, ExitCodeFor(code))
		}
		if env.Error.Retryable != Retryable(code) {
			t.Errorf("%s: retryable = %v, want %v", name, env.Error.Retryable, Retryable(code))
		}
		if hasDetails, wantDetails := len(env.Error.Details) != 0, NewDetails(code) != nil; hasDetails != wantDetails {
			t.Errorf("%s: details present = %v, want %v", name, hasDetails, wantDetails)
		}
		if env.Error.HTTPStatus != wp01HTTPStatus[code] {
			t.Errorf("%s: http_status = %d, want %d", name, env.Error.HTTPStatus, wp01HTTPStatus[code])
		}
		if env.Error.Message == "" || strings.ContainsRune(env.Error.Message, '\x1b') {
			t.Errorf("%s: message must be a non-empty sentence without ANSI", name)
		}
		if env.Error.Advice == nil {
			t.Errorf("%s: advice must be [] rather than null or absent", name)
		}
	}
	for code := range codeTable {
		if !seen[code] {
			t.Errorf("no fixture error.%s.json", code)
		}
	}
	if got := ExitCodeFor(CodeNotImplemented); got != 10 {
		t.Errorf("ExitCodeFor(not_implemented) = %d, want 10", got)
	}
	env := wp01DecodeEnvelope(t, "error.not_implemented.json", wp01ReadFixture(t, "error.not_implemented.json"))
	if env.ExitCode != 10 || !reflect.DeepEqual(env.Error.Advice, []string{"ctx update"}) {
		t.Errorf("error.not_implemented.json: exit_code %d advice %q, want 10 [ctx update]", env.ExitCode, env.Error.Advice)
	}
}

// ---------------------------------------------------------------------------
// version fixtures

func TestCapabilities(t *testing.T) {
	if len(Capabilities) != 11 {
		t.Errorf("len(Capabilities) = %d, want 11", len(Capabilities))
	}
	if !KnownCapability(CapRepair) {
		t.Error("KnownCapability(repair) = false")
	}
	if KnownCapability("pages.at") {
		t.Error("KnownCapability(pages.at) = true; pages.at is a server capability")
	}
	all := append(append([]Capability{}, Capabilities...), OptionalCapabilities...)
	load := func(name string) VersionDoc {
		var d VersionDoc
		wp01StrictDecode(t, name, wp01ReadFixture(t, name), &d)
		return d
	}
	for _, name := range []string{"version.release.json", "version.dev.json"} {
		d := load(name)
		if !reflect.DeepEqual(d.Capabilities, all) {
			t.Errorf("%s: capabilities = %v, want Capabilities ++ OptionalCapabilities = %v", name, d.Capabilities, all)
		}
		if d.ContractVersion != ContractVersion {
			t.Errorf("%s: contract_version = %d", name, d.ContractVersion)
		}
	}
	if d := load("version.release.json"); d.DevBuild || d.Version == "dev" {
		t.Errorf("version.release.json: version %q dev_build %v", d.Version, d.DevBuild)
	}
	if d := load("version.dev.json"); !d.DevBuild || d.Version != "dev" {
		t.Errorf("version.dev.json: version %q dev_build %v", d.Version, d.DevBuild)
	}

	d := load("version.missing-caps.json")
	if d.ContractVersion != ContractVersion {
		t.Errorf("version.missing-caps.json: contract_version = %d", d.ContractVersion)
	}
	order := map[Capability]int{}
	for i, c := range all {
		order[c] = i
	}
	have := map[Capability]bool{}
	for i, c := range d.Capabilities {
		if !KnownCapability(c) || have[c] {
			t.Errorf("version.missing-caps.json: unknown or duplicate capability %q", c)
		}
		if i > 0 && order[d.Capabilities[i-1]] >= order[c] {
			t.Errorf("version.missing-caps.json: %q listed out of contract order", c)
		}
		have[c] = true
	}
	var missing []Capability
	for _, c := range Capabilities {
		if !have[c] {
			missing = append(missing, c)
		}
	}
	if want := []Capability{CapPushDistillGuard, CapResolve}; !reflect.DeepEqual(missing, want) {
		t.Errorf("version.missing-caps.json lacks %v, want exactly %v", missing, want)
	}

	if d := load("version.contract2.json"); d.ContractVersion != 2 {
		t.Errorf("version.contract2.json: contract_version = %d, want 2", d.ContractVersion)
	}
}

func TestTimeOfWholeSeconds(t *testing.T) {
	in := time.Date(2026, 11, 3, 16, 14, 7, 123456789, time.FixedZone("WIB", 7*3600))
	doc := struct {
		At    Time  `json:"at"`
		AtPtr *Time `json:"at_ptr"`
	}{TimeOf(in), TimePtr(in)}
	var buf bytes.Buffer
	if err := WriteDoc(&buf, doc); err != nil {
		t.Fatalf("WriteDoc: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, buf.Bytes())
	}
	const want = "2026-11-03T09:14:07Z"
	for _, k := range []string{"at", "at_ptr"} {
		if got[k] != want || !wp01TimeRE.MatchString(got[k]) {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if tt := TimeOf(in); tt.Location() != time.UTC || tt.Nanosecond() != 0 {
		t.Errorf("TimeOf = %v, want UTC whole seconds", tt)
	}
}

// ---------------------------------------------------------------------------
// fixture hygiene (errata)

func wp01WalkJSON(v any, at string, visit func(at, key string, val any)) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := at + "." + k
			visit(p, k, x[k])
			wp01WalkJSON(x[k], p, visit)
		}
	case []any:
		for i, e := range x {
			wp01WalkJSON(e, fmt.Sprintf("%s[%d]", at, i), visit)
		}
	}
}

func TestFixturesHonourErrata(t *testing.T) {
	files := append(wp01FixtureFiles(t), wp01ManifestFile)
	for _, f := range files {
		data := wp01ReadFixture(t, f)
		if bytes.Contains(data, []byte("pages.etag")) {
			t.Errorf("%s: lists pages.etag, which is reserved (E13)", f)
		}
		if bytes.Contains(data, []byte("ctxp_")) {
			t.Errorf("%s: contains a ctxp_ token string", f)
		}
		for _, em := range wp01EmailRE.FindAll(data, -1) {
			if !bytes.HasSuffix(em, []byte("@acme.dev")) {
				t.Errorf("%s: email %q is not @acme.dev", f, em)
			}
		}
		if !strings.HasSuffix(f, ".json") {
			continue
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		wp01WalkJSON(v, "$", func(at, key string, val any) {
			s, isStr := val.(string)
			switch {
			case wp01TimeKeys[key]:
				if !isStr || !wp01TimeRE.MatchString(s) {
					t.Errorf("%s: %s = %v is not a UTC whole-second time", f, at, val)
				}
			case wp01SHAKeys[key]:
				if !isStr || !wp01SHARE.MatchString(s) {
					t.Errorf("%s: %s = %v is not a full 40-hex sha", f, at, val)
				}
			case key == "hash":
				if !isStr || !wp01HashRE.MatchString(s) {
					t.Errorf("%s: %s = %v is not sha256:<64 hex>", f, at, val)
				}
			case key == "source":
				if s == string(RemoteCache) {
					t.Errorf("%s: %s = cache; v1 never emits remote.source cache (E8)", f, at)
				}
			}
		})
	}
	d := wp01LoadStatus(t, "status.not-authenticated.json")
	if d.Remote.Error == nil || d.Remote.Error.Code != CodeNotAuthenticated {
		t.Errorf("status.not-authenticated.json: remote.error = %+v, want code not_authenticated (E22)", d.Remote.Error)
	}
}

// TestFixturesMatchContractExamples pins the fixtures that carry a literal
// example of the wiki contract page to that example.
func TestFixturesMatchContractExamples(t *testing.T) {
	names := make([]string, 0, len(wp01ContractExamples))
	for name := range wp01ContractExamples {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		want := wp01ContractExamples[name]
		if name == "status.short.json" {
			// E13: pages.etag is reserved and removed from every fixture.
			trimmed := strings.Replace(want, `"pages.at","pages.etag",`, `"pages.at",`, 1)
			if trimmed == want {
				t.Fatalf("status example no longer lists pages.etag; update this test")
			}
			want = trimmed
		}
		data := wp01ReadFixture(t, name)
		got := string(wp01Compact(t, name, data))
		if strings.HasSuffix(name, ".stderr.json") || strings.HasPrefix(name, "error.") {
			got = strings.TrimSuffix(string(data), "\n")
		}
		if got != want {
			t.Errorf("%s differs from the contract example\n got: %s\nwant: %s", name, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// status fixtures

func wp01HasNote(p PageEntry, n NoteCode) bool {
	for _, x := range p.Notes {
		if x == n {
			return true
		}
	}
	return false
}

func wp01HasWarning(d []Warning, c WarningCode) bool {
	for _, w := range d {
		if w.Code == c {
			return true
		}
	}
	return false
}

// wp01PushableDistillPage reports whether a modified or new page is a
// concept or analysis (the distill-guard predicate's page half).
func wp01PushableDistillPage(d StatusDoc) bool {
	for _, p := range d.Pages {
		if (p.State == StateModified || p.State == StateNew) && (p.Type == "concept" || p.Type == "analysis") {
			return true
		}
	}
	return false
}

func wp01Tally(pages []PageEntry) StateCounts {
	var c StateCounts
	for _, p := range pages {
		switch p.State {
		case StateUnchanged:
			c.Unchanged++
		case StateModified:
			c.Modified++
		case StateNew:
			c.New++
		case StateIncoming:
			c.Incoming++
		case StateIncomingNew:
			c.IncomingNew++
		case StateConflict:
			c.Conflict++
		case StateDeletedLocal:
			c.DeletedLocal++
		case StateInvalid:
			c.Invalid++
		case StateUnverified:
			c.Unverified++
		}
	}
	c.Ahead = c.Modified + c.New
	c.Behind = c.Incoming + c.IncomingNew
	return c
}

var wp01ServerCaps = []ServerCapability{SrvPagesAt, SrvExpectAbsent, SrvWritePathSHA}

func TestStatusFixturesConsistent(t *testing.T) {
	m := wp01LoadManifest(t)
	n := 0
	for _, e := range m.Fixtures {
		if e.Schema == nil || *e.Schema != SchemaStatus {
			continue
		}
		n++
		t.Run(e.File, func(t *testing.T) {
			wp01CheckStatusDoc(t, wp01LoadStatus(t, e.File))
		})
	}
	if n != 12 {
		t.Errorf("%d ctx.status.v1 fixtures, want 12", n)
	}
}

func wp01CheckStatusDoc(t *testing.T, d StatusDoc) {
	t.Helper()
	if d.Schema != SchemaStatus || d.Pages == nil || d.Warnings == nil {
		t.Errorf("schema %q, pages/warnings must be arrays", d.Schema)
	}
	if got, want := d.Counts, wp01Tally(d.Pages); got != want {
		t.Errorf("counts = %+v, want the per-state tally %+v", got, want)
	}
	if d.Counts.Ahead != d.Counts.Modified+d.Counts.New || d.Counts.Behind != d.Counts.Incoming+d.Counts.IncomingNew {
		t.Errorf("ahead/behind inconsistent: %+v", d.Counts)
	}
	for i, p := range d.Pages {
		w := p.Path
		if i > 0 && d.Pages[i-1].Path >= p.Path {
			t.Errorf("pages not sorted by path (byte order) or duplicated: %q then %q", d.Pages[i-1].Path, p.Path)
		}
		switch p.State {
		case StateUnchanged, StateModified, StateNew, StateIncoming, StateIncomingNew,
			StateConflict, StateDeletedLocal, StateInvalid, StateUnverified:
		default:
			t.Errorf("%s: unknown state %q", w, p.State)
		}
		if (p.State == StateConflict) != (p.ConflictKind != "") {
			t.Errorf("%s: conflict_kind %q must be present iff state is conflict (state %q)", w, p.ConflictKind, p.State)
		}
		if p.ConflictKind != "" && p.ConflictKind != ConflictBothModified && p.ConflictKind != ConflictBothAdded {
			t.Errorf("%s: conflict_kind %q", w, p.ConflictKind)
		}
		if (p.State == StateInvalid) != (p.Invalid != nil) {
			t.Errorf("%s: invalid must be present iff state is invalid (state %q)", w, p.State)
		}
		if p.Invalid != nil && (p.Invalid.Stage == InvalidPathMismatch) != (p.Invalid.ExpectedPath != "") {
			t.Errorf("%s: expected_path must be present iff stage is path_mismatch", w)
		}
		if p.Tags == nil || p.Notes == nil {
			t.Errorf("%s: tags and notes must be arrays", w)
		}
		hasLocal := p.State != StateIncomingNew && p.State != StateDeletedLocal
		if (p.Local != nil) != hasLocal {
			t.Errorf("%s: local side present = %v for state %q", w, p.Local != nil, p.State)
		}
		if p.Local != nil {
			if p.Local.File != p.Path {
				t.Errorf("%s: local.file %q != path", w, p.Local.File)
			}
			if (p.State == StateInvalid) == (p.Local.Hash != "") {
				t.Errorf("%s: local.hash must be present iff the file is valid", w)
			}
		}
		if p.Base != nil && (p.Base.Stale != (p.Base.Hash == "") || p.Base.Stale != (p.Base.SyncedAt == nil)) {
			t.Errorf("%s: a stale base carries only sha; a valid base carries sha, hash and synced_at", w)
		}
		switch p.State {
		case StateIncomingNew:
			if p.Base != nil || p.Remote == nil {
				t.Errorf("%s: incoming_new has no base and a remote side", w)
			}
		case StateDeletedLocal:
			if p.Base == nil {
				t.Errorf("%s: deleted_local has a base", w)
			}
		case StateUnverified:
			if p.Base == nil || !p.Base.Stale {
				t.Errorf("%s: unverified has a stale base", w)
			}
		case StateIncoming:
			if p.Remote == nil || p.Base == nil || p.Remote.SHA == p.Base.SHA {
				t.Errorf("%s: incoming has a remote sha past the base sha", w)
			}
		}
		if d.Remote.Source == RemoteNone && p.Remote != nil &&
			(p.Remote.Author != "" || p.Remote.Email != "" || p.Remote.Time != nil || p.Remote.Message != "") {
			t.Errorf("%s: without a live listing a remote side comes from a sidecar and carries only sha (E9)", w)
		}
		collides := false
		for _, q := range d.Pages {
			if q.Path != p.Path && strings.EqualFold(q.Path, p.Path) {
				collides = true
			}
		}
		if collides != wp01HasNote(p, NoteCaseCollision) {
			t.Errorf("%s: note case_collision iff another path differs only by case", w)
		}
		if collides && !wp01HasWarning(d.Warnings, WarnCaseCollision) {
			t.Errorf("%s: case collision without warning case_collision", w)
		}
	}

	switch d.Remote.Source {
	case RemoteLive:
		if d.Offline || d.Remote.FetchedAt == nil || d.Remote.Error != nil {
			t.Errorf("a live remote is online, has fetched_at and no error")
		}
	case RemoteNone:
		if d.Remote.FetchedAt != nil || d.Remote.RepoExists != nil || d.Remote.Head != "" || d.Remote.PageCount != 0 {
			t.Errorf("remote.source none has no fetched_at, repo_exists, head or page_count")
		}
	default:
		t.Errorf("remote.source %q: v1 emits only live or none (E8)", d.Remote.Source)
	}
	if d.Offline && (d.Remote.Source != RemoteNone || d.Remote.FetchedAt != nil || d.Remote.Error != nil) {
		t.Errorf("an offline doc has remote.source none, no fetched_at and no remote.error")
	}
	if e := d.Remote.Error; e != nil {
		if !KnownCode(e.Code) || e.Advice == nil || e.Retryable != Retryable(e.Code) || e.Message == "" {
			t.Errorf("remote.error %+v is not a well-formed error body", e)
		}
		if NewDetails(e.Code) == nil && e.Details != nil {
			t.Errorf("remote.error code %q carries no details", e.Code)
		}
	}

	if d.Server.Capabilities == nil {
		t.Errorf("server.capabilities must be an array")
	}
	if !d.Server.CapabilitiesKnown && (len(d.Server.Capabilities) != 0 || d.Server.Version != "") {
		t.Errorf("unknown server capabilities: capabilities [] and no version")
	}
	if d.Offline && d.Server.CapabilitiesKnown {
		t.Errorf("an offline status never contacts the server, so capabilities are not known")
	}
	have := map[ServerCapability]bool{}
	for _, c := range d.Server.Capabilities {
		switch {
		case have[c]:
			t.Errorf("server.capabilities %v: %q listed twice", d.Server.Capabilities, c)
		case c != SrvPagesAt && c != SrvExpectAbsent && c != SrvWritePathSHA:
			t.Errorf("server.capabilities %v: v1 servers advertise only %v", d.Server.Capabilities, wp01ServerCaps)
		}
		have[c] = true
	}
	outdated := false
	for _, c := range wp01ServerCaps {
		if !have[c] {
			outdated = true
		}
	}
	if want := d.Server.CapabilitiesKnown && outdated; wp01HasWarning(d.Warnings, WarnServerOutdated) != want {
		t.Errorf("warning server_outdated present = %v, want %v (E13)", !want, want)
	}

	if d.Project.DashboardURL == "" && d.Project.DashboardPageURLTemplate != "" {
		t.Errorf("dashboard_page_url_template must be omitted when dashboard_url is \"\" (E23)")
	}
	if tpl := d.Project.DashboardPageURLTemplate; tpl != "" && !strings.Contains(tpl, "{path}") {
		t.Errorf("dashboard_page_url_template %q lacks {path}", tpl)
	}

	switch d.Auth.State {
	case AuthNotAuthenticated:
		if d.Auth.CredentialsSource != "" || d.Auth.TokenKind != "" || d.Auth.UserName != "" || d.Auth.UserEmail != "" {
			t.Errorf("not_authenticated carries no credentials fields")
		}
	case AuthAuthenticated, AuthUnchecked, AuthRejected:
		if d.Auth.CredentialsSource == "" || d.Auth.TokenKind == "" {
			t.Errorf("auth.state %q has credentials_source and token_kind", d.Auth.State)
		}
	default:
		t.Errorf("unknown auth.state %q", d.Auth.State)
	}
	if d.Auth.State == AuthAuthenticated && d.Remote.Source != RemoteLive {
		t.Errorf("authenticated means the server accepted the token in this run")
	}
	if d.Offline && d.Auth.State != AuthUnchecked && d.Auth.State != AuthNotAuthenticated {
		t.Errorf("offline auth.state is unchecked or not_authenticated, got %q", d.Auth.State)
	}

	c := d.Captures
	if c.Sessions == nil || c.Fresh < 0 || c.Fresh > c.Pending || len(c.Sessions) > c.Pending {
		t.Errorf("captures inconsistent: %+v", c)
	}
	if c.BlocksPush && (c.Fresh == 0 || !wp01PushableDistillPage(d)) {
		t.Errorf("blocks_push needs a fresh session and a modified/new concept or analysis page")
	}
	for i := 1; i < len(c.Sessions); i++ {
		if c.Sessions[i-1].UpdatedAt.Before(c.Sessions[i].UpdatedAt) {
			t.Errorf("capture sessions are not most recent first")
		}
	}
	for _, w := range d.Warnings {
		if w.Paths == nil || w.Message == "" {
			t.Errorf("warning %q: paths must be an array and message non-empty", w.Code)
		}
		if w.Code == WarnRemoteStale {
			t.Errorf("warning remote_stale is reserved; v1 never emits it (E8)")
		}
	}
}

// TestStatusFixtureScenarios pins what each status fixture exists to show
// (contract "Shared fixtures" + E13/E22/E23/E31).
func TestStatusFixtureScenarios(t *testing.T) {
	t.Run("all-states", func(t *testing.T) {
		d := wp01LoadStatus(t, "status.all-states.json")
		c := d.Counts
		for name, n := range map[string]int{
			"unchanged": c.Unchanged, "modified": c.Modified, "new": c.New, "incoming": c.Incoming,
			"incoming_new": c.IncomingNew, "conflict": c.Conflict, "deleted_local": c.DeletedLocal,
			"invalid": c.Invalid, "unverified": c.Unverified,
		} {
			if n == 0 {
				t.Errorf("no %s page", name)
			}
		}
		var bothModified, bothAdded, parse, mismatch, newWithBase, remoteChanged bool
		for _, p := range d.Pages {
			bothModified = bothModified || p.ConflictKind == ConflictBothModified
			bothAdded = bothAdded || p.ConflictKind == ConflictBothAdded
			if p.Invalid != nil {
				parse = parse || p.Invalid.Stage == InvalidParse
				mismatch = mismatch || (p.Invalid.Stage == InvalidPathMismatch && p.Invalid.ExpectedPath != "")
			}
			newWithBase = newWithBase || (p.State == StateNew && p.Base != nil && wp01HasNote(p, NoteMissingOnServer))
			remoteChanged = remoteChanged ||
				((p.State == StateInvalid || p.State == StateDeletedLocal) && wp01HasNote(p, NoteRemoteChanged))
		}
		if !bothModified || !bothAdded || !parse || !mismatch || !newWithBase || !remoteChanged {
			t.Errorf("both_modified %v both_added %v parse %v path_mismatch %v new+base+missing_on_server %v remote_changed %v",
				bothModified, bothAdded, parse, mismatch, newWithBase, remoteChanged)
		}
		if d.Offline || d.Remote.Source != RemoteLive {
			t.Errorf("all-states is an online, live status")
		}
		if d.Captures.Fresh == 0 || d.Captures.BlocksPush || wp01PushableDistillPage(d) {
			t.Errorf("captures must be fresh but not blocking (no modified/new concept or analysis page)")
		}
	})
	t.Run("captures-blocking", func(t *testing.T) {
		d := wp01LoadStatus(t, "status.captures-blocking.json")
		modifiedConcept := false
		for _, p := range d.Pages {
			modifiedConcept = modifiedConcept || (p.State == StateModified && p.Type == "concept")
		}
		if !d.Captures.BlocksPush || d.Captures.Fresh == 0 || !modifiedConcept {
			t.Errorf("blocks_push %v fresh %d modified concept %v", d.Captures.BlocksPush, d.Captures.Fresh, modifiedConcept)
		}
	})
	t.Run("offline-sidecar", func(t *testing.T) {
		d := wp01LoadStatus(t, "status.offline-sidecar.json")
		if !d.Offline || d.Remote.Source != RemoteNone || d.Auth.State != AuthUnchecked ||
			d.Server.CapabilitiesKnown || len(d.Server.Capabilities) != 0 {
			t.Errorf("E31a: offline, remote.source none, auth unchecked, capabilities unknown and []")
		}
		conflicts := 0
		for _, p := range d.Pages {
			switch {
			case p.State == StateConflict:
				conflicts++
				if p.Remote == nil || p.Remote.SHA == "" {
					t.Errorf("%s: the sidecar conflict has remote {sha}", p.Path)
				}
			case p.State == StateIncoming || p.State == StateIncomingNew:
				t.Errorf("%s: no incoming pages offline", p.Path)
			case p.Remote != nil:
				t.Errorf("%s: only the sidecar conflict has a remote side", p.Path)
			}
		}
		if conflicts != 1 || wp01HasWarning(d.Warnings, WarnRemoteStale) {
			t.Errorf("E31a: exactly one conflict (%d) and no remote_stale", conflicts)
		}
	})
	t.Run("offline-nocache", func(t *testing.T) {
		d := wp01LoadStatus(t, "status.offline-nocache.json")
		if !d.Offline || d.Remote.Source != RemoteNone || d.Server.CapabilitiesKnown {
			t.Errorf("E31c: offline, remote.source none, capabilities unknown")
		}
		allowed := map[PageState]bool{StateNew: true, StateModified: true, StateUnchanged: true,
			StateInvalid: true, StateDeletedLocal: true, StateUnverified: true}
		for _, p := range d.Pages {
			if !allowed[p.State] || p.Remote != nil {
				t.Errorf("%s: state %q / remote side %v not possible without sidecars (E31c)", p.Path, p.State, p.Remote)
			}
		}
	})
	t.Run("not-authenticated", func(t *testing.T) {
		d := wp01LoadStatus(t, "status.not-authenticated.json")
		e := d.Remote.Error
		if d.Auth.State != AuthNotAuthenticated || d.Remote.Source != RemoteNone || e == nil ||
			e.Code != CodeNotAuthenticated || !reflect.DeepEqual(e.Advice, []string{"ctx auth login"}) ||
			e.Retryable || e.HTTPStatus != 0 {
			t.Errorf("E22: auth not_authenticated, source none, remote.error not_authenticated [ctx auth login]; got %+v %+v", d.Auth, e)
		}
	})
	t.Run("auth-rejected", func(t *testing.T) {
		d := wp01LoadStatus(t, "status.auth-rejected.json")
		e := d.Remote.Error
		if d.Auth.State != AuthRejected || d.Remote.Source != RemoteNone || e == nil ||
			e.Code != CodeAuthRejected || e.HTTPStatus != 401 {
			t.Errorf("auth rejected, source none, remote.error auth_rejected 401; got %+v %+v", d.Auth, e)
		}
	})
	t.Run("network-error", func(t *testing.T) {
		d := wp01LoadStatus(t, "status.network-error.json")
		e := d.Remote.Error
		if d.Auth.State != AuthUnchecked || d.Remote.Source != RemoteNone || e == nil ||
			e.Code != CodeNetwork || !e.Retryable {
			t.Errorf("auth unchecked, source none, remote.error network retryable; got %+v %+v", d.Auth, e)
		}
	})
	t.Run("old-server", func(t *testing.T) {
		d := wp01LoadStatus(t, "status.old-server.json")
		if d.Server.Version != "" || !d.Server.CapabilitiesKnown || len(d.Server.Capabilities) != 0 ||
			!wp01HasWarning(d.Warnings, WarnServerOutdated) || !wp01HasWarning(d.Warnings, WarnBaseRepairSkipped) ||
			d.Counts.Unverified == 0 {
			t.Errorf("no version, capabilities known and [], server_outdated + base_repair_skipped, unverified pages")
		}
	})
	t.Run("repo-missing", func(t *testing.T) {
		d := wp01LoadStatus(t, "status.repo-missing.json")
		if d.Remote.RepoExists == nil || *d.Remote.RepoExists || len(d.Pages) == 0 {
			t.Errorf("remote.repo_exists false with local pages")
		}
		for _, p := range d.Pages {
			if p.State != StateNew {
				t.Errorf("%s: state %q, every page is new when the repo is missing", p.Path, p.State)
			}
		}
	})
	t.Run("url-mismatch", func(t *testing.T) {
		d := wp01LoadStatus(t, "status.url-mismatch.json")
		if d.Project.ConfigServerURL == "" || d.Project.ConfigServerURL == d.Server.URL ||
			d.Server.URLSource != URLFromCredentials || !wp01HasWarning(d.Warnings, WarnServerURLMismatch) {
			t.Errorf("config_server_url %q vs server.url %q (%s), warning server_url_mismatch",
				d.Project.ConfigServerURL, d.Server.URL, d.Server.URLSource)
		}
	})
	t.Run("empty", func(t *testing.T) {
		d := wp01LoadStatus(t, "status.empty.json")
		c := d.Captures
		if len(d.Pages) != 0 || d.Counts != (StateCounts{}) || c.Pending != 0 || c.Fresh != 0 ||
			c.BlocksPush || len(c.Sessions) != 0 {
			t.Errorf("pages [], all counts 0, captures empty")
		}
	})
}

// ---------------------------------------------------------------------------
// push / pull / resolve fixtures

func wp01LoadDoc(t *testing.T, name string, v any) {
	t.Helper()
	wp01StrictDecode(t, name, wp01ReadFixture(t, name), v)
}

func TestOpFixtures(t *testing.T) {
	m := wp01LoadManifest(t)
	for _, e := range m.Fixtures {
		if e.Stream != wp01StreamStdout || e.Schema == nil {
			continue
		}
		switch *e.Schema {
		case SchemaPush:
			var d PushDoc
			wp01LoadDoc(t, e.File, &d)
			if d.Pushed == nil || d.Conflicts == nil || d.Skipped == nil || d.Warnings == nil {
				t.Errorf("%s: arrays must not be null", e.File)
			}
			if d.Message == "" || d.ServerURL == "" || d.RepoID == "" {
				t.Errorf("%s: message, server_url and repo_id are set", e.File)
			}
			for _, p := range d.Pushed {
				if d.DryRun == (p.SHA != "") {
					t.Errorf("%s: %s: pushed sha must be present iff not a dry run", e.File, p.Path)
				}
			}
			if (d.DryRun || len(d.Pushed) == 0) != (d.NewHead == "") {
				t.Errorf("%s: new_head is absent iff dry run or nothing pushed", e.File)
			}
			if d.DryRun && len(d.Conflicts) != 0 {
				t.Errorf("%s: a dry run has no conflicts", e.File)
			}
			for _, c := range d.Conflicts {
				if (c.Kind == ConflictBothAdded) != (c.BaseSHA == "") {
					t.Errorf("%s: %s: base_sha is absent iff both_added", e.File, c.Path)
				}
			}
		case SchemaPull:
			var d PullDoc
			wp01LoadDoc(t, e.File, &d)
			if d.Written == nil || d.Adopted == nil || d.Skipped == nil || d.Warnings == nil {
				t.Errorf("%s: arrays must not be null", e.File)
			}
			for _, p := range d.Written {
				if p.Kind == "" {
					t.Errorf("%s: written %s has a kind", e.File, p.Path)
				}
			}
			for _, p := range d.Adopted {
				if p.Kind != "" {
					t.Errorf("%s: adopted %s has no kind", e.File, p.Path)
				}
			}
			if d.Reindexed && len(d.Written) == 0 {
				t.Errorf("%s: reindexed without written pages", e.File)
			}
		case SchemaResolve:
			var d ResolveDoc
			wp01LoadDoc(t, e.File, &d)
			if d.Warnings == nil || (d.Strategy == ResolveOurs && d.BackupFile != "") {
				t.Errorf("%s: warnings array; backup_file only for theirs", e.File)
			}
		}
	}

	t.Run("push.ok", func(t *testing.T) {
		var d PushDoc
		wp01LoadDoc(t, "push.ok.json", &d)
		kinds := map[WriteKind]bool{}
		for _, p := range d.Pushed {
			kinds[p.Kind] = true
		}
		if d.DryRun || len(d.Pushed) != 2 || !kinds[WriteCreate] || !kinds[WriteUpdate] || d.NewHead == "" ||
			len(d.Conflicts) != 0 || len(d.Skipped) != 0 || d.Distill != (DistillCheck{}) {
			t.Errorf("two pushed pages (create + update), new_head, no conflicts/skips, distill {false,false}")
		}
	})
	t.Run("push.dry-run", func(t *testing.T) {
		var d PushDoc
		wp01LoadDoc(t, "push.dry-run.json", &d)
		blocking := map[SkipReason]bool{SkipConflict: true, SkipInvalid: true, SkipDeletedLocal: true, SkipUnverified: true}
		if !d.DryRun || d.NewHead != "" || len(d.Pushed) == 0 || len(d.Conflicts) != 0 ||
			len(d.Skipped) != 1 || !blocking[d.Skipped[0].Reason] {
			t.Errorf("dry run, no new_head, pushed without sha, conflicts [], one blocking skip")
		}
	})
	t.Run("push.nothing", func(t *testing.T) {
		var d PushDoc
		wp01LoadDoc(t, "push.nothing.json", &d)
		if len(d.Pushed) != 0 || d.NewHead != "" || len(d.Skipped) != 1 ||
			d.Skipped[0].State != StateUnchanged || d.Skipped[0].Reason != SkipUnchanged {
			t.Errorf("pushed [], skipped [{state unchanged, reason unchanged}], no new_head")
		}
	})
	t.Run("push.distill-bypassed", func(t *testing.T) {
		var d PushDoc
		wp01LoadDoc(t, "push.distill-bypassed.json", &d)
		if d.Distill != (DistillCheck{Required: true, Bypassed: true}) {
			t.Errorf("distill = %+v, want {required true, bypassed true}", d.Distill)
		}
	})
	t.Run("pull.ok", func(t *testing.T) {
		var d PullDoc
		wp01LoadDoc(t, "pull.ok.json", &d)
		kinds := map[WriteKind]bool{}
		for _, p := range d.Written {
			kinds[p.Kind] = true
		}
		if len(d.Written) != 2 || !kinds[WriteCreate] || !kinds[WriteUpdate] || len(d.Adopted) != 0 ||
			!d.Reindexed || d.Head == "" {
			t.Errorf("written create + update, adopted [], reindexed, head")
		}
	})
	t.Run("pull.case-collision", func(t *testing.T) {
		var d PullDoc
		wp01LoadDoc(t, "pull.case-collision.json", &d)
		var paths []string
		for _, s := range d.Skipped {
			if s.Reason == SkipCaseCollision {
				paths = append(paths, s.Path)
			}
		}
		if len(paths) != 2 || paths[0] == paths[1] || !strings.EqualFold(paths[0], paths[1]) {
			t.Fatalf("two case-variant paths skipped with case_collision, got %v", paths)
		}
		found := false
		for _, w := range d.Warnings {
			if w.Code == WarnCaseCollision {
				found = reflect.DeepEqual(w.Paths, paths)
			}
		}
		if !found {
			t.Errorf("warning case_collision listing %v", paths)
		}
	})
	t.Run("pull.up-to-date", func(t *testing.T) {
		var d PullDoc
		wp01LoadDoc(t, "pull.up-to-date.json", &d)
		if len(d.Written)+len(d.Adopted)+len(d.Skipped)+len(d.Warnings) != 0 || d.Reindexed || d.Head == "" {
			t.Errorf("all arrays empty, reindexed false, head present")
		}
	})
}
