package e2e

// Golden contract fixtures: deterministic states driven through the real
// ctx, captured, redacted (golden_redact_test.go) and compared with, or with
// -update written to, testdata/contract/ plus manifest.json. WP-2.I
// validates them against the contexo-vscode schemas; WP-3.1 vendors them.
//
// The builders live in golden_status_test.go, golden_ops_test.go and
// golden_errors_test.go. Each group runs ONE ctx command and captures one or
// two of its streams.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	gosync "sync"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

var update = flag.Bool("update", false, "rewrite internal/e2e/testdata/contract (TestGolden)")

const (
	goldenDir      = "testdata/contract"
	goldenManifest = "manifest.json"
	generatedBy    = "go test ./internal/e2e -run TestGolden -update"
	// gitFixedDate pins every server commit time (the in-process gitstore
	// runs git with the test process env), so listing times tie the same
	// way on every run.
	gitFixedDate = "@1767225600 +0000"
)

// Golden streams (E32).
const (
	streamStdout = "stdout"
	streamStderr = "stderr"
	streamRaw    = "raw"
)

// goldenFile is one fixture captured from a group's run.
type goldenFile struct {
	Name   string // file name under testdata/contract
	Stream string // stdout, stderr or raw
	Schema string // ctx.*.v1; "" for raw (null in the manifest)
}

// goldenGroup builds a deterministic state and runs the one ctx command
// whose output becomes Files.
type goldenGroup struct {
	Files []goldenFile
	// Skip, when it returns non-empty, skips the group (e.g. version.dev.json
	// against a stamped CONTEXO_E2E_CTX binary).
	Skip func() string
	Run  func(g *goldenEnv) ctxResult
}

// goldenGroups is every golden, in a stable order.
func goldenGroups() []goldenGroup {
	var gs []goldenGroup
	gs = append(gs, goldenStatusGroups()...)
	gs = append(gs, goldenOpGroups()...)
	gs = append(gs, goldenErrorGroups()...)
	return gs
}

// stdoutDoc and stderrEnv are goldenFile shorthands.
func stdoutDoc(name, schema string) goldenFile {
	return goldenFile{Name: name, Stream: streamStdout, Schema: schema}
}

func stderrEnv(name string) goldenFile {
	return goldenFile{Name: name, Stream: streamStderr, Schema: contract.SchemaError}
}

func rawFile(name string) goldenFile { return goldenFile{Name: name, Stream: streamRaw} }

// goldenEnv is a group's environment: it registers every root, HOME and
// server URL with the group's redactor.
type goldenEnv struct {
	t   *testing.T
	red *redactor
}

func newGoldenEnv(t *testing.T) *goldenEnv {
	t.Helper()
	t.Setenv("GIT_AUTHOR_DATE", gitFixedDate)
	t.Setenv("GIT_COMMITTER_DATE", gitFixedDate)
	return &goldenEnv{t: t, red: newRedactor()}
}

// server starts a current testkit server and registers its URLs.
func (g *goldenEnv) server() *e2eServer {
	g.t.Helper()
	s := startServer(g.t)
	g.url(s.URL)
	g.url(s.TK.URL)
	return s
}

// oldServer starts a pre-v1 testkit server and registers its URLs.
func (g *goldenEnv) oldServer() *e2eServer {
	g.t.Helper()
	s := startOldServer(g.t)
	g.url(s.URL)
	g.url(s.TK.URL)
	return s
}

// user creates a project user (newUser) written as /e2e/<name>.
func (g *goldenEnv) user(srv *e2eServer, name string, opts ...userOpt) *e2eUser {
	g.t.Helper()
	u := newUser(g.t, srv, name, opts...)
	g.red.addRoot(u.Root, name)
	g.red.addRoot(u.Home, name+"-home")
	return u
}

// bareUser creates a user with no project (newBareUser).
func (g *goldenEnv) bareUser(name string) *e2eUser {
	g.t.Helper()
	u := newBareUser(g.t, name)
	g.red.addRoot(u.Root, name)
	g.red.addRoot(u.Home, name+"-home")
	return u
}

// url registers a server URL (a synthetic upstream or a closed port).
func (g *goldenEnv) url(u string) { g.red.addURL(u) }

// goldenEntry is one manifest.json fixture entry (E32).
type goldenEntry struct {
	File     string   `json:"file"`
	Schema   *string  `json:"schema"`
	Command  []string `json:"command"`
	Stream   string   `json:"stream"`
	ExitCode int      `json:"exit_code"`
	SHA256   string   `json:"sha256"`
}

type goldenManifestDoc struct {
	ContractVersion int           `json:"contract_version"`
	GeneratedBy     string        `json:"generated_by"`
	Fixtures        []goldenEntry `json:"fixtures"`
}

var (
	goldenMu      gosync.Mutex
	goldenEntries = map[string]goldenEntry{}
)

var reErrorGolden = regexp.MustCompile(`^error\.([a-z_]+)\.json$`)

func TestGolden(t *testing.T) {
	requireCtx(t)
	groups := goldenGroups()
	ran := 0
	for _, grp := range groups {
		grp := grp
		t.Run(grp.Files[0].Name, func(t *testing.T) {
			if grp.Skip != nil {
				if why := grp.Skip(); why != "" {
					t.Skip(why)
				}
			}
			g := newGoldenEnv(t)
			r := grp.Run(g)
			goldenCheckRun(t, grp, r)
			for _, f := range grp.Files {
				goldenWriteOrCompare(t, g, f, r)
			}
			ran++
		})
	}
	if t.Failed() {
		return
	}
	if *update {
		goldenWriteManifest(t, groups, ran == len(groups))
		return
	}
	goldenCompareManifest(t)
}

// goldenCheckRun checks the raw (unredacted) run against the contract
// before anything is written.
func goldenCheckRun(t *testing.T, grp goldenGroup, r ctxResult) {
	t.Helper()
	has := map[string]bool{}
	for _, f := range grp.Files {
		has[f.Stream] = true
	}
	switch {
	case has[streamStdout] && has[streamStderr]:
		doc, _ := assertPushConflict(t, r)
		_ = doc
	case has[streamStderr]:
		e := assertFail(t, r, goldenCodeOf(t, grp))
		_ = e
	case has[streamStdout]:
		assertOK(t, r, nil)
	case has[streamRaw]:
		if r.Exit != 0 || len(r.Stderr) != 0 {
			t.Fatalf("raw output: want exit 0 and empty stderr\n%s", r)
		}
	}
	for _, f := range grp.Files {
		if f.Stream != streamStdout {
			continue
		}
		var head struct {
			Schema string `json:"schema"`
		}
		if err := json.Unmarshal(r.Stdout, &head); err != nil || head.Schema != f.Schema {
			t.Fatalf("%s: stdout schema %q, want %q\n%s", f.Name, head.Schema, f.Schema, r)
		}
	}
}

// goldenCodeOf is the error code a stderr group must produce: from its
// error.<code>.json name, otherwise conflict.
func goldenCodeOf(t *testing.T, grp goldenGroup) contract.ErrorCode {
	t.Helper()
	for _, f := range grp.Files {
		if m := reErrorGolden.FindStringSubmatch(f.Name); m != nil {
			code := contract.ErrorCode(m[1])
			if !contract.KnownCode(code) {
				t.Fatalf("%s: %q is not a contract error code", f.Name, code)
			}
			return code
		}
	}
	return contract.CodeConflict
}

// goldenCommand is the manifest command: argv without --root <dir>.
func goldenCommand(args []string) []string {
	out := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" && i+1 < len(args) {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func goldenWriteOrCompare(t *testing.T, g *goldenEnv, f goldenFile, r ctxResult) {
	t.Helper()
	var data []byte
	switch f.Stream {
	case streamStdout, streamRaw:
		data = r.Stdout
	case streamStderr:
		data = r.Stderr
	default:
		t.Fatalf("%s: unknown stream %q", f.Name, f.Stream)
	}
	sess := g.red.session()
	red := sess.redact(data)
	cmdJSON, err := json.Marshal(goldenCommand(r.Args))
	if err != nil {
		t.Fatal(err)
	}
	var cmd []string
	if err := json.Unmarshal(sess.redact(cmdJSON), &cmd); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(red)
	e := goldenEntry{File: f.Name, Command: cmd, Stream: f.Stream, ExitCode: r.Exit, SHA256: hex.EncodeToString(sum[:])}
	if f.Schema != "" {
		s := f.Schema
		e.Schema = &s
	}
	goldenMu.Lock()
	goldenEntries[f.Name] = e
	goldenMu.Unlock()

	path := filepath.Join(goldenDir, f.Name)
	if *update {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, red, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (regenerate with: make test-e2e-update)", f.Name, err)
	}
	if !bytes.Equal(red, want) {
		t.Errorf("%s differs from the golden (regenerate with make test-e2e-update only for a legitimate shape change):\n%s",
			f.Name, goldenDiff(want, red))
	}
}

// goldenDiff names the first differing line.
func goldenDiff(want, got []byte) string {
	wl := strings.Split(string(want), "\n")
	gl := strings.Split(string(got), "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("line %d:\n golden: %s\n    got: %s", i+1, w, g)
		}
	}
	return "(differs only in trailing bytes)"
}

// goldenWriteManifest writes manifest.json. A partial run (-run
// TestGolden/<file>) merges its entries into the existing manifest; a full
// run also removes goldens that no group produces any more.
func goldenWriteManifest(t *testing.T, groups []goldenGroup, full bool) {
	t.Helper()
	entries := map[string]goldenEntry{}
	if old, err := goldenLoadManifest(); err == nil {
		for _, e := range old.Fixtures {
			entries[e.File] = e
		}
	}
	known := map[string]bool{}
	for _, grp := range groups {
		for _, f := range grp.Files {
			known[f.Name] = true
		}
	}
	goldenMu.Lock()
	for k, v := range goldenEntries {
		entries[k] = v
	}
	goldenMu.Unlock()
	for name := range entries {
		if !known[name] {
			delete(entries, name)
		}
	}
	if full {
		files, err := goldenDiskFiles()
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			if !known[name] {
				if err := os.Remove(filepath.Join(goldenDir, name)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	doc := goldenManifestDoc{ContractVersion: contract.ContractVersion, GeneratedBy: generatedBy, Fixtures: []goldenEntry{}}
	for _, e := range entries {
		doc.Fixtures = append(doc.Fixtures, e)
	}
	sort.Slice(doc.Fixtures, func(i, j int) bool { return doc.Fixtures[i].File < doc.Fixtures[j].File })
	var buf bytes.Buffer
	if err := contract.WriteDoc(&buf, doc); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goldenDir, goldenManifest), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// goldenCompareManifest checks the produced entries against manifest.json.
func goldenCompareManifest(t *testing.T) {
	t.Helper()
	m, err := goldenLoadManifest()
	if err != nil {
		t.Fatalf("manifest: %v (regenerate with: make test-e2e-update)", err)
	}
	byFile := map[string]goldenEntry{}
	for _, e := range m.Fixtures {
		byFile[e.File] = e
	}
	goldenMu.Lock()
	defer goldenMu.Unlock()
	for name, got := range goldenEntries {
		want, ok := byFile[name]
		if !ok {
			t.Errorf("manifest has no entry for %s", name)
			continue
		}
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(want)
		if !bytes.Equal(gj, wj) {
			t.Errorf("manifest entry for %s drifted:\n golden: %s\n    got: %s", name, wj, gj)
		}
	}
}

func goldenLoadManifest() (*goldenManifestDoc, error) {
	b, err := os.ReadFile(filepath.Join(goldenDir, goldenManifest))
	if err != nil {
		return nil, err
	}
	var m goldenManifestDoc
	if err := strictDecode(b, &m); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	return &m, nil
}

// goldenDiskFiles lists the fixture files (everything but the manifest).
func goldenDiskFiles() ([]string, error) {
	var names []string
	err := filepath.WalkDir(goldenDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(goldenDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel != goldenManifest {
			names = append(names, rel)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	sort.Strings(names)
	return names, err
}

// ---------------------------------------------------------------- meta tests

// TestGoldenManifest: the files on disk, the manifest entries and the golden
// groups match one-to-one, and every sha256 matches.
func TestGoldenManifest(t *testing.T) {
	m, err := goldenLoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.ContractVersion != contract.ContractVersion || m.GeneratedBy != generatedBy {
		t.Errorf("manifest header = %d %q", m.ContractVersion, m.GeneratedBy)
	}
	disk, err := goldenDiskFiles()
	if err != nil {
		t.Fatal(err)
	}
	inManifest := map[string]goldenEntry{}
	var names []string
	for _, e := range m.Fixtures {
		if _, dup := inManifest[e.File]; dup {
			t.Errorf("manifest lists %s twice", e.File)
		}
		inManifest[e.File] = e
		names = append(names, e.File)
	}
	if !sort.StringsAreSorted(names) {
		t.Error("manifest fixtures are not sorted by file")
	}
	if !equalStrings(disk, names) {
		t.Errorf("files on disk %q != manifest entries %q", disk, names)
	}
	var groupFiles []string
	for _, grp := range goldenGroups() {
		for _, f := range grp.Files {
			groupFiles = append(groupFiles, f.Name)
		}
	}
	sort.Strings(groupFiles)
	if !equalStrings(groupFiles, names) {
		t.Errorf("golden groups %q != manifest entries %q", groupFiles, names)
	}
	for _, e := range m.Fixtures {
		b, err := os.ReadFile(filepath.Join(goldenDir, e.File))
		if err != nil {
			t.Errorf("%s: %v", e.File, err)
			continue
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != e.SHA256 {
			t.Errorf("%s: sha256 %s, manifest says %s", e.File, got, e.SHA256)
		}
		switch e.Stream {
		case streamStdout, streamStderr:
			if e.Schema == nil || !strings.HasPrefix(*e.Schema, "ctx.") || !strings.HasSuffix(*e.Schema, ".v1") {
				t.Errorf("%s: schema %v, want ctx.*.v1", e.File, e.Schema)
			}
		case streamRaw:
			if e.Schema != nil {
				t.Errorf("%s: raw entry has schema %q, want null", e.File, *e.Schema)
			}
		default:
			t.Errorf("%s: stream %q", e.File, e.Stream)
		}
		if len(e.Command) == 0 {
			t.Errorf("%s: empty command", e.File)
		}
		for _, a := range e.Command {
			if a == "--root" {
				t.Errorf("%s: command keeps --root", e.File)
			}
		}
	}
}

// goldenDocType returns a fresh value for a stdout golden's schema.
func goldenDocType(schema string) any {
	switch schema {
	case contract.SchemaVersion:
		return &contract.VersionDoc{}
	case contract.SchemaStatus:
		return &contract.StatusDoc{}
	case contract.SchemaPush:
		return &contract.PushDoc{}
	case contract.SchemaPull:
		return &contract.PullDoc{}
	case contract.SchemaResolve:
		return &contract.ResolveDoc{}
	case contract.SchemaRepair:
		return &syncops.RepairResult{}
	}
	return nil
}

// TestGoldenStrictDecode: each stdout golden decodes with
// DisallowUnknownFields into its contract type and re-encodes through
// contract.WriteDoc to identical compacted JSON; each stderr golden is one
// line whose details decode via contract.NewDetails; every time string is a
// whole-second UTC time (E16).
func TestGoldenStrictDecode(t *testing.T) {
	m, err := goldenLoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Fixtures {
		b, err := os.ReadFile(filepath.Join(goldenDir, e.File))
		if err != nil {
			t.Fatal(err)
		}
		switch e.Stream {
		case streamStdout:
			v := goldenDocType(*e.Schema)
			if v == nil {
				t.Errorf("%s: no Go type for schema %s", e.File, *e.Schema)
				continue
			}
			if err := strictDecode(b, v); err != nil {
				t.Errorf("%s: strict decode into %T: %v", e.File, v, err)
				continue
			}
			var re bytes.Buffer
			if err := contract.WriteDoc(&re, v); err != nil {
				t.Fatal(err)
			}
			var a, c bytes.Buffer
			_ = json.Compact(&a, b)
			_ = json.Compact(&c, re.Bytes())
			if !bytes.Equal(a.Bytes(), c.Bytes()) {
				t.Errorf("%s: re-encoding through WriteDoc differs:\n golden: %s\n  again: %s", e.File, a.Bytes(), c.Bytes())
			}
			if err := checkDocBytes(b); err != nil {
				t.Errorf("%s: %v", e.File, err)
			}
		case streamStderr:
			env, err := parseEnvelopeLine(b, e.ExitCode)
			if err != nil {
				t.Errorf("%s: %v", e.File, err)
				continue
			}
			if m := reErrorGolden.FindStringSubmatch(e.File); m != nil && string(env.Code) != m[1] {
				t.Errorf("%s: envelope code %s", e.File, env.Code)
			}
		}
	}
}

// goldenEachFile calls fn with every golden's name and bytes.
func goldenEachFile(t *testing.T, fn func(name string, b []byte)) {
	t.Helper()
	files, err := goldenDiskFiles()
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, goldenManifest)
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join(goldenDir, name))
		if err != nil {
			t.Fatal(err)
		}
		fn(name, b)
	}
}

// TestGoldens_NoSecrets: no golden holds the testkit key, a bearer header,
// a personal access token prefix or a wrong key used by the suite.
func TestGoldens_NoSecrets(t *testing.T) {
	secrets := []string{"testkit-key", "Bearer", "ctxp_", "ctxi_", goldenWrongKey}
	goldenEachFile(t, func(name string, b []byte) {
		for _, s := range secrets {
			if bytes.Contains(b, []byte(s)) {
				t.Errorf("%s contains %q", name, s)
			}
		}
	})
}

// TestGoldens_NoPagesETag: pages.etag is reserved and never emitted (E13).
func TestGoldens_NoPagesETag(t *testing.T) {
	goldenEachFile(t, func(name string, b []byte) {
		if bytes.Contains(b, []byte("pages.etag")) {
			t.Errorf("%s lists pages.etag (E13)", name)
		}
	})
}

// goldenWrongKey is the token of the auth_rejected goldens.
const goldenWrongKey = "e2e-revoked-token"
