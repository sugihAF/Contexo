package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/diff"
	"github.com/sugihAF/contexo/internal/sync"
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
	files := []sync.PushFile{
		{Path: "new.md", Content: "---\nslug: new\ntype: concept\n---\nfresh body\n"},
		{Path: "same.md", Content: "---\nslug: same\ntype: concept\n---\nbody\n"},
		{Path: "edit.md", Content: "---\nslug: edit\ntype: concept\n---\n## Decision\nnew text\n"},
		{Path: "broken.md", Content: "---\nslug: broken\ntype: concept\n---\nx\n"},
	}
	previews := computePushPreview(client, "repo", files)
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
		"y\n":   true,
		"Y\n":   true,
		"yes\n": true,
		"YES\n": true,
		"n\n":   false,
		"\n":    false, // default is N
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
