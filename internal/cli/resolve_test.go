package cli

// Owner: WP-1.4. `ctx resolve` command tests: the flag and argument
// surface, the envelope only with --json (E21), the strict ctx.resolve.v1
// document, the human next-step text, and the two-project end-to-end
// conflict. Helpers are prefixed sr14Resolve so they
// never collide with show_test.go's sr14Show* helpers.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
)

// sr14ResolveRepo is the repo id of every resolve project.
const sr14ResolveRepo = "acme-kb"

// sr14ResolveRel is the page the resolve fixtures conflict on.
const sr14ResolveRel = "wiki/concepts/retry-policy.md"

// sr14ResolvePage returns a valid concept page at sr14ResolveRel.
func sr14ResolvePage(body string) []byte {
	return testkit.PageBytes(testkit.PageSpec{Slug: "retry-policy", Body: body})
}

// sr14ResolveNormalize is syncstate.Normalize, failing the test on error.
func sr14ResolveNormalize(t *testing.T, b []byte) []byte {
	t.Helper()
	n, err := syncstate.Normalize(b)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// sr14ResolvePush pushes Normalize(content) at sr14ResolveRel with parent
// and returns the push response.
func sr14ResolvePush(t *testing.T, c *sync.Client, content []byte, parent string) *sync.PushResponse {
	t.Helper()
	resp, err := c.PushPages(sr14ResolveRepo, &sync.PushRequest{Files: []sync.PushFile{{
		Path: sr14ResolveRel, Content: string(sr14ResolveNormalize(t, content)), ParentSHA: parent,
	}}})
	if err != nil {
		t.Fatalf("PushPages: %v", err)
	}
	return resp
}

// sr14ResolvePushed pushes like sr14ResolvePush and returns the new sha,
// failing on a conflict.
func sr14ResolvePushed(t *testing.T, c *sync.Client, content []byte, parent string) string {
	t.Helper()
	resp := sr14ResolvePush(t, c, content, parent)
	if len(resp.Conflicts) != 0 || len(resp.Pushed) != 1 {
		t.Fatalf("push: pushed %+v, conflicts %+v", resp.Pushed, resp.Conflicts)
	}
	return resp.Pushed[0].SHA
}

// sr14ResolveSynced records what a pull of content at sha leaves behind in
// p: the working file, the base and page_shas.
func sr14ResolveSynced(t *testing.T, p *testkit.Project, content []byte, sha string) {
	t.Helper()
	norm := sr14ResolveNormalize(t, content)
	p.WriteRaw(sr14ResolveRel, norm)
	b, err := syncstate.OpenBases(p.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Put(sr14ResolveRel, norm, sha, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	st, _, err := sync.LoadStateOrEmpty(p.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	st.PageSHAs[sr14ResolveRel] = sha
	if err := sync.SaveState(p.HubDir, st); err != nil {
		t.Fatal(err)
	}
}

// sr14ResolveFix is a project on a real in-process server whose page sits
// at s1 locally while a teammate pushed s2.
type sr14ResolveFix struct {
	s      *testkit.Server
	p      *testkit.Project
	client *sync.Client
	s1, s2 string
}

// sr14ResolveServerFix pushes the base at s1, syncs the project to it and
// pushes a teammate's edit at s2. The working file is still the base: the
// page is incoming.
func sr14ResolveServerFix(t *testing.T) *sr14ResolveFix {
	t.Helper()
	s := testkit.StartServer(t)
	x := &sr14ResolveFix{s: s, client: sync.NewClient(s.URL, s.Key)}
	x.p = testkit.NewProject(t, testkit.ProjectOptions{ServerURL: s.URL, Key: s.Key, RepoID: sr14ResolveRepo})
	base := sr14ResolvePage("Retry three times.")
	x.s1 = sr14ResolvePushed(t, x.client, base, "")
	sr14ResolveSynced(t, x.p, base, x.s1)
	x.s2 = sr14ResolvePushed(t, x.client, sr14ResolvePage("Retry five times."), x.s1)
	return x
}

// sr14ResolveServerConflict is sr14ResolveServerFix with the working file
// edited too: the page is a both_modified conflict.
func sr14ResolveServerConflict(t *testing.T) *sr14ResolveFix {
	t.Helper()
	x := sr14ResolveServerFix(t)
	x.p.WriteRaw(sr14ResolveRel, sr14ResolvePage("Retry with exponential backoff."))
	return x
}

// sr14ResolveDecode strictly decodes stdout as exactly one ResolveDoc and
// checks it is byte-for-byte what contract.WriteDoc writes for it, which
// pins the key order and the trailing newline.
func sr14ResolveDecode(t *testing.T, stdout string) contract.ResolveDoc {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	var doc contract.ResolveDoc
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not a strict ctx.resolve.v1 document: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("trailing data after the document:\n%s", stdout)
	}
	var buf bytes.Buffer
	if err := contract.WriteDoc(&buf, doc); err != nil {
		t.Fatal(err)
	}
	if buf.String() != stdout {
		t.Errorf("stdout is not the WriteDoc encoding of its document:\n got %q\nwant %q", stdout, buf.String())
	}
	if doc.Schema != contract.SchemaResolve {
		t.Errorf("schema = %q, want %q", doc.Schema, contract.SchemaResolve)
	}
	return doc
}

// sr14ResolveDetails strictly decodes the envelope's details into v.
func sr14ResolveDetails(t *testing.T, stderr string, v any) {
	t.Helper()
	lines := wp01NonEmptyLines(stderr)
	if len(lines) == 0 {
		t.Fatal("stderr is empty")
	}
	var raw struct {
		Error struct {
			Details json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &raw); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw.Error.Details))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("details %s: %v", raw.Error.Details, err)
	}
}

// sr14ResolveEnvelope asserts a failed --json run: exit, code, empty
// stdout, and the envelope as the only stderr line.
func sr14ResolveEnvelope(t *testing.T, code int, stdout, stderr string, exit int, want contract.ErrorCode) contract.ErrorEnvelope {
	t.Helper()
	if code != exit {
		t.Errorf("exit = %d, want %d (stderr %q)", code, exit, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if n := len(wp01NonEmptyLines(stderr)); n != 1 {
		t.Errorf("stderr must be one envelope line, got %q", stderr)
	}
	env := wp01Envelope(t, stderr)
	if env.Error.Code != want {
		t.Errorf("code = %q, want %q (message %q)", env.Error.Code, want, env.Error.Message)
	}
	if env.ExitCode != code {
		t.Errorf("envelope exit_code %d != process exit %d", env.ExitCode, code)
	}
	return env
}

func TestResolveFlags(t *testing.T) {
	wp01IsolateEnv(t)
	resolve := wp01Command(t, "resolve")
	if resolve.Hidden {
		t.Error("ctx resolve is hidden; WP-1.4 unhides it")
	}
	for name, typ := range map[string]string{
		"ours": "bool", "theirs": "bool", "json": "bool", "if-server-sha": "string",
	} {
		f := resolve.Flags().Lookup(name)
		if f == nil {
			t.Errorf("--%s is not defined", name)
			continue
		}
		if f.Value.Type() != typ {
			t.Errorf("--%s is %s, want %s", name, f.Value.Type(), typ)
		}
		if f.Hidden {
			t.Errorf("--%s must not be hidden", name)
		}
	}
	if d := resolve.Flags().Lookup("json").DefValue; d != "false" {
		t.Errorf("--json defaults to %q, want false (envelope only with --json, E21)", d)
	}

	if c := wp01ArgsCode(t, resolve, "wiki/concepts/a.md"); c != "" {
		t.Errorf("one path rejected: %q", c)
	}
	for _, args := range [][]string{{}, {"a.md", "b.md"}} {
		if c := wp01ArgsCode(t, resolve, args...); c != contract.CodeUsage {
			t.Errorf("args %q -> %q, want usage", args, c)
		}
	}
}

// Exactly one strategy is required. The check runs before RequireHub, so it
// is usage even in an uninitialised root.
func TestResolveJSON_RequiresExactlyOneSide(t *testing.T) {
	uninit := t.TempDir()
	initRoot := wp01InitRoot(t)
	for _, root := range []string{uninit, initRoot} {
		for _, flags := range [][]string{{"--ours", "--theirs"}, {}} {
			args := append([]string{"resolve", "wiki/concepts/a.md", "--json", "--root", root}, flags...)
			code, stdout, stderr := wp01Run(t, args...)
			env := sr14ResolveEnvelope(t, code, stdout, stderr, contract.ExitUsage, contract.CodeUsage)
			if !strings.Contains(env.Error.Message, "exactly one of --ours or --theirs") {
				t.Errorf("%q: message = %q", args, env.Error.Message)
			}
		}
	}
}

// Without --json a usage error stays human: exit 1, no envelope (E21).
func TestResolveHuman_BothFlagsExit1NoEnvelope(t *testing.T) {
	uninit := t.TempDir()
	for _, flags := range [][]string{{"--ours", "--theirs"}, {}} {
		args := append([]string{"resolve", "wiki/concepts/a.md", "--root", uninit}, flags...)
		code, stdout, stderr := wp01Run(t, args...)
		if code != 1 || stdout != "" {
			t.Errorf("%q: exit %d stdout %q, want 1 and nothing", args, code, stdout)
		}
		if strings.Contains(stderr, "{") || strings.TrimSpace(stderr) == "" {
			t.Errorf("%q: human stderr = %q", args, stderr)
		}
	}
}

func TestResolveJSON_ShapeStrict(t *testing.T) {
	x := sr14ResolveServerConflict(t)
	code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--theirs", "--json", "--root", x.p.Root)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	doc := sr14ResolveDecode(t, stdout)
	if doc.Path != sr14ResolveRel || doc.Strategy != contract.ResolveTheirs ||
		doc.PreviousState != contract.StateConflict || doc.State != contract.StateUnchanged ||
		doc.BaseSHA != x.s2 || !doc.FileWritten || doc.Warnings == nil {
		t.Errorf("doc = %+v", doc)
	}
	if !strings.HasPrefix(doc.BackupFile, ".sync/backup/"+sr14ResolveRel+".") {
		t.Errorf("backup_file = %q", doc.BackupFile)
	}
	for _, key := range []string{`"warnings": []`, `"file_written": true`} {
		if !strings.Contains(stdout, key) {
			t.Errorf("stdout lacks %s:\n%s", key, stdout)
		}
	}
}

func TestResolveJSON_ErrorsAreEnvelopes(t *testing.T) {
	t.Run("not_resolvable", func(t *testing.T) {
		x := sr14ResolveServerFix(t)
		code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--ours", "--json", "--root", x.p.Root)
		sr14ResolveEnvelope(t, code, stdout, stderr, contract.ExitRefused, contract.CodeNotResolvable)
		var d contract.NotResolvableDetails
		sr14ResolveDetails(t, stderr, &d)
		want := contract.NotResolvableDetails{Path: sr14ResolveRel, State: contract.StateIncoming, Strategy: contract.ResolveOurs}
		if d != want {
			t.Errorf("details = %+v, want %+v", d, want)
		}
	})
	t.Run("stale", func(t *testing.T) {
		x := sr14ResolveServerConflict(t)
		code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--theirs", "--if-server-sha", x.s1,
			"--json", "--root", x.p.Root)
		sr14ResolveEnvelope(t, code, stdout, stderr, contract.ExitConflict, contract.CodeStale)
		var d contract.StaleDetails
		sr14ResolveDetails(t, stderr, &d)
		if d != (contract.StaleDetails{Path: sr14ResolveRel, ExpectedSHA: x.s1, ServerSHA: x.s2}) {
			t.Errorf("details = %+v, want server_sha %s", d, x.s2)
		}
	})
	t.Run("invalid_path", func(t *testing.T) {
		x := sr14ResolveServerConflict(t)
		for _, root := range []string{x.p.Root, t.TempDir()} {
			for _, arg := range []string{"../x.md", ".sync/state.json", "credentials.json", "index.md"} {
				code, stdout, stderr := wp01Run(t, "resolve", arg, "--theirs", "--json", "--root", root)
				sr14ResolveEnvelope(t, code, stdout, stderr, contract.ExitUsage, contract.CodeInvalidPath)
			}
		}
	})
	t.Run("bad --if-server-sha", func(t *testing.T) {
		root := wp01InitRoot(t)
		code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--ours", "--if-server-sha", "HEAD",
			"--json", "--root", root)
		sr14ResolveEnvelope(t, code, stdout, stderr, contract.ExitUsage, contract.CodeUsage)
	})
	t.Run("page_not_found", func(t *testing.T) {
		x := sr14ResolveServerConflict(t)
		code, stdout, stderr := wp01Run(t, "resolve", "wiki/concepts/never-pushed.md", "--theirs", "--json", "--root", x.p.Root)
		sr14ResolveEnvelope(t, code, stdout, stderr, contract.ExitNotFound, contract.CodePageNotFound)
	})
	t.Run("repo_not_found", func(t *testing.T) {
		s := testkit.StartServer(t)
		p := testkit.NewProject(t, testkit.ProjectOptions{ServerURL: s.URL, Key: s.Key, RepoID: "never-created"})
		code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--theirs", "--json", "--root", p.Root)
		sr14ResolveEnvelope(t, code, stdout, stderr, contract.ExitNotFound, contract.CodeRepoNotFound)
	})
	t.Run("not_authenticated", func(t *testing.T) {
		p := testkit.NewProject(t, testkit.ProjectOptions{ServerURL: "http://127.0.0.1:1", RepoID: sr14ResolveRepo, NoCreds: true})
		code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--ours", "--json", "--root", p.Root)
		env := sr14ResolveEnvelope(t, code, stdout, stderr, contract.ExitAuth, contract.CodeNotAuthenticated)
		if len(env.Error.Advice) == 0 {
			t.Error("not_authenticated carries no advice")
		}
	})
	t.Run("repo_not_configured", func(t *testing.T) {
		s := testkit.StartServer(t)
		p := testkit.NewProject(t, testkit.ProjectOptions{ServerURL: s.URL, Key: s.Key})
		code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--ours", "--json", "--root", p.Root)
		sr14ResolveEnvelope(t, code, stdout, stderr, contract.ExitConfig, contract.CodeRepoNotConfigured)
	})
	t.Run("not_initialized", func(t *testing.T) {
		code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--ours", "--json", "--root", t.TempDir())
		env := sr14ResolveEnvelope(t, code, stdout, stderr, contract.ExitConfig, contract.CodeNotInitialized)
		if !reflect.DeepEqual(env.Error.Advice, []string{"ctx init"}) {
			t.Errorf("advice = %q, want [ctx init]", env.Error.Advice)
		}
	})
	t.Run("busy", func(t *testing.T) {
		x := sr14ResolveServerConflict(t)
		t.Setenv("CONTEXO_LOCK_TIMEOUT", "200ms")
		unlock, err := syncstate.Lock(x.p.HubDir, "push", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--theirs", "--json", "--root", x.p.Root)
		sr14ResolveEnvelope(t, code, stdout, stderr, contract.ExitBusy, contract.CodeBusy)
		var d contract.BusyDetails
		sr14ResolveDetails(t, stderr, &d)
		if d.Command != "push" || d.PID != os.Getpid() {
			t.Errorf("details = %+v, want the push lock held by this process", d)
		}
	})
}

// Human success text names the next step: push after --ours, the backup's
// absolute path after --theirs.
func TestResolveHuman_NextStep(t *testing.T) {
	t.Run("ours", func(t *testing.T) {
		x := sr14ResolveServerConflict(t)
		code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--ours", "--root", x.p.Root)
		if code != 0 || stderr != "" {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		if !strings.Contains(stdout, "now run ctx push") || !strings.Contains(stdout, x.s2[:7]) {
			t.Errorf("stdout = %q, want the push next step and the short sha %s", stdout, x.s2[:7])
		}
	})
	t.Run("theirs", func(t *testing.T) {
		x := sr14ResolveServerConflict(t)
		code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--theirs", "--root", x.p.Root)
		if code != 0 || stderr != "" {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		dir := filepath.Join(x.p.HubDir, ".sync", "backup", "wiki", "concepts")
		ents, err := os.ReadDir(dir)
		if err != nil || len(ents) != 1 {
			t.Fatalf("backups in %s: %v (%v)", dir, ents, err)
		}
		if abs := filepath.Join(dir, ents[0].Name()); !strings.Contains(stdout, abs) {
			t.Errorf("stdout = %q, want the backup path %s", stdout, abs)
		}

		code, stdout, _ = wp01Run(t, "resolve", sr14ResolveRel, "--theirs", "--root", x.p.Root)
		if code != 0 || !strings.Contains(stdout, "already matches the server version") {
			t.Errorf("second --theirs: exit %d, stdout %q", code, stdout)
		}
	})
}

// A human-mode failure shows the contract advice as a hint, and no JSON.
func TestResolveHuman_ErrorShowsAdvice(t *testing.T) {
	code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--ours", "--root", t.TempDir())
	if code != 1 || stdout != "" {
		t.Errorf("exit %d stdout %q, want 1 and nothing", code, stdout)
	}
	if strings.Contains(stderr, "{") || !strings.Contains(stderr, "ctx init") {
		t.Errorf("stderr = %q, want a human error with the ctx init hint", stderr)
	}

	x := sr14ResolveServerFix(t)
	code, _, stderr = wp01Run(t, "resolve", sr14ResolveRel, "--ours", "--root", x.p.Root)
	if code != 1 || strings.Contains(stderr, "{") || !strings.Contains(stderr, "incoming") {
		t.Errorf("not_resolvable in human mode: exit %d, stderr %q", code, stderr)
	}
}

func TestShowResolveNotHidden(t *testing.T) {
	wp01IsolateEnv(t)
	root := NewRootCmd()
	for _, name := range []string{"show", "resolve"} {
		t.Run(name, func(t *testing.T) {
			c, _, err := root.Find([]string{name})
			if err != nil || c == nil || c.Name() != name {
				t.Fatalf("ctx %s is not registered (%v)", name, err)
			}
			if c.Hidden {
				t.Errorf("ctx %s is hidden; WP-1.4 unhides it", name)
			}
		})
	}
}

func TestResolveCapability(t *testing.T) {
	wp01IsolateEnv(t)
	for _, c := range advertisedCapabilities() {
		if c == contract.CapResolve {
			return
		}
	}
	t.Errorf("capability %q is not advertised: %q", contract.CapResolve, advertisedCapabilities())
}

// The full conflict loop on a real server with two projects: bob pushes,
// alice's push conflicts and records the sidecar, `ctx resolve --ours`
// re-bases alice onto bob's version, and her next push supersedes it.
func TestResolveE2E_ConflictThenOurs(t *testing.T) {
	s := testkit.StartServer(t)
	client := sync.NewClient(s.URL, s.Key)
	opts := testkit.ProjectOptions{ServerURL: s.URL, Key: s.Key, RepoID: sr14ResolveRepo}
	alice := testkit.NewProject(t, opts)
	bob := testkit.NewProject(t, opts)

	// alice creates the page (parent ""); bob pulls it.
	base := sr14ResolvePage("Retry three times.")
	s1 := sr14ResolvePushed(t, client, base, "")
	sr14ResolveSynced(t, alice, base, s1)
	sr14ResolveSynced(t, bob, base, s1)

	// bob edits and pushes on top of s1.
	bobEdit := sr14ResolvePage("Retry five times.")
	bob.WriteRaw(sr14ResolveRel, bobEdit)
	s2 := sr14ResolvePushed(t, client, bobEdit, s1)

	// alice edits; her push with parent s1 conflicts, and push records the
	// sidecar from the 409.
	aliceEdit := sr14ResolvePage("Retry with exponential backoff.")
	alice.WriteRaw(sr14ResolveRel, aliceEdit)
	resp := sr14ResolvePush(t, client, aliceEdit, s1)
	if len(resp.Conflicts) != 1 || resp.Conflicts[0].CurrentSHA != s2 {
		t.Fatalf("alice's push: pushed %+v, conflicts %+v; want one conflict at %s", resp.Pushed, resp.Conflicts, s2)
	}
	conflicts, err := syncstate.OpenConflicts(alice.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := conflicts.Put(syncstate.ConflictRecord{
		Path: sr14ResolveRel, ServerSHA: resp.Conflicts[0].CurrentSHA, BaseSHA: s1,
		Kind: syncstate.KindBothModified, Source: syncstate.SourcePush,
	}, []byte(schema.SanitizeContent(string(resp.Conflicts[0].CurrentContent)))); err != nil {
		t.Fatal(err)
	}
	if err := conflicts.Save(); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := wp01Run(t, "resolve", sr14ResolveRel, "--ours", "--json", "--root", alice.Root)
	if code != 0 || stderr != "" {
		t.Fatalf("resolve --ours: exit %d, stderr %q", code, stderr)
	}
	doc := sr14ResolveDecode(t, stdout)
	if doc.PreviousState != contract.StateConflict || doc.State != contract.StateModified ||
		doc.BaseSHA != s2 || doc.FileWritten || doc.BackupFile != "" {
		t.Errorf("doc = %+v, want conflict -> modified at %s, file untouched", doc, s2)
	}
	if got := alice.Read(sr14ResolveRel); string(got) != string(aliceEdit) {
		t.Error("--ours changed alice's working file")
	}
	conflicts, err = syncstate.OpenConflicts(alice.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := conflicts.Get(sr14ResolveRel); ok {
		t.Errorf("the sidecar survived --ours: %+v", rec)
	}

	// alice's push on top of the new base supersedes bob's version.
	sr14ResolvePushed(t, client, aliceEdit, doc.BaseSHA)
	got, _, err := client.ReadPage(sr14ResolveRepo, sr14ResolveRel)
	if err != nil {
		t.Fatalf("bob's ReadPage: %v", err)
	}
	if want := sr14ResolveNormalize(t, aliceEdit); string(got) != string(want) {
		t.Errorf("bob reads %q, want alice's content %q", got, want)
	}
}
