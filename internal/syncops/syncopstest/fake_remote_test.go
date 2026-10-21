package syncopstest

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	stdsync "sync"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// FakeRemote implements syncops.Remote. The assertion lives here, not in
// fake_remote.go, so syncopstest itself never imports syncops and a
// package-syncops test may import syncopstest without an import cycle.
var _ syncops.Remote = (*FakeRemote)(nil)

const (
	wp01Repo    = "acme-kb"
	wp01PathA   = "wiki/concepts/a.md"
	wp01PathB   = "wiki/concepts/b.md"
	wp01PathC   = "wiki/concepts/c.md"
	wp01Missing = "wiki/concepts/missing.md"
	// A well-formed sha that is not in any fake repo.
	wp01UnknownSHA = "dddddddddddddddddddddddddddddddddddddddd"
)

// wp01SHA is the sha of the fake's n-th commit.
func wp01SHA(n int) string {
	return fmt.Sprintf("%x", sha1.Sum([]byte("fake:"+strconv.Itoa(n))))
}

// wp01Time is the commit time of the fake's n-th commit.
func wp01Time(n int) time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Second)
}

func wp01Push(t *testing.T, f *FakeRemote, files ...sync.PushFile) *sync.PushResponse {
	t.Helper()
	resp, err := f.PushPages(wp01Repo, &sync.PushRequest{
		AuthorName: "Ana", AuthorEmail: "ana@acme.dev", Message: "edit", Files: files,
	})
	if err != nil {
		t.Fatalf("PushPages: %v", err)
	}
	if resp == nil {
		t.Fatal("PushPages returned a nil response")
	}
	return resp
}

func wp01HTTPError(t *testing.T, err error) *sync.HTTPError {
	t.Helper()
	var he *sync.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("error %v (%T) is not a *sync.HTTPError", err, err)
	}
	return he
}

func wp01AssertCode(t *testing.T, err error, status int, code contract.ErrorCode, is error) {
	t.Helper()
	he := wp01HTTPError(t, err)
	if he.HTTPStatus() != status || he.ErrorCode() != string(code) {
		t.Fatalf("error = %d/%s (%q), want %d/%s", he.HTTPStatus(), he.ErrorCode(), he.Error(), status, code)
	}
	if !errors.Is(err, is) {
		t.Fatalf("errors.Is(%v, %v) = false", err, is)
	}
}

func TestFakeRemoteDeterministicSHAs(t *testing.T) {
	f := NewFakeRemote(FakeOptions{})
	if f.Head() != "" {
		t.Fatalf("empty repo Head() = %q, want \"\"", f.Head())
	}
	s1 := f.Seed(wp01PathA, []byte("v1\n"))
	s2 := f.Seed(wp01PathB, []byte("b\n"))
	if s1 != wp01SHA(1) || s2 != wp01SHA(2) {
		t.Fatalf("seed shas = %s, %s; want hex(sha1(fake:1)), hex(sha1(fake:2))", s1, s2)
	}
	if len(s1) != 40 {
		t.Fatalf("sha %q is not 40 hex", s1)
	}
	if f.Head() != s2 {
		t.Fatalf("Head() = %s, want %s", f.Head(), s2)
	}
	// Seeding unchanged content is a no-op write: no new commit.
	if got := f.Seed(wp01PathA, []byte("v1\n")); got != s1 || f.Head() != s2 {
		t.Fatalf("unchanged Seed = %s (head %s), want %s (head %s)", got, f.Head(), s1, s2)
	}
	if b, ok := f.Content(wp01PathA); !ok || string(b) != "v1\n" {
		t.Fatalf("Content(a) = %q, %v", b, ok)
	}
	if _, ok := f.Content(wp01Missing); ok {
		t.Fatal("Content(missing) reported present")
	}
	// Content returns a copy.
	b, _ := f.Content(wp01PathA)
	b[0] = 'X'
	if again, _ := f.Content(wp01PathA); string(again) != "v1\n" {
		t.Fatalf("Content aliased the fake's bytes: %q", again)
	}
}

func TestFakeRemoteParentSHAConflict(t *testing.T) {
	f := NewFakeRemote(FakeOptions{})
	s1 := f.Seed(wp01PathA, []byte("v1\n"))
	s2 := f.Seed(wp01PathA, []byte("v2 teammate\n")) // the server moved past our base

	resp := wp01Push(t, f, sync.PushFile{Path: wp01PathA, Content: "v2 mine\n", ParentSHA: s1})
	want := sync.Conflict{
		Path: wp01PathA, CurrentSHA: s2, CurrentContent: []byte("v2 teammate\n"),
		ExpectedParentSHA: s1, AncestorContent: []byte("v1\n"),
	}
	if len(resp.Conflicts) != 1 || !reflect.DeepEqual(resp.Conflicts[0], want) {
		t.Fatalf("conflicts = %+v, want [%+v]", resp.Conflicts, want)
	}
	if len(resp.Pushed) != 0 {
		t.Fatalf("pushed = %+v, want none", resp.Pushed)
	}
	if resp.NewHead != s2 || f.Head() != s2 {
		t.Fatalf("new_head = %s, Head() = %s; want %s (nothing committed)", resp.NewHead, f.Head(), s2)
	}
	if b, _ := f.Content(wp01PathA); string(b) != "v2 teammate\n" {
		t.Fatalf("content after conflict = %q", b)
	}

	t.Run("unknown parent has no ancestor", func(t *testing.T) {
		resp := wp01Push(t, f, sync.PushFile{Path: wp01PathA, Content: "mine\n", ParentSHA: wp01UnknownSHA})
		if len(resp.Conflicts) != 1 || resp.Conflicts[0].AncestorContent != nil ||
			resp.Conflicts[0].CurrentSHA != s2 || resp.Conflicts[0].ExpectedParentSHA != wp01UnknownSHA {
			t.Fatalf("conflicts = %+v", resp.Conflicts)
		}
	})

	t.Run("parent where the path is absent has no ancestor", func(t *testing.T) {
		g := NewFakeRemote(FakeOptions{})
		other := g.Seed(wp01PathB, []byte("b\n"))
		cur := g.Seed(wp01PathA, []byte("a\n"))
		resp := wp01Push(t, g, sync.PushFile{Path: wp01PathA, Content: "mine\n", ParentSHA: other})
		if len(resp.Conflicts) != 1 || resp.Conflicts[0].AncestorContent != nil || resp.Conflicts[0].CurrentSHA != cur {
			t.Fatalf("conflicts = %+v", resp.Conflicts)
		}
	})

	t.Run("matching parent writes", func(t *testing.T) {
		resp := wp01Push(t, f, sync.PushFile{Path: wp01PathA, Content: "v3\n", ParentSHA: s2})
		if len(resp.Conflicts) != 0 || len(resp.Pushed) != 1 {
			t.Fatalf("resp = %+v", resp)
		}
		got := resp.Pushed[0]
		if got.Path != wp01PathA || got.SHA != f.Head() || got.SHA == s2 || resp.NewHead != got.SHA {
			t.Fatalf("pushed = %+v, new_head %s, head %s", got, resp.NewHead, f.Head())
		}
	})

	t.Run("parent on an absent path creates it (no check)", func(t *testing.T) {
		resp := wp01Push(t, f, sync.PushFile{Path: wp01PathC, Content: "c\n", ParentSHA: wp01UnknownSHA})
		if len(resp.Conflicts) != 0 || len(resp.Pushed) != 1 || resp.Pushed[0].SHA != f.Head() {
			t.Fatalf("resp = %+v", resp)
		}
	})

	t.Run("partial: one conflict, one pushed, one commit per file", func(t *testing.T) {
		g := NewFakeRemote(FakeOptions{})
		a1 := g.Seed(wp01PathA, []byte("a1\n"))
		a2 := g.Seed(wp01PathA, []byte("a2\n"))
		b1 := g.Seed(wp01PathB, []byte("b1\n"))
		resp := wp01Push(t, g,
			sync.PushFile{Path: wp01PathA, Content: "mine\n", ParentSHA: a1},
			sync.PushFile{Path: wp01PathB, Content: "b2\n", ParentSHA: b1},
			sync.PushFile{Path: wp01PathC, Content: "c1\n"},
		)
		if len(resp.Conflicts) != 1 || resp.Conflicts[0].Path != wp01PathA || resp.Conflicts[0].CurrentSHA != a2 {
			t.Fatalf("conflicts = %+v", resp.Conflicts)
		}
		wantPushed := []sync.PushedFile{{Path: wp01PathB, SHA: wp01SHA(4)}, {Path: wp01PathC, SHA: wp01SHA(5)}}
		if !reflect.DeepEqual(resp.Pushed, wantPushed) {
			t.Fatalf("pushed = %+v, want %+v", resp.Pushed, wantPushed)
		}
		if resp.NewHead != wp01SHA(5) || g.Head() != wp01SHA(5) {
			t.Fatalf("new_head = %s, head = %s", resp.NewHead, g.Head())
		}
	})
}

func TestFakeRemoteNoopReturnsPathSHA(t *testing.T) {
	f := NewFakeRemote(FakeOptions{})
	a := f.Seed(wp01PathA, []byte("same\n"))
	b := f.Seed(wp01PathB, []byte("b\n")) // HEAD moves past a's last touch

	for _, parent := range []string{a, ""} {
		resp := wp01Push(t, f, sync.PushFile{Path: wp01PathA, Content: "same\n", ParentSHA: parent})
		want := []sync.PushedFile{{Path: wp01PathA, SHA: a}}
		if !reflect.DeepEqual(resp.Pushed, want) || len(resp.Conflicts) != 0 {
			t.Fatalf("parent %q: resp = %+v, want pushed %+v", parent, resp, want)
		}
		// new_head is repo HEAD after the batch (WP-0.2 handler.Push:
		// store.HeadSHA), never the no-op file's own older sha.
		if resp.NewHead != b {
			t.Fatalf("parent %q: new_head = %s, want HEAD %s (not the no-op path sha %s)", parent, resp.NewHead, b, a)
		}
		if f.Head() != b {
			t.Fatalf("no-op write committed: Head() = %s, want %s", f.Head(), b)
		}
	}
	pages, err := f.ListPages(wp01Repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0].SHA != a {
		t.Fatalf("listing after no-op = %+v", pages)
	}

	// A mixed batch whose last file is a no-op: new_head is still HEAD (b's
	// new commit), not a's older last-touch sha.
	for _, legacy := range []bool{false, true} {
		g := NewFakeRemote(FakeOptions{LegacyNoopHeadSHA: legacy})
		a := g.Seed(wp01PathA, []byte("same\n"))
		b := g.Seed(wp01PathB, []byte("b1\n"))
		resp := wp01Push(t, g,
			sync.PushFile{Path: wp01PathB, Content: "b2\n", ParentSHA: b},
			sync.PushFile{Path: wp01PathA, Content: "same\n", ParentSHA: a},
		)
		head := g.Head()
		if head == b || len(resp.Pushed) != 2 || resp.Pushed[0].SHA != head {
			t.Fatalf("legacy %v: resp = %+v, head %s", legacy, resp, head)
		}
		wantNoop := a
		if legacy {
			wantNoop = head
		}
		if resp.Pushed[1].SHA != wantNoop {
			t.Fatalf("legacy %v: no-op pushed sha = %s, want %s", legacy, resp.Pushed[1].SHA, wantNoop)
		}
		if resp.NewHead != head {
			t.Fatalf("legacy %v: new_head = %s, want HEAD %s", legacy, resp.NewHead, head)
		}
	}
}

func TestFakeRemoteLegacyNoopHeadSHA(t *testing.T) {
	f := NewFakeRemote(FakeOptions{LegacyNoopHeadSHA: true})
	a := f.Seed(wp01PathA, []byte("same\n"))
	head := f.Seed(wp01PathB, []byte("b\n"))

	resp := wp01Push(t, f, sync.PushFile{Path: wp01PathA, Content: "same\n", ParentSHA: a})
	want := []sync.PushedFile{{Path: wp01PathA, SHA: head}}
	if !reflect.DeepEqual(resp.Pushed, want) || resp.NewHead != head {
		t.Fatalf("resp = %+v, want pushed %+v (repo HEAD, the legacy bug)", resp, want)
	}
	if f.Head() != head {
		t.Fatalf("no-op write committed: Head() = %s", f.Head())
	}
	// The path's real last touch is unchanged.
	if pages, _ := f.ListPages(wp01Repo); pages[0].SHA != a {
		t.Fatalf("listing = %+v", pages)
	}
	if _, caps, _ := f.ServerInfo(); wp01HasCap(caps, contract.SrvWritePathSHA) {
		t.Fatalf("legacy server advertises write.path_sha: %v", caps)
	}
}

func TestFakeRemoteListPagesLastTouch(t *testing.T) {
	f := NewFakeRemote(FakeOptions{})
	empty, err := f.ListPages(wp01Repo)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty repo: ListPages = %#v, %v; want [] (non-nil)", empty, err)
	}

	f.Seed(wp01PathB, []byte("b1\n"))                               // 1
	f.Seed(wp01PathA, []byte("a1\n"))                               // 2
	f.Seed(wp01PathA, []byte("a2\n"))                               // 3
	wp01Push(t, f, sync.PushFile{Path: wp01PathC, Content: "c1\n"}) // 4, by Ana

	got, err := f.ListPages(wp01Repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []sync.PageMeta{
		{Path: wp01PathA, SHA: wp01SHA(3), Author: "Seed", Email: "seed@acme.dev", Time: wp01Time(3), Message: "seed " + wp01PathA},
		{Path: wp01PathB, SHA: wp01SHA(1), Author: "Seed", Email: "seed@acme.dev", Time: wp01Time(1), Message: "seed " + wp01PathB},
		{Path: wp01PathC, SHA: wp01SHA(4), Author: "Ana", Email: "ana@acme.dev", Time: wp01Time(4), Message: "edit"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListPages =\n%+v\nwant\n%+v", got, want)
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].Path < got[j].Path }) {
		t.Fatal("listing not sorted by path")
	}
	if got[0].Time.Location() != time.UTC {
		t.Fatalf("time %v is not UTC", got[0].Time)
	}

	// Server defaults for an anonymous push (handler.Push fallbacks).
	resp, err := f.PushPages(wp01Repo, &sync.PushRequest{Files: []sync.PushFile{{Path: wp01PathB, Content: "b2\n"}}})
	if err != nil || len(resp.Pushed) != 1 {
		t.Fatalf("anonymous push: %+v, %v", resp, err)
	}
	got, _ = f.ListPages(wp01Repo)
	if b := got[1]; b.Author != "unknown" || b.Email != "unknown@acme.dev" || b.Message != "ctx push" {
		t.Fatalf("anonymous push meta = %+v", b)
	}
}

func TestFakeRemoteReadPageAt(t *testing.T) {
	f := NewFakeRemote(FakeOptions{})
	s1 := f.Seed(wp01PathA, []byte("a1\n"))
	s2 := f.Seed(wp01PathB, []byte("b1\n"))
	s3 := f.Seed(wp01PathA, []byte("a2\n"))

	cases := []struct {
		path, at, content, pageSHA string
	}{
		{wp01PathA, s1, "a1\n", s1},
		{wp01PathA, s2, "a1\n", s1}, // latest touch at or before s2
		{wp01PathA, s3, "a2\n", s3},
		{wp01PathB, s3, "b1\n", s2},
		// Like git (WP-0.2 gitstore.ValidSHA + rev-parse): an abbreviated
		// or uppercase id resolves, and X-Page-SHA is the full sha.
		{wp01PathA, s2[:7], "a1\n", s1},
		{wp01PathA, s1[:4], "a1\n", s1},
		{wp01PathA, strings.ToUpper(s3), "a2\n", s3},
		{wp01PathB, strings.ToUpper(s3[:12]), "b1\n", s2},
	}
	for _, c := range cases {
		b, sha, err := f.ReadPageAt(wp01Repo, c.path, c.at)
		if err != nil || string(b) != c.content || sha != c.pageSHA {
			t.Fatalf("ReadPageAt(%s, %s) = %q, %s, %v; want %q, %s", c.path, c.at, b, sha, err, c.content, c.pageSHA)
		}
	}

	// The WP-0.2 handler's error answers (gin bodies, keys sorted).
	const (
		bodyInvalid  = `{"code":"invalid_sha","error":"invalid sha"}`
		bodyUnknown  = `{"code":"sha_not_found","error":"sha not found"}`
		bodyNotAtSHA = `{"code":"sha_not_found","error":"page not found at sha"}`
	)
	errCases := []struct {
		name, path, at string
		status         int
		code           contract.ErrorCode
		body           string
	}{
		{"path absent at sha", wp01PathB, s1, 404, contract.CodeSHANotFound, bodyNotAtSHA},
		{"path never written", wp01Missing, s3, 404, contract.CodeSHANotFound, bodyNotAtSHA},
		{"unknown sha", wp01PathA, wp01UnknownSHA, 404, contract.CodeSHANotFound, bodyUnknown},
		{"unknown abbreviation", wp01PathA, wp01UnknownSHA[:7], 404, contract.CodeSHANotFound, bodyUnknown},
		{"empty sha is invalid, not HEAD", wp01PathA, "", 400, contract.CodeUsage, bodyInvalid},
		{"3 hex is too short", wp01PathA, s1[:3], 400, contract.CodeUsage, bodyInvalid},
		{"65 hex is too long", wp01PathA, strings.Repeat("a", 65), 400, contract.CodeUsage, bodyInvalid},
		{"not hex", wp01PathA, "HEAD", 400, contract.CodeUsage, bodyInvalid},
		{"rev syntax", wp01PathA, s1[:8] + "^", 400, contract.CodeUsage, bodyInvalid},
	}
	for _, c := range errCases {
		b, sha, err := f.ReadPageAt(wp01Repo, c.path, c.at)
		if b != nil || sha != "" {
			t.Fatalf("%s: ReadPageAt(%s, %q) returned data with an error", c.name, c.path, c.at)
		}
		he := wp01HTTPError(t, err)
		if he.HTTPStatus() != c.status || he.ErrorCode() != string(c.code) || he.Body != c.body {
			t.Fatalf("%s: ReadPageAt(%s, %q) = %d/%s %s; want %d/%s %s",
				c.name, c.path, c.at, he.HTTPStatus(), he.ErrorCode(), he.Body, c.status, c.code, c.body)
		}
		if want := fmt.Sprintf("sync: read page at (%d): %s", c.status, c.body); he.Error() != want {
			t.Fatalf("%s: Error() = %q, want %q", c.name, he.Error(), want)
		}
		if errors.Is(err, sync.ErrSHANotFound) != (c.code == contract.CodeSHANotFound) {
			t.Fatalf("%s: errors.Is(ErrSHANotFound) = %v", c.name, errors.Is(err, sync.ErrSHANotFound))
		}
	}

	t.Run("ambiguous abbreviation is sha_not_found", func(t *testing.T) {
		// git rev-parse fails on an ambiguous short id ("Needed a single
		// revision"), which the server maps to sha_not_found.
		g := NewFakeRemote(FakeOptions{})
		seen := map[string]int{}
		var prefix string
		for n := 1; prefix == ""; n++ {
			s := g.Seed(fmt.Sprintf("wiki/concepts/p%d.md", n), []byte("x\n"))
			if _, dup := seen[s[:4]]; dup {
				prefix = s[:4]
			}
			seen[s[:4]] = n
		}
		_, _, err := g.ReadPageAt(wp01Repo, "wiki/concepts/p1.md", prefix)
		he := wp01HTTPError(t, err)
		if he.HTTPStatus() != 404 || he.ErrorCode() != string(contract.CodeSHANotFound) || he.Body != bodyUnknown {
			t.Fatalf("ambiguous %s: %d/%s %s", prefix, he.HTTPStatus(), he.ErrorCode(), he.Body)
		}
	})

	// ReadPage: HEAD content plus last-touch sha; absent -> page_not_found.
	b, sha, err := f.ReadPage(wp01Repo, wp01PathA)
	if err != nil || string(b) != "a2\n" || sha != s3 {
		t.Fatalf("ReadPage(a) = %q, %s, %v", b, sha, err)
	}
	_, _, err = f.ReadPage(wp01Repo, wp01Missing)
	wp01AssertCode(t, err, 404, contract.CodePageNotFound, sync.ErrPageNotFound)
	if he := wp01HTTPError(t, err); he.Body != `{"code":"page_not_found","error":"page not found"}` {
		t.Fatalf("ReadPage 404 body = %s, want the gin (sorted keys) body", he.Body)
	}

	// Returned bytes are copies.
	b[0] = 'X'
	if again, _, _ := f.ReadPage(wp01Repo, wp01PathA); string(again) != "a2\n" {
		t.Fatalf("ReadPage aliased the fake's bytes: %q", again)
	}
}

func TestFakeRemoteOldServer(t *testing.T) {
	f := NewFakeRemote(FakeOptions{OldServer: true})
	s1 := f.Seed(wp01PathA, []byte("a1\n"))
	f.Seed(wp01PathA, []byte("a2\n"))

	if v, caps, known := f.ServerInfo(); v != "" || caps == nil || len(caps) != 0 || known {
		t.Fatalf("before any call ServerInfo = %q, %#v, %v; want \"\", [], false", v, caps, known)
	}

	b, sha, err := f.ReadPageAt(wp01Repo, wp01PathA, s1)
	if b != nil || sha != "" {
		t.Fatalf("old server ReadPageAt returned data: %q, %s", b, sha)
	}
	wp01AssertCode(t, err, 200, contract.CodeServerUnsupported, sync.ErrServerUnsupported)
	he := wp01HTTPError(t, err)
	wantDetails := contract.ServerUnsupportedDetails{Missing: []contract.ServerCapability{contract.SrvPagesAt}}
	if !reflect.DeepEqual(he.ErrorDetails(), wantDetails) {
		t.Fatalf("details = %#v, want %#v", he.ErrorDetails(), wantDetails)
	}

	// The old server serves ReadPage for ?at; an absent path is its codeless
	// JSON 404, which the real client maps to sha_not_found on an ?at read.
	_, _, err = f.ReadPageAt(wp01Repo, wp01Missing, s1)
	wp01AssertCode(t, err, 404, contract.CodeSHANotFound, sync.ErrSHANotFound)

	if v, caps, known := f.ServerInfo(); v != "" || caps == nil || len(caps) != 0 || !known {
		t.Fatalf("after a call ServerInfo = %q, %#v, %v; want \"\", [], true", v, caps, known)
	}

	// Everything else still works.
	if b, sha, err := f.ReadPage(wp01Repo, wp01PathA); err != nil || string(b) != "a2\n" || sha != f.Head() {
		t.Fatalf("ReadPage = %q, %s, %v", b, sha, err)
	}
	_, _, err = f.ReadPage(wp01Repo, wp01Missing)
	wp01AssertCode(t, err, 404, contract.CodePageNotFound, sync.ErrPageNotFound)
}

func TestFakeRemoteServerInfo(t *testing.T) {
	all := []contract.ServerCapability{contract.SrvPagesAt, contract.SrvExpectAbsent, contract.SrvWritePathSHA}
	cases := []struct {
		name     string
		o        FakeOptions
		version  string
		wantCaps []contract.ServerCapability
	}{
		{"current", FakeOptions{}, "0.7.0-fake", all},
		{"version", FakeOptions{ServerVersion: "0.7.1"}, "0.7.1", all},
		{"legacy noop", FakeOptions{LegacyNoopHeadSHA: true}, "0.7.0-fake",
			[]contract.ServerCapability{contract.SrvPagesAt, contract.SrvExpectAbsent}},
		{"no expect_absent", FakeOptions{NoExpectAbsent: true}, "0.7.0-fake",
			[]contract.ServerCapability{contract.SrvPagesAt, contract.SrvWritePathSHA}},
		{"both", FakeOptions{LegacyNoopHeadSHA: true, NoExpectAbsent: true}, "0.7.0-fake",
			[]contract.ServerCapability{contract.SrvPagesAt}},
		{"old", FakeOptions{OldServer: true, ServerVersion: "0.6.6"}, "", []contract.ServerCapability{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := NewFakeRemote(c.o)
			f.Seed(wp01PathA, []byte("a\n")) // not a Remote call
			if v, caps, known := f.ServerInfo(); v != "" || len(caps) != 0 || caps == nil || known {
				t.Fatalf("before any call = %q, %#v, %v", v, caps, known)
			}
			if _, err := f.ListPages(wp01Repo); err != nil {
				t.Fatal(err)
			}
			v, caps, known := f.ServerInfo()
			if v != c.version || !reflect.DeepEqual(caps, c.wantCaps) || !known {
				t.Fatalf("ServerInfo = %q, %#v, %v; want %q, %#v, true", v, caps, known, c.version, c.wantCaps)
			}
			// The caller owns the returned slice.
			if len(caps) > 0 {
				caps[0] = "mutated"
				if _, again, _ := f.ServerInfo(); again[0] == "mutated" {
					t.Fatal("ServerInfo aliased its capability slice")
				}
			}
			if got := f.Requests(); got["ServerInfo"] != 0 {
				t.Fatalf("ServerInfo was counted: %v", got)
			}
		})
	}

	t.Run("a network failure is not a response", func(t *testing.T) {
		f := NewFakeRemote(FakeOptions{})
		f.FailNext("ListPages", &sync.NetworkError{Op: "list pages", Err: errors.New("connection refused")})
		if _, err := f.ListPages(wp01Repo); err == nil {
			t.Fatal("FailNext did not fail")
		}
		if _, _, known := f.ServerInfo(); known {
			t.Fatal("ServerInfo known after a network failure only")
		}
		f.FailNext("ListPages", sync.NewHTTPError(500, []byte(`{"error":"boom"}`), "sync: list pages (500)", nil, false))
		if _, err := f.ListPages(wp01Repo); err == nil {
			t.Fatal("FailNext did not fail")
		}
		if _, _, known := f.ServerInfo(); !known {
			t.Fatal("ServerInfo unknown after an HTTP error response")
		}
	})
}

// TestNoSyncopsTestReadsContractMD keeps CONTRACT.md free to change after
// wave 0: [HUMAN] confirms or vetoes its pending deviations before WP-0.I,
// WP-2.2 rewrites it (Known deviations) and WP-4.1 deletes rows, and none
// of them may edit internal/syncops. So no test under internal/syncops opens
// it; the code it summarizes is pinned by the tests themselves.
func TestNoSyncopsTestReadsContractMD(t *testing.T) {
	needle := "CONTRACT" + ".md" // two literals, so this file never matches
	fset := token.NewFileSet()
	files := 0
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files++
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err == nil && strings.HasSuffix(s, needle) && !strings.ContainsAny(s, " \t\n") {
				t.Errorf("%s: %s names %s; tests under internal/syncops must not read it (WP-2.2 and [HUMAN] edit it after wave 0)",
					fset.Position(lit.Pos()), lit.Value, needle)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("found no test files under internal/syncops")
	}
}

// TestHTTPMappingUnlistedStatuses pins what the client, and therefore
// FakeRemote, does for the statuses E14 does not list: the behaviour that
// CONTRACT.md's HTTP error mapping table and its deviation 1 describe. It
// never reads CONTRACT.md (see TestNoSyncopsTestReadsContractMD).
func TestHTTPMappingUnlistedStatuses(t *testing.T) {
	// sync.NewHTTPError is the one mapping every client method uses.
	probes := []struct {
		status int
		body   string
		want   contract.ErrorCode
	}{
		{100, "", contract.CodeInternal},
		{101, "", contract.CodeInternal},
		{103, "", contract.CodeInternal},
		{300, "", contract.CodeInternal},
		{301, "", contract.CodeInternal},
		{302, "", contract.CodeInternal},
		{304, "", contract.CodeInternal},
		{307, "", contract.CodeInternal},
		{308, "", contract.CodeInternal},
		{405, `{"error":"method not allowed"}`, contract.CodeInternal},
		{409, `{"error":"conflict"}`, contract.CodeInternal},
		{410, "", contract.CodeInternal},
		{413, `{"error":"request body too large"}`, contract.CodeInternal},
		{422, "", contract.CodeInternal},
		{200, "<html>garbled</html>", contract.CodeServerError},
		{204, "", contract.CodeServerError},
		{500, `{"error":"boom"}`, contract.CodeServerError},
		{503, "unavailable", contract.CodeServerError},
	}
	for _, p := range probes {
		if got := sync.NewHTTPError(p.status, []byte(p.body), "m", nil, false).Code; got != p.want {
			t.Errorf("NewHTTPError(%d, %q).Code = %s, want %s", p.status, p.body, got, p.want)
		}
	}
	// A push 409 whose body does not decode is server_error: neither
	// conflict data nor internal.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte("<html>proxy error</html>"))
	}))
	defer srv.Close()
	_, err := sync.NewClient(srv.URL, "test-token").PushPages(wp01Repo, &sync.PushRequest{})
	if he := wp01HTTPError(t, err); he.ErrorCode() != string(contract.CodeServerError) {
		t.Errorf("garbled push 409: code = %s, want server_error", he.ErrorCode())
	}
}

func wp01HasCap(caps []contract.ServerCapability, c contract.ServerCapability) bool {
	for _, x := range caps {
		if x == c {
			return true
		}
	}
	return false
}

func TestFakeRemoteExpectAbsent(t *testing.T) {
	f := NewFakeRemote(FakeOptions{})
	cur := f.Seed(wp01PathA, []byte("theirs\n"))

	for _, content := range []string{"mine\n", "theirs\n"} { // even an equal write is refused
		resp := wp01Push(t, f, sync.PushFile{Path: wp01PathA, Content: content, ExpectAbsent: true})
		want := sync.Conflict{Path: wp01PathA, CurrentSHA: cur, CurrentContent: []byte("theirs\n"), ExpectedParentSHA: ""}
		if len(resp.Conflicts) != 1 || !reflect.DeepEqual(resp.Conflicts[0], want) {
			t.Fatalf("content %q: conflicts = %+v, want [%+v]", content, resp.Conflicts, want)
		}
		if len(resp.Pushed) != 0 || resp.NewHead != cur || f.Head() != cur {
			t.Fatalf("content %q: resp = %+v, head %s", content, resp, f.Head())
		}
	}

	// A new path is created.
	resp := wp01Push(t, f, sync.PushFile{Path: wp01PathB, Content: "b\n", ExpectAbsent: true})
	if len(resp.Conflicts) != 0 || len(resp.Pushed) != 1 || resp.Pushed[0].SHA != f.Head() {
		t.Fatalf("new path: resp = %+v", resp)
	}

	t.Run("NoExpectAbsent ignores the flag", func(t *testing.T) {
		g := NewFakeRemote(FakeOptions{NoExpectAbsent: true})
		cur := g.Seed(wp01PathA, []byte("theirs\n"))
		resp := wp01Push(t, g, sync.PushFile{Path: wp01PathA, Content: "mine\n", ExpectAbsent: true})
		if len(resp.Conflicts) != 0 || len(resp.Pushed) != 1 || resp.Pushed[0].SHA == cur {
			t.Fatalf("resp = %+v", resp)
		}
		if b, _ := g.Content(wp01PathA); string(b) != "mine\n" {
			t.Fatalf("content = %q, want the overwrite", b)
		}
	})
}

func TestFakeRemotePullAllAtHead(t *testing.T) {
	f := NewFakeRemote(FakeOptions{})
	empty, err := f.PullPages(wp01Repo, "")
	if err != nil || empty.NewHead != "" || empty.Files == nil || len(empty.Files) != 0 {
		t.Fatalf("empty repo: PullPages = %+v, %v; want new_head \"\" and files []", empty, err)
	}

	s1 := f.Seed(wp01PathB, []byte("b1\n"))
	s2 := f.Seed(wp01PathA, []byte("a1\n"))
	s3 := f.Seed(wp01PathA, []byte("a2\n"))
	s4 := f.Seed(wp01PathC, []byte("c1\n"))

	all := []sync.PullFile{
		{Path: wp01PathA, Content: "a2\n", SHA: s3},
		{Path: wp01PathB, Content: "b1\n", SHA: s1},
		{Path: wp01PathC, Content: "c1\n", SHA: s4},
	}
	cases := []struct {
		since string
		want  []sync.PullFile
	}{
		{"", all},
		{s1, []sync.PullFile{all[0], all[2]}}, // a (twice) and c changed after s1
		{s2, []sync.PullFile{all[0], all[2]}},
		{s3, []sync.PullFile{all[2]}},
		{s4, []sync.PullFile{}},
		// git resolves an abbreviated or uppercase since.
		{s1[:7], []sync.PullFile{all[0], all[2]}},
		{strings.ToUpper(s3), []sync.PullFile{all[2]}},
		{s4[:7], []sync.PullFile{}},
	}
	for _, c := range cases {
		got, err := f.PullPages(wp01Repo, c.since)
		if err != nil {
			t.Fatalf("since %q: %v", c.since, err)
		}
		if got.NewHead != s4 || got.Files == nil || !reflect.DeepEqual(got.Files, c.want) {
			t.Fatalf("since %q: PullPages = %+v, want new_head %s files %+v", c.since, got, s4, c.want)
		}
	}

	// A since the repo does not know fails like the real server: handler.Pull
	// turns gitstore.ChangedSince's error (ErrUnknownSHA for a non-hex since,
	// git diff's failure for unknown hex) into a 500, which the client maps to
	// server_error. It never silently means "everything".
	errCases := []struct {
		name, since, serverErr string
	}{
		{"unknown full sha", wp01UnknownSHA,
			"gitstore: diff: exit status 128: fatal: bad object " + wp01UnknownSHA + "\n"},
		{"unknown abbreviation", wp01UnknownSHA[:7],
			"gitstore: diff: exit status 128: fatal: bad revision '" + wp01UnknownSHA[:7] + "'\n"},
		{"3 hex is not an abbreviation", s1[:3],
			"gitstore: diff: exit status 128: fatal: bad revision '" + s1[:3] + "'\n"},
		{"not hex", "main", "gitstore: unknown sha"},
	}
	for _, c := range errCases {
		got, err := f.PullPages(wp01Repo, c.since)
		if got != nil {
			t.Fatalf("%s: PullPages(%q) = %+v with an error", c.name, c.since, got)
		}
		he := wp01HTTPError(t, err)
		body, _ := json.Marshal(map[string]string{"error": c.serverErr})
		if he.HTTPStatus() != 500 || he.ErrorCode() != string(contract.CodeServerError) || he.Body != string(body) {
			t.Fatalf("%s: PullPages(%q) = %d/%s %s; want 500/server_error %s",
				c.name, c.since, he.HTTPStatus(), he.ErrorCode(), he.Body, body)
		}
		if want := "sync: pull failed (500): " + string(body); he.Error() != want {
			t.Fatalf("%s: Error() = %q, want %q", c.name, he.Error(), want)
		}
	}

	t.Run("empty repo answers any since with nothing", func(t *testing.T) {
		// gitstore.ChangedSince returns (nil, "", nil) before looking at
		// since when the repo has no commits.
		g := NewFakeRemote(FakeOptions{})
		for _, since := range []string{"", wp01UnknownSHA, "main"} {
			got, err := g.PullPages(wp01Repo, since)
			if err != nil || got.NewHead != "" || got.Files == nil || len(got.Files) != 0 {
				t.Fatalf("since %q: PullPages = %+v, %v", since, got, err)
			}
		}
	})

	t.Run("a page changed back to its since content is not listed", func(t *testing.T) {
		// git diff <since> HEAD compares trees, not the commits between.
		g := NewFakeRemote(FakeOptions{})
		a1 := g.Seed(wp01PathA, []byte("a1\n"))
		g.Seed(wp01PathA, []byte("a2\n"))
		g.Seed(wp01PathA, []byte("a1\n"))
		c := g.Seed(wp01PathC, []byte("c1\n"))
		got, err := g.PullPages(wp01Repo, a1)
		want := []sync.PullFile{{Path: wp01PathC, Content: "c1\n", SHA: c}}
		if err != nil || !reflect.DeepEqual(got.Files, want) {
			t.Fatalf("PullPages(a1) = %+v, %v; want files %+v", got, err, want)
		}
	})
}

func TestFakeRemoteCountsRequests(t *testing.T) {
	f := NewFakeRemote(FakeOptions{})
	if got := f.Requests(); got == nil || len(got) != 0 {
		t.Fatalf("fresh Requests() = %#v, want an empty map", got)
	}
	s := f.Seed(wp01PathA, []byte("a\n"))
	_ = f.Head()
	_, _ = f.Content(wp01PathA)
	_, _, _ = f.ServerInfo()
	if got := f.Requests(); len(got) != 0 {
		t.Fatalf("helpers were counted: %v", got)
	}

	wp01Push(t, f, sync.PushFile{Path: wp01PathB, Content: "b\n"})
	_, _ = f.PullPages(wp01Repo, "")
	_, _ = f.PullPages(wp01Repo, s)
	_, _ = f.ListPages(wp01Repo)
	_, _, _ = f.ReadPage(wp01Repo, wp01PathA)
	_, _, _ = f.ReadPage(wp01Repo, wp01Missing) // errors count too
	_, _, _ = f.ReadPageAt(wp01Repo, wp01PathA, s)

	want := map[string]int{"PushPages": 1, "PullPages": 2, "ListPages": 1, "ReadPage": 2, "ReadPageAt": 1}
	got := f.Requests()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Requests() = %v, want %v", got, want)
	}
	got["ReadPage"] = 99
	if again := f.Requests(); again["ReadPage"] != 2 {
		t.Fatalf("Requests() returned the live map: %v", again)
	}

	t.Run("FailNext", func(t *testing.T) {
		boom := errors.New("boom")
		boom2 := errors.New("boom again")
		f.FailNext("ReadPage", boom)
		f.FailNext("ReadPage", boom2) // queued
		if _, _, err := f.ReadPage(wp01Repo, wp01PathA); !errors.Is(err, boom) {
			t.Fatalf("first ReadPage err = %v, want boom", err)
		}
		if _, _, err := f.ReadPage(wp01Repo, wp01PathA); !errors.Is(err, boom2) {
			t.Fatalf("second ReadPage err = %v, want boom again", err)
		}
		if b, _, err := f.ReadPage(wp01Repo, wp01PathA); err != nil || string(b) != "a\n" {
			t.Fatalf("third ReadPage = %q, %v; want success", b, err)
		}
		if got := f.Requests()["ReadPage"]; got != 5 {
			t.Fatalf("ReadPage count = %d, want 5 (failed calls count)", got)
		}

		f.FailNext("PushPages", boom)
		head := f.Head()
		resp, err := f.PushPages(wp01Repo, &sync.PushRequest{Files: []sync.PushFile{{Path: wp01PathC, Content: "c\n"}}})
		if resp != nil || !errors.Is(err, boom) || f.Head() != head {
			t.Fatalf("failed push = %+v, %v (head %s -> %s)", resp, err, head, f.Head())
		}
		for _, m := range []string{"PullPages", "ListPages", "ReadPageAt"} {
			f.FailNext(m, boom)
		}
		if r, err := f.PullPages(wp01Repo, ""); r != nil || !errors.Is(err, boom) {
			t.Fatalf("PullPages = %+v, %v", r, err)
		}
		if r, err := f.ListPages(wp01Repo); r != nil || !errors.Is(err, boom) {
			t.Fatalf("ListPages = %+v, %v", r, err)
		}
		if b, sha, err := f.ReadPageAt(wp01Repo, wp01PathA, s); b != nil || sha != "" || !errors.Is(err, boom) {
			t.Fatalf("ReadPageAt = %q, %s, %v", b, sha, err)
		}
	})

	t.Run("FailNext rejects an unknown method", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("FailNext(\"ReadPages\") did not panic")
			}
		}()
		f.FailNext("ReadPages", errors.New("typo"))
	})
}

// TestFakeRemoteErrorsMatchRealClient drives a real sync.Client against an
// httptest script of the WP-0.2 handlers (and of an old server) and requires
// the fake to give the identical *sync.HTTPError values and success results.
// The script writes error bodies as gin's c.JSON does: encoding/json over a
// map, so the keys are sorted.
func TestFakeRemoteErrorsMatchRealClient(t *testing.T) {
	const contentA = "---\ntitle: A\n---\nhead content\n"
	// The fake and the server hold the same repo: a.md (commit 1), b.md (2).
	newFake := func(o FakeOptions) (*FakeRemote, string) {
		f := NewFakeRemote(o)
		s1 := f.Seed(wp01PathA, []byte(contentA))
		f.Seed(wp01PathB, []byte("b\n"))
		return f, s1
	}
	_, s1 := newFake(FakeOptions{})
	s2 := wp01SHA(2)
	commits := []string{s1, s2}
	type page struct{ content, sha string }
	// tree[i] is the repo at commits[i].
	tree := []map[string]page{
		{wp01PathA: {contentA, s1}},
		{wp01PathA: {contentA, s1}, wp01PathB: {"b\n", s2}},
	}
	// resolve is git's resolution of a unique, case-insensitive prefix.
	resolve := func(s string) int {
		found := -1
		for i, c := range commits {
			if len(s) >= 4 && strings.HasPrefix(c, strings.ToLower(s)) {
				if found >= 0 {
					return -1
				}
				found = i
			}
		}
		return found
	}

	server := func(old bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !old {
				w.Header().Set("X-Contexo-Server-Version", "0.7.0-fake")
				w.Header().Set("X-Contexo-Capabilities", "pages.at,push.expect_absent,write.path_sha")
			}
			ginJSON := func(status int, h map[string]string) {
				b, err := json.Marshal(h)
				if err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(status)
				_, _ = w.Write(b)
			}
			head := len(commits) - 1

			if r.URL.Path == "/v1/repos/"+wp01Repo+"/sync/pull" {
				// handler.Pull over gitstore.ChangedSince.
				since := r.URL.Query().Get("since")
				from := -1
				switch {
				case since == "":
				case since == commits[head]:
					from = head
				case !wp01IsHexRef(since):
					ginJSON(500, map[string]string{"error": "gitstore: unknown sha"})
					return
				default:
					if from = resolve(since); from < 0 {
						msg := "fatal: bad revision '" + since + "'"
						if len(since) == 40 {
							msg = "fatal: bad object " + since
						}
						ginJSON(500, map[string]string{"error": "gitstore: diff: exit status 128: " + msg + "\n"})
						return
					}
				}
				resp := sync.PullResponse{NewHead: commits[head], Files: []sync.PullFile{}}
				for _, p := range []string{wp01PathA, wp01PathB} {
					cur, ok := tree[head][p]
					if !ok {
						continue
					}
					if from >= 0 { // git diff <since> HEAD: tree content differs
						if prev, ok := tree[from][p]; ok && prev.content == cur.content {
							continue
						}
					}
					resp.Files = append(resp.Files, sync.PullFile{Path: p, Content: cur.content, SHA: cur.sha})
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			path := strings.TrimPrefix(r.URL.Path, "/v1/repos/"+wp01Repo+"/pages/")
			at, hasAt := r.URL.Query()["at"]
			if old || !hasAt { // HEAD read (an old server ignores ?at)
				pg, ok := tree[head][path]
				if !ok {
					if old {
						ginJSON(404, map[string]string{"error": "page not found"})
					} else {
						ginJSON(404, map[string]string{"error": "page not found", "code": "page_not_found"})
					}
					return
				}
				w.Header().Set("X-Page-SHA", pg.sha)
				w.Header().Set("Content-Type", "text/markdown")
				_, _ = w.Write([]byte(pg.content))
				return
			}
			// WP-0.2 handler.ReadPage ?at: ValidSHA before any git call, then
			// the commit, then the path at it.
			if !wp01ValidSHA(at[0]) {
				ginJSON(400, map[string]string{"error": "invalid sha", "code": "invalid_sha"})
				return
			}
			i := resolve(at[0])
			if i < 0 {
				ginJSON(404, map[string]string{"error": "sha not found", "code": "sha_not_found"})
				return
			}
			pg, ok := tree[i][path]
			if !ok {
				ginJSON(404, map[string]string{"error": "page not found at sha", "code": "sha_not_found"})
				return
			}
			w.Header().Set("X-Contexo-At", at[0])
			w.Header().Set("X-Page-SHA", pg.sha)
			_, _ = w.Write([]byte(pg.content))
		}))
	}

	type call func(r syncops.Remote) error
	readPage := func(p string) call {
		return func(r syncops.Remote) error {
			_, _, err := r.ReadPage(wp01Repo, p)
			return err
		}
	}
	readAt := func(p, sha string) call {
		return func(r syncops.Remote) error {
			_, _, err := r.ReadPageAt(wp01Repo, p, sha)
			return err
		}
	}
	pull := func(since string) call {
		return func(r syncops.Remote) error {
			_, err := r.PullPages(wp01Repo, since)
			return err
		}
	}

	cases := []struct {
		name string
		old  bool
		call call
		code contract.ErrorCode
	}{
		{"ReadPage 404 page_not_found", false, readPage(wp01Missing), contract.CodePageNotFound},
		{"ReadPageAt unknown sha", false, readAt(wp01PathA, wp01UnknownSHA), contract.CodeSHANotFound},
		{"ReadPageAt unknown abbreviation", false, readAt(wp01PathA, wp01UnknownSHA[:7]), contract.CodeSHANotFound},
		{"ReadPageAt path absent at sha", false, readAt(wp01PathB, s1), contract.CodeSHANotFound},
		{"ReadPageAt path never written", false, readAt(wp01Missing, s2[:7]), contract.CodeSHANotFound},
		{"ReadPageAt empty sha is usage", false, readAt(wp01PathA, ""), contract.CodeUsage},
		{"ReadPageAt non-hex sha is usage", false, readAt(wp01PathA, "HEAD"), contract.CodeUsage},
		{"PullPages unknown since", false, pull(wp01UnknownSHA), contract.CodeServerError},
		{"PullPages unknown abbreviated since", false, pull(wp01UnknownSHA[:7]), contract.CodeServerError},
		{"PullPages non-hex since", false, pull("main"), contract.CodeServerError},
		{"old server ReadPageAt without X-Contexo-At", true, readAt(wp01PathA, s1), contract.CodeServerUnsupported},
		{"old server ReadPageAt absent path", true, readAt(wp01Missing, s1), contract.CodeSHANotFound},
		{"old server ReadPage 404", true, readPage(wp01Missing), contract.CodePageNotFound},
	}
	sentinels := []error{sync.ErrPageNotFound, sync.ErrSHANotFound, sync.ErrRepoNotFound, sync.ErrServerUnsupported}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := server(c.old)
			defer srv.Close()
			client := sync.NewClient(srv.URL, "test-token")
			fake, _ := newFake(FakeOptions{OldServer: c.old})

			realErr := c.call(client)
			fakeErr := c.call(fake)
			if realErr == nil || fakeErr == nil {
				t.Fatalf("real err = %v, fake err = %v; want both non-nil", realErr, fakeErr)
			}
			rh, fh := wp01HTTPError(t, realErr), wp01HTTPError(t, fakeErr)
			if rh.ErrorCode() != string(c.code) {
				t.Fatalf("real client code = %s, want %s (test script drifted from the client)", rh.ErrorCode(), c.code)
			}
			if fh.Error() != rh.Error() {
				t.Errorf("Error():\nfake %q\nreal %q", fh.Error(), rh.Error())
			}
			if fh.ErrorCode() != rh.ErrorCode() || fh.HTTPStatus() != rh.HTTPStatus() {
				t.Errorf("code/status: fake %s/%d, real %s/%d", fh.ErrorCode(), fh.HTTPStatus(), rh.ErrorCode(), rh.HTTPStatus())
			}
			if !reflect.DeepEqual(fh.ErrorDetails(), rh.ErrorDetails()) {
				t.Errorf("details: fake %#v, real %#v", fh.ErrorDetails(), rh.ErrorDetails())
			}
			for _, s := range sentinels {
				if errors.Is(fakeErr, s) != errors.Is(realErr, s) {
					t.Errorf("errors.Is(%v): fake %v, real %v", s, errors.Is(fakeErr, s), errors.Is(realErr, s))
				}
			}
			if !reflect.DeepEqual(fh, rh) {
				t.Errorf("HTTPError values differ:\nfake %#v\nreal %#v", fh, rh)
			}

			// ServerInfo after the same call agrees too.
			rv, rc, rk := client.ServerInfo()
			fv, fc, fk := fake.ServerInfo()
			if rv != fv || !reflect.DeepEqual(rc, fc) || rk != fk {
				t.Errorf("ServerInfo: fake %q %#v %v, real %q %#v %v", fv, fc, fk, rv, rc, rk)
			}
		})
	}

	// The success paths agree as well, abbreviated and uppercase ids included.
	t.Run("success paths", func(t *testing.T) {
		srv := server(false)
		defer srv.Close()
		client := sync.NewClient(srv.URL, "test-token")
		fake, _ := newFake(FakeOptions{})
		reads := []struct{ path, at string }{
			{wp01PathA, s1}, {wp01PathA, s2}, {wp01PathB, s2},
			{wp01PathA, s2[:7]}, {wp01PathA, s1[:4]}, {wp01PathA, strings.ToUpper(s1)},
			{wp01PathB, strings.ToUpper(s2[:10])},
		}
		for _, rd := range reads {
			rb, rs, rerr := client.ReadPageAt(wp01Repo, rd.path, rd.at)
			fb, fs, ferr := fake.ReadPageAt(wp01Repo, rd.path, rd.at)
			if rerr != nil || ferr != nil || !bytes.Equal(rb, fb) || rs != fs || len(fs) != 40 {
				t.Fatalf("ReadPageAt(%s, %s): real %q %s %v, fake %q %s %v", rd.path, rd.at, rb, rs, rerr, fb, fs, ferr)
			}
		}
		rb, rs, rerr := client.ReadPage(wp01Repo, wp01PathA)
		fb, fs, ferr := fake.ReadPage(wp01Repo, wp01PathA)
		if rerr != nil || ferr != nil || !bytes.Equal(rb, fb) || rs != fs {
			t.Fatalf("ReadPage(a): real %q %s %v, fake %q %s %v", rb, rs, rerr, fb, fs, ferr)
		}
		for _, since := range []string{"", s1, s2, s1[:7], strings.ToUpper(s1), s2[:5]} {
			rp, rerr := client.PullPages(wp01Repo, since)
			fp, ferr := fake.PullPages(wp01Repo, since)
			if rerr != nil || ferr != nil || !reflect.DeepEqual(rp, fp) {
				t.Fatalf("PullPages(%q): real %+v %v, fake %+v %v", since, rp, rerr, fp, ferr)
			}
		}
	})
}

// wp01ValidSHA is gitstore.ValidSHA (WP-0.2): 4 to 64 hex, either case.
func wp01ValidSHA(s string) bool {
	return len(s) >= 4 && wp01IsHexRef(s)
}

// wp01IsHexRef is gitstore.isHexRef: at most 64 hex characters, either case.
func wp01IsHexRef(s string) bool {
	if len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

func TestFakeRemoteConcurrentSafe(t *testing.T) {
	f := NewFakeRemote(FakeOptions{})
	seed := f.Seed(wp01PathA, []byte("a\n"))
	const workers, rounds = 8, 10
	var wg stdsync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			p := fmt.Sprintf("wiki/concepts/w%d.md", w)
			for i := 0; i < rounds; i++ {
				if _, err := f.PushPages(wp01Repo, &sync.PushRequest{
					AuthorName: "Ana", AuthorEmail: "ana@acme.dev",
					Files: []sync.PushFile{{Path: p, Content: fmt.Sprintf("round %d\n", i)}},
				}); err != nil {
					t.Error(err)
					return
				}
				_, _ = f.ListPages(wp01Repo)
				_, _ = f.PullPages(wp01Repo, seed)
				_, _, _ = f.ReadPage(wp01Repo, p)
				_, _, _ = f.ReadPageAt(wp01Repo, wp01PathA, f.Head())
				_, _, _ = f.ServerInfo()
				_ = f.Requests()
				_, _ = f.Content(p)
			}
		}(w)
	}
	wg.Wait()

	if want := wp01SHA(1 + workers*rounds); f.Head() != want {
		t.Fatalf("Head() = %s, want commit %d", f.Head(), 1+workers*rounds)
	}
	got := f.Requests()
	n := workers * rounds
	want := map[string]int{"PushPages": n, "ListPages": n, "PullPages": n, "ReadPage": n, "ReadPageAt": n}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Requests() = %v, want %v", got, want)
	}
	pages, _ := f.ListPages(wp01Repo)
	if len(pages) != 1+workers {
		t.Fatalf("listing has %d pages, want %d", len(pages), 1+workers)
	}
}
