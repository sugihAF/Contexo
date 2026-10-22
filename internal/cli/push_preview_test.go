package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/diff"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

func TestSummarizeDiff_TrimAndCap(t *testing.T) {
	d := &diff.SectionDiff{
		Frontmatter: diff.FrontmatterDiff{Added: []diff.FrontmatterFieldChange{{Field: "tags", To: "x"}}},
		Sections: []diff.SectionChange{
			{Heading: "## A", Status: diff.StatusModified},
			{Heading: "## B", Status: diff.StatusAdded},
			{Heading: "## C", Status: diff.StatusRemoved},
			{Heading: "## D", Status: diff.StatusAdded},
			{Heading: "## E", Status: diff.StatusModified},
		},
	}
	got := summarizeDiff(d)
	// 1 frontmatter + 5 sections = 6 parts, capped to 4 + "+2 more"
	if !strings.Contains(got, "frontmatter") {
		t.Errorf("expected 'frontmatter' in summary: %s", got)
	}
	if !strings.Contains(got, "+2 more") {
		t.Errorf("expected cap suffix '+2 more': %s", got)
	}
}

func TestIsEmptyDiff(t *testing.T) {
	empty := &diff.SectionDiff{Sections: []diff.SectionChange{
		{Heading: "## A", Status: diff.StatusUnchanged},
	}}
	if !isEmptyDiff(empty) {
		t.Error("expected empty diff with only unchanged sections")
	}
	notEmpty := &diff.SectionDiff{Sections: []diff.SectionChange{
		{Heading: "## A", Status: diff.StatusModified},
	}}
	if isEmptyDiff(notEmpty) {
		t.Error("expected non-empty when a section is modified")
	}
	fmChange := &diff.SectionDiff{Frontmatter: diff.FrontmatterDiff{
		Added: []diff.FrontmatterFieldChange{{Field: "tags", To: "x"}},
	}}
	if isEmptyDiff(fmChange) {
		t.Error("expected non-empty when frontmatter changed")
	}
}

func TestComputePushPreview_MixOfNewEditNoChange(t *testing.T) {
	// Server returns:
	//   - new.md     → 404
	//   - same.md    → identical to local
	//   - edit.md    → different from local
	//   - broken.md  → 500 (preview unavailable)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/repos/repo/pages/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/repos/repo/pages/")
		switch path {
		case "new.md":
			w.WriteHeader(http.StatusNotFound)
		case "same.md":
			w.Header().Set("X-Page-SHA", "abc1234")
			_, _ = w.Write([]byte("---\nslug: same\ntype: concept\n---\nbody\n"))
		case "edit.md":
			w.Header().Set("X-Page-SHA", "def5678")
			_, _ = w.Write([]byte("---\nslug: edit\ntype: concept\n---\n## Decision\nold\n"))
		case "broken.md":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("nope"))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := sync.NewClient(srv.URL, "tok")
	// new.md is an update the server no longer has: a 404 still previews
	// as NEW.
	plan := []syncops.PushPlanFile{
		{Kind: contract.WriteUpdate, File: sync.PushFile{Path: "new.md", Content: "---\nslug: new\ntype: concept\n---\nfresh body\n"}},
		{Kind: contract.WriteUpdate, File: sync.PushFile{Path: "same.md", Content: "---\nslug: same\ntype: concept\n---\nbody\n"}},
		{Kind: contract.WriteUpdate, File: sync.PushFile{Path: "edit.md", Content: "---\nslug: edit\ntype: concept\n---\n## Decision\nnew text\n"}},
		{Kind: contract.WriteUpdate, File: sync.PushFile{Path: "broken.md", Content: "---\nslug: broken\ntype: concept\n---\nx\n"}},
	}
	previews := computePushPreview(client, "repo", plan)
	if len(previews) != 4 {
		t.Fatalf("want 4 previews, got %d", len(previews))
	}
	if previews[0].Status != pushStatusNew {
		t.Errorf("new.md: got %v want NEW", previews[0].Status)
	}
	if previews[1].Status != pushStatusNoChange {
		t.Errorf("same.md: got %v want SAME", previews[1].Status)
	}
	if previews[2].Status != pushStatusEdit {
		t.Errorf("edit.md: got %v want EDIT", previews[2].Status)
	}
	if previews[3].Status != pushStatusError {
		t.Errorf("broken.md: got %v want ERROR", previews[3].Status)
	}
}

// A create file has no server version, so its preview is NEW without a
// request; only the update file is read.
func TestComputePushPreview_CreateSkipsRead(t *testing.T) {
	var (
		mu   gosync.Mutex
		hits []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.Path)
		mu.Unlock()
		w.Header().Set("X-Page-SHA", "abc1234")
		_, _ = w.Write([]byte("---\nslug: old\ntype: concept\n---\n## Decision\nold\n"))
	}))
	defer srv.Close()

	client := sync.NewClient(srv.URL, "tok")
	plan := []syncops.PushPlanFile{
		{Kind: contract.WriteCreate, File: sync.PushFile{Path: "wiki/concepts/new.md",
			Content: "---\nslug: new\ntype: concept\n---\nfresh\n", ExpectAbsent: true}},
		{Kind: contract.WriteUpdate, File: sync.PushFile{Path: "wiki/concepts/old.md",
			Content: "---\nslug: old\ntype: concept\n---\n## Decision\nnew\n", ParentSHA: "abc1234"}},
	}
	previews := computePushPreview(client, "repo", plan)
	if len(previews) != 2 {
		t.Fatalf("want 2 previews, got %d", len(previews))
	}
	if previews[0].Path != "wiki/concepts/new.md" || previews[0].Status != pushStatusNew {
		t.Errorf("create preview = %+v, want NEW for wiki/concepts/new.md", previews[0])
	}
	if previews[0].LineHint != "  [NEW]   wiki/concepts/new.md" {
		t.Errorf("create line hint = %q", previews[0].LineHint)
	}
	if previews[1].Status != pushStatusEdit {
		t.Errorf("update preview status = %v, want EDIT", previews[1].Status)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"/v1/repos/repo/pages/wiki/concepts/old.md"}
	if !reflect.DeepEqual(hits, want) {
		t.Errorf("server requests = %q, want only %q (a create file is never read)", hits, want)
	}
}

// TestComputePushPreview_ConcurrencyAtMost4 pins E12: every per-page fan-out
// in ctx, the preview's server reads included, keeps at most 4 requests in
// flight.
func TestComputePushPreview_ConcurrencyAtMost4(t *testing.T) {
	var (
		mu             gosync.Mutex
		inFlight, peak int
	)
	release := make(chan struct{})
	arrived := make(chan struct{}, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()
		arrived <- struct{}{}
		<-release
		mu.Lock()
		inFlight--
		mu.Unlock()
		w.Header().Set("X-Page-SHA", "abc1234")
		_, _ = w.Write([]byte("---\nslug: p\ntype: concept\n---\nbody\n"))
	}))
	defer srv.Close()

	plan := make([]syncops.PushPlanFile, 12)
	for i := range plan {
		plan[i] = syncops.PushPlanFile{Kind: contract.WriteUpdate, File: sync.PushFile{
			Path: "wiki/concepts/p" + string(rune('a'+i)) + ".md", Content: "---\nslug: p\ntype: concept\n---\nnew\n"}}
	}
	done := make(chan []pushPreview)
	go func() { done <- computePushPreview(sync.NewClient(srv.URL, "tok"), "repo", plan) }()

	// Let the first wave arrive, give any extra request time to show up,
	// then release everything.
	for i := 0; i < 4; i++ {
		<-arrived
	}
	extra := false
	select {
	case <-arrived:
		extra = true
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if got := len(<-done); got != len(plan) {
		t.Fatalf("previews = %d, want %d", got, len(plan))
	}
	mu.Lock()
	defer mu.Unlock()
	if extra || peak > 4 {
		t.Fatalf("preview reads in flight peaked at %d (extra=%v), want at most 4 (E12)", peak, extra)
	}
}

func TestRenderPreview_MarkersAndEditFlag(t *testing.T) {
	d := diff.SectionDiff{Sections: []diff.SectionChange{
		{Heading: "## Decision", Status: diff.StatusModified},
	}}
	previews := []pushPreview{
		{Path: "new.md", Status: pushStatusNew, LineHint: "  [NEW]   new.md"},
		{Path: "edit.md", Status: pushStatusEdit, Diff: &d, LineHint: "  [EDIT]  edit.md  (~ Decision)"},
		{Path: "same.md", Status: pushStatusNoChange, LineHint: "  [SAME]  same.md"},
	}
	var buf bytes.Buffer
	hasEdits := renderPreview(&buf, "repo", previews, false)
	if !hasEdits {
		t.Error("expected hasEdits=true when an EDIT preview is present")
	}
	out := buf.String()
	for _, want := range []string{"[NEW]", "[EDIT]", "[SAME]", "edit.md  (~ Decision)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRenderPreview_NoEditsReturnsFalse(t *testing.T) {
	previews := []pushPreview{
		{Path: "new.md", Status: pushStatusNew, LineHint: "  [NEW]   new.md"},
		{Path: "same.md", Status: pushStatusNoChange, LineHint: "  [SAME]  same.md"},
	}
	var buf bytes.Buffer
	if renderPreview(&buf, "repo", previews, false) {
		t.Error("expected hasEdits=false when no EDIT entries")
	}
}

func TestConfirm_YesNoVariants(t *testing.T) {
	cases := map[string]bool{
		"y\n":     true,
		"Y\n":     true,
		"yes\n":   true,
		"YES\n":   true,
		"n\n":     false,
		"\n":      false, // default is N
		"maybe\n": false,
	}
	for in, want := range cases {
		var out bytes.Buffer
		got, err := confirm(strings.NewReader(in), &out, "Proceed?")
		if err != nil {
			t.Errorf("[input=%q] err: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("[input=%q] got %v want %v", in, got, want)
		}
	}
}

func TestReadPage_NotFoundErrType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	client := sync.NewClient(srv.URL, "tok")
	_, _, err := client.ReadPage("repo", "x.md")
	if !errors.Is(err, sync.ErrPageNotFound) {
		t.Errorf("expected ErrPageNotFound, got %v", err)
	}
}
