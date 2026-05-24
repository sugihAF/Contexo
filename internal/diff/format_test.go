package diff

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToJSON_RoundTrip(t *testing.T) {
	d := SectionDiff{
		FromSHA: "abc1234",
		ToSHA:   "def5678",
		Frontmatter: FrontmatterDiff{
			Changed: []FrontmatterFieldChange{{Field: "reasoning_summary", From: "old", To: "new"}},
			Added:   []FrontmatterFieldChange{{Field: "tags", To: "stripe-fee"}},
		},
		Sections: []SectionChange{
			{Heading: "## Decision", Status: StatusModified, From: "a", To: "b", LineDiff: "- a\n+ b"},
			{Heading: "## Why", Status: StatusUnchanged},
			{Heading: "## New", Status: StatusAdded, To: "fresh content"},
		},
	}
	js, err := d.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	var back SectionDiff
	if err := json.Unmarshal(js, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.FromSHA != d.FromSHA || back.ToSHA != d.ToSHA {
		t.Fatalf("SHA mismatch: %+v vs %+v", back, d)
	}
	if len(back.Sections) != 3 {
		t.Fatalf("want 3 sections, got %d", len(back.Sections))
	}
}

func TestToText_AllStatuses(t *testing.T) {
	d := SectionDiff{
		FromSHA: "abc1234",
		ToSHA:   "def5678",
		Frontmatter: FrontmatterDiff{
			Changed: []FrontmatterFieldChange{{Field: "reasoning_summary", From: "old", To: "new"}},
			Added:   []FrontmatterFieldChange{{Field: "tags", To: "stripe-fee"}},
			Removed: []FrontmatterFieldChange{{Field: "tags", From: "old-tag"}},
		},
		Sections: []SectionChange{
			{Heading: "## Decision", Status: StatusModified, LineDiff: "- a\n+ b\n- c\n+ d"},
			{Heading: "## Why", Status: StatusUnchanged},
			{Heading: "## New", Status: StatusAdded, To: "line1\nline2\nline3"},
			{Heading: "## Old", Status: StatusRemoved, From: "x\ny"},
		},
	}
	out := d.ToText("stripe-subscription")
	for _, want := range []string{
		"diff: stripe-subscription   abc1234..def5678",
		"~ reasoning_summary",
		"- old",
		"+ new",
		"+ tags: stripe-fee",
		"- tags: old-tag",
		"~ ## Decision",
		"= ## Why",
		"+ ## New (3 lines added)",
		"- ## Old (2 lines removed)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q. Full:\n%s", want, out)
		}
	}
}

func TestToText_EmptyDiff(t *testing.T) {
	d := SectionDiff{FromSHA: "abc1234", ToSHA: "def5678"}
	out := d.ToText("x")
	if !strings.HasPrefix(out, "diff: x   abc1234..def5678") {
		t.Fatalf("empty text output unexpected: %q", out)
	}
}

func TestToText_MultilineFrontmatterValue(t *testing.T) {
	d := SectionDiff{
		FromSHA: "a1234567",
		ToSHA:   "b1234567",
		Frontmatter: FrontmatterDiff{
			Changed: []FrontmatterFieldChange{
				{Field: "reasoning_summary", From: "single line", To: "first line\nsecond line\nthird line"},
			},
		},
	}
	out := d.ToText("p")
	// Should NOT have raw newlines inside the marker line; instead the value
	// should be rendered with the YAML-style block marker `|` and continuation
	// lines indented under the prefix.
	if strings.Contains(out, "+ first line\nsecond line") {
		t.Errorf("multiline value should be block-quoted, not dumped inline:\n%s", out)
	}
	if !strings.Contains(out, "      + |\n") {
		t.Errorf("expected block marker `+ |` for the new multiline value:\n%s", out)
	}
	if !strings.Contains(out, "second line\n") {
		t.Errorf("expected continuation line indented:\n%s", out)
	}
}

func TestToText_ParseFallback(t *testing.T) {
	d := SectionDiff{
		FromSHA:       "a",
		ToSHA:         "b",
		ParseFallback: true,
		Preamble:      &SectionChange{Heading: "_preamble", Status: StatusModified, LineDiff: "- old\n+ new"},
	}
	out := d.ToText("")
	if !strings.Contains(out, "unparseable") {
		t.Fatalf("expected fallback note in output: %s", out)
	}
}
