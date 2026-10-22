package e2e

import (
	"bytes"
	"net/http"
	"os"
	"runtime"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
)

// sv is the helper prefix of this file (scenarios 09 and 10).

// svWantCapabilities is the E19 order: the 11 v1 capabilities, then repair.
var svWantCapabilities = []contract.Capability{
	"status.json", "status.offline", "show.base", "show.server", "show.sha",
	"pull.json", "push.json", "push.paths", "push.distill_guard", "resolve",
	"errors.envelope", "repair",
}

// TestScenario10_VersionJSON: the handshake with no project and an empty
// HOME. With CONTEXO_E2E_CTX (a stamped binary) only the strict decode and
// the capability check run.
func TestScenario10_VersionJSON(t *testing.T) {
	u := newBareUser(t, "nobody")
	r := runCtx(t, u, "version", "--json")
	var doc contract.VersionDoc
	assertOK(t, r, &doc)
	if doc.Schema != contract.SchemaVersion || doc.ContractVersion != 1 {
		t.Errorf("schema %q contract_version %d, want %s 1", doc.Schema, doc.ContractVersion, contract.SchemaVersion)
	}
	got := make([]contract.Capability, len(doc.Capabilities))
	copy(got, doc.Capabilities)
	if len(got) != len(svWantCapabilities) {
		t.Fatalf("capabilities = %q, want exactly %q (E19 order)", got, svWantCapabilities)
	}
	for i := range got {
		if got[i] != svWantCapabilities[i] {
			t.Fatalf("capabilities = %q, want exactly %q (E19 order)", got, svWantCapabilities)
		}
	}
	if ctxPrebuilt {
		return
	}
	if doc.Version != "dev" || doc.Commit != "none" || doc.Date != "unknown" || !doc.DevBuild {
		t.Errorf("unstamped build: version %q commit %q date %q dev_build %v, want dev none unknown true",
			doc.Version, doc.Commit, doc.Date, doc.DevBuild)
	}
	if doc.OS != runtime.GOOS || doc.Arch != runtime.GOARCH {
		t.Errorf("os/arch = %s/%s, want %s/%s", doc.OS, doc.Arch, runtime.GOOS, runtime.GOARCH)
	}

	// --short keeps its exact one-line output.
	s := runCtx(t, u, "version", "--short")
	if s.Exit != 0 || string(s.Stdout) != "dev\n" || len(s.Stderr) != 0 {
		t.Errorf("version --short: want exit 0, stdout \"dev\\n\", empty stderr\n%s", s)
	}
}

// svZWSP is U+200B ZERO WIDTH SPACE, which schema.SanitizeContent drops. A
// page carrying it tells the verbatim base apart from the sanitized server
// bytes (E6).
const svZWSP = "\xe2\x80\x8b"

// svUnknownSHA is a well-formed 40-hex sha no test repo contains.
const svUnknownSHA = "fedcba9876543210fedcba9876543210fedcba98"

// TestScenario09_Show: ctx show --at base|server|<sha> (E20: always machine
// mode, so no --json). Success is the raw bytes on stdout, nothing added,
// and an empty stderr.
func TestScenario09_Show(t *testing.T) {
	requireCtx(t)
	srv := startServer(t)
	spec := conceptPage("shown", "Version one"+svZWSP+" body.")
	rel := pageRel(spec)
	u := newUser(t, srv, "alice", withPages(spec))
	mustPush(t, u, "--yes")
	sha1 := serverSHA(t, srv, defaultRepo, rel)
	base1 := svReadBase(t, u, rel)
	editPage(t, u, rel, "Version two"+svZWSP+" line.")
	mustPush(t, u, "--yes")
	sha2 := serverSHA(t, srv, defaultRepo, rel)
	base2 := svReadBase(t, u, rel)
	if sha1 == sha2 || bytes.Equal(base1, base2) {
		t.Fatalf("precondition: the second push did not move %s (%s)", rel, sha2)
	}
	if !bytes.Contains(base2, []byte(svZWSP)) {
		t.Fatalf("precondition: the base must hold the zero-width space verbatim:\n%q", base2)
	}

	t.Run("base", func(t *testing.T) {
		srv.Proxy.Reset()
		got := svShowOK(t, u, rel, "base")
		if !bytes.Equal(got, base2) {
			t.Errorf("show --at base = %q, want the base snapshot %q", got, base2)
		}
		if recs := srv.Proxy.Records(); len(recs) != 0 {
			t.Errorf("show --at base made %d request(s): %+v", len(recs), recs)
		}
	})

	t.Run("server", func(t *testing.T) {
		raw := serverContent(t, srv, defaultRepo, rel)
		want := []byte(schema.SanitizeContent(string(raw)))
		if bytes.Equal(want, raw) {
			t.Fatalf("precondition: the server bytes must need sanitizing:\n%q", raw)
		}
		if got := svShowOK(t, u, rel, "server"); !bytes.Equal(got, want) {
			t.Errorf("show --at server = %q, want SanitizeContent(server) %q", got, want)
		}
	})

	t.Run("old_sha", func(t *testing.T) {
		old, err := srv.TK.Store.ReadAtSha(defaultRepo, rel, sha1)
		if err != nil {
			t.Fatal(err)
		}
		want := []byte(schema.SanitizeContent(string(old)))
		if !bytes.Equal(want, []byte(schema.SanitizeContent(string(base1)))) {
			t.Fatalf("precondition: the server bytes at %s differ from the first base", sha1)
		}
		srv.Proxy.Reset()
		if got := svShowOK(t, u, rel, sha1); !bytes.Equal(got, want) {
			t.Errorf("show --at %s = %q, want the old content %q", sha1, got, want)
		}
		recs := srv.Proxy.Records()
		if len(recs) != 1 || recs[0].RawQuery != "at="+sha1 || recs[0].Status != http.StatusOK {
			t.Errorf("show --at <sha> requests = %+v, want one ?at=%s read answered 200", recs, sha1)
		}
		// --at takes 7-64 hex: an abbreviation reads the same commit.
		if got := svShowOK(t, u, rel, sha1[:7]); !bytes.Equal(got, want) {
			t.Errorf("show --at %s = %q, want the old content %q", sha1[:7], got, want)
		}
	})

	t.Run("raw_bytes", func(t *testing.T) {
		// A teammate's page stored with CRLF line ends and no final line
		// break: show passes both through and adds nothing.
		const crlfRel = "wiki/concepts/crlf.md"
		raw := bytes.ReplaceAll(testkit.PageBytes(conceptPage("crlf", "Line one.\nLine two.")), []byte("\n"), []byte("\r\n"))
		raw = bytes.TrimRight(raw, "\r\n")
		if _, c, err := srv.TK.Store.Write(defaultRepo, crlfRel, raw, "bob", "bob@example.test", "seed crlf", ""); err != nil || c != nil {
			t.Fatalf("seed %s: conflict %v err %v", crlfRel, c, err)
		}
		if got := svShowOK(t, u, crlfRel, "server"); !bytes.Equal(got, raw) {
			t.Errorf("show --at server = %q, want exactly %q", got, raw)
		}
	})

	t.Run("unknown_sha", func(t *testing.T) {
		r := runCtx(t, u, "show", rel, "--at", svUnknownSHA)
		e := assertFail(t, r, contract.CodeSHANotFound)
		t.Logf("argv %q exit %d\nenvelope: %s", r.Args, r.Exit, e.Line)
		if e.HTTPStatus != http.StatusNotFound || e.Details != nil {
			t.Errorf("sha_not_found http_status %d details %#v, want 404 and none", e.HTTPStatus, e.Details)
		}
	})

	t.Run("old_server", func(t *testing.T) {
		old := startOldServer(t)
		c := newUser(t, old, "carol", withPages(spec))
		mustPush(t, c, "--yes")
		sha := serverSHA(t, old, defaultRepo, rel)

		r := runCtx(t, c, "show", rel, "--at", sha)
		e := assertFail(t, r, contract.CodeServerUnsupported)
		t.Logf("argv %q exit %d\nenvelope: %s", r.Args, r.Exit, e.Line)
		d, ok := e.Details.(*contract.ServerUnsupportedDetails)
		if !ok || len(d.Missing) != 1 || d.Missing[0] != contract.SrvPagesAt {
			t.Errorf("server_unsupported details = %#v, want missing [pages.at]", e.Details)
		}
		if e.HTTPStatus != 0 {
			t.Errorf("http_status = %d, want none (the old server answered 200)", e.HTTPStatus)
		}

		want := []byte(schema.SanitizeContent(string(serverContent(t, old, defaultRepo, rel))))
		if got := svShowOK(t, c, rel, "server"); !bytes.Equal(got, want) {
			t.Errorf("old server show --at server = %q, want %q", got, want)
		}
	})
}

// svShowOK runs `show rel --at at` and returns stdout after checking exit 0
// and an empty stderr.
func svShowOK(t *testing.T, u *e2eUser, rel, at string) []byte {
	t.Helper()
	r := runCtx(t, u, "show", rel, "--at", at)
	if r.Exit != 0 || len(r.Stderr) != 0 {
		t.Fatalf("show --at %s: want exit 0 and an empty stderr\n%s", at, r)
	}
	return r.Stdout
}

// svReadBase returns the base snapshot bytes of rel (.sync/base/<rel>).
func svReadBase(t *testing.T, u *e2eUser, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(syncstate.BaseFile(u.Hub, rel))
	if err != nil {
		t.Fatalf("read base of %s: %v", rel, err)
	}
	return b
}
