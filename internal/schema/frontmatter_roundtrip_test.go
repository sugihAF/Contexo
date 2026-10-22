package schema_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// wp16RoundTrip returns SerializePage(ParsePage(in)).
func wp16RoundTrip(t *testing.T, in string) string {
	t.Helper()
	p, err := schema.ParsePage([]byte(in))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	out, err := schema.SerializePage(p)
	if err != nil {
		t.Fatalf("SerializePage: %v", err)
	}
	return string(out)
}

// Inputs of TestSerialize_ByteIdenticalForSchemaOnlyPages: pages that use
// only schema keys.
const (
	wp16InConceptBlock = "---\nschema: ctx.page.v1\nslug: retry-policy\ntype: concept\n" +
		"author: alice\nagent: claude-code\ncreated: 2026-10-01T09:00:00Z\n" +
		"updated: 2026-10-02T10:30:00Z\nparent_sha: \"\"\ntags:\n  - payments\n  - retries\n" +
		"---\n# Retry policy\n\nBack off exponentially.\n"
	wp16InEntityFlow = "---\nschema: ctx.page.v1\nslug: ledger-service\ntype: entity\n" +
		"author: bob\ncreated: 2026-10-01T09:00:00Z\nupdated: 2026-10-01T09:00:00Z\n" +
		"parent_sha: 3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f\ntags: [ledger, core]\n" +
		"---\n# Ledger service\n\nOwns balances.\n"
	wp16InAnalysisLinks = "---\nschema: ctx.page.v1\nslug: q3-chargebacks\ntype: analysis\n" +
		"author: carol\nagent: cursor\ncreated: 2026-09-30T00:00:00Z\nupdated: 2026-09-30T12:00:00Z\n" +
		"parent_sha: \"\"\nsources:\n  - 2026-09-29-chargeback-session\n" +
		"related: [retry-policy, ledger-service]\ntags: [risk]\n" +
		"reasoning_summary: Compared three quarters; Q3 spike is one merchant.\n" +
		"---\n# Q3 chargebacks\n\n## Finding\nOne merchant.\n"
	wp16InSourceNoAgentEmptyBody = "---\nschema: ctx.page.v1\nslug: 2026-09-29-chargeback-session\n" +
		"type: source\nauthor: carol\ncreated: 2026-09-29T08:00:00Z\n" +
		"updated: 2026-09-29T08:00:00Z\nparent_sha: \"\"\n---\n"
	wp16InCRLF = "---\r\nschema: ctx.page.v1\r\nslug: webhook-signing\r\ntype: concept\r\n" +
		"author: dan\r\ncreated: 2026-08-01T00:00:00Z\r\nupdated: 2026-08-02T00:00:00Z\r\n" +
		"parent_sha: \"\"\r\ntags:\r\n  - webhooks\r\n---\r\n# Webhook signing\r\n\r\nHMAC-SHA256.\r\n"
	wp16InEndTrim = "---\nschema: ctx.page.v1\nslug: empty-body\ntype: entity\nauthor: erin\n" +
		"created: 2026-07-01T00:00:00Z\nupdated: 2026-07-01T00:00:00Z\nparent_sha: \"\"\n---"
	wp16InUnicodeBody = "---\nschema: ctx.page.v1\nslug: i18n-notes\ntype: concept\nauthor: zoë\n" +
		"created: 2026-06-01T00:00:00Z\nupdated: 2026-06-01T00:00:00Z\nparent_sha: \"\"\n" +
		"tags: [i18n]\n---\n# Café notes — 漢字\n\nEmoji 👩‍💻 and accents: naïve, façade.\n"
	wp16InSummaryLiteral = "---\nschema: ctx.page.v1\nslug: rate-limits\ntype: concept\nauthor: frank\n" +
		"agent: claude-code\ncreated: 2026-05-01T00:00:00Z\nupdated: 2026-05-02T00:00:00Z\n" +
		"parent_sha: 4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f70\n" +
		"reasoning_summary: |\n  Chose a token bucket.\n  Rejected a fixed window.\n" +
		"---\n# Rate limits\n\n300/min per IP.\n"
	wp16InSummaryQuoted = "---\nschema: ctx.page.v1\nslug: auth-flow\ntype: concept\nauthor: gina\n" +
		"created: 2026-04-01T00:00:00Z\nupdated: 2026-04-01T00:00:00Z\nparent_sha: \"\"\n" +
		"reasoning_summary: \"Line one.\\nLine two: \\\"quoted\\\".\"\n---\n# Auth flow\n"
	wp16InReorderedDates = "---\ntype: analysis\nslug: billing-comparison\nschema: ctx.page.v1\n" +
		"tags: [billing, stripe]\nauthor: hank\ncreated: 2026-03-01\n" +
		"updated: 2026-03-01T17:04:05.123456789+07:00\nrelated: [ledger-service]\n" +
		"---\n# Billing comparison\n"
)

// Goldens of TestSerialize_ByteIdenticalForSchemaOnlyPages: printed from
// SerializePage(ParsePage(in)) of the code before PageFrontmatter.Extra
// existed (WP-1.6 commit 1). Never edit them: a schema-only page must keep
// these exact bytes, so no stored base hash changes.
const (
	wp16GoldenConceptBlockTags = "---\n" +
		"schema: ctx.page.v1\n" +
		"slug: retry-policy\n" +
		"type: concept\n" +
		"author: alice\n" +
		"agent: claude-code\n" +
		"created: 2026-10-01T09:00:00Z\n" +
		"updated: 2026-10-02T10:30:00Z\n" +
		"parent_sha: \"\"\n" +
		"tags:\n" +
		"  - payments\n" +
		"  - retries\n" +
		"---\n" +
		"# Retry policy\n" +
		"\n" +
		"Back off exponentially.\n"
	wp16GoldenEntityFlowTagsParentSha = "---\n" +
		"schema: ctx.page.v1\n" +
		"slug: ledger-service\n" +
		"type: entity\n" +
		"author: bob\n" +
		"created: 2026-10-01T09:00:00Z\n" +
		"updated: 2026-10-01T09:00:00Z\n" +
		"parent_sha: 3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f\n" +
		"tags:\n" +
		"  - ledger\n" +
		"  - core\n" +
		"---\n" +
		"# Ledger service\n" +
		"\n" +
		"Owns balances.\n"
	wp16GoldenAnalysisSourcesRelated = "---\n" +
		"schema: ctx.page.v1\n" +
		"slug: q3-chargebacks\n" +
		"type: analysis\n" +
		"author: carol\n" +
		"agent: cursor\n" +
		"created: 2026-09-30T00:00:00Z\n" +
		"updated: 2026-09-30T12:00:00Z\n" +
		"parent_sha: \"\"\n" +
		"sources:\n" +
		"  - 2026-09-29-chargeback-session\n" +
		"related:\n" +
		"  - retry-policy\n" +
		"  - ledger-service\n" +
		"tags:\n" +
		"  - risk\n" +
		"reasoning_summary: Compared three quarters; Q3 spike is one merchant.\n" +
		"---\n" +
		"# Q3 chargebacks\n" +
		"\n" +
		"## Finding\n" +
		"One merchant.\n"
	wp16GoldenSourceNoAgentEmptyBody = "---\n" +
		"schema: ctx.page.v1\n" +
		"slug: 2026-09-29-chargeback-session\n" +
		"type: source\n" +
		"author: carol\n" +
		"created: 2026-09-29T08:00:00Z\n" +
		"updated: 2026-09-29T08:00:00Z\n" +
		"parent_sha: \"\"\n" +
		"---\n"
	wp16GoldenCrlf = "---\n" +
		"schema: ctx.page.v1\n" +
		"slug: webhook-signing\n" +
		"type: concept\n" +
		"author: dan\n" +
		"created: 2026-08-01T00:00:00Z\n" +
		"updated: 2026-08-02T00:00:00Z\n" +
		"parent_sha: \"\"\n" +
		"tags:\n" +
		"  - webhooks\n" +
		"---\n" +
		"# Webhook signing\n" +
		"\n" +
		"HMAC-SHA256.\n"
	wp16GoldenEndTrimEmptyBody = "---\n" +
		"schema: ctx.page.v1\n" +
		"slug: empty-body\n" +
		"type: entity\n" +
		"author: erin\n" +
		"created: 2026-07-01T00:00:00Z\n" +
		"updated: 2026-07-01T00:00:00Z\n" +
		"parent_sha: \"\"\n" +
		"---\n"
	wp16GoldenUnicodeBody = "---\n" +
		"schema: ctx.page.v1\n" +
		"slug: i18n-notes\n" +
		"type: concept\n" +
		"author: zoë\n" +
		"created: 2026-06-01T00:00:00Z\n" +
		"updated: 2026-06-01T00:00:00Z\n" +
		"parent_sha: \"\"\n" +
		"tags:\n" +
		"  - i18n\n" +
		"---\n" +
		"# Café notes — 漢字\n" +
		"\n" +
		"Emoji 👩\u200d💻 and accents: naïve, façade.\n"
	wp16GoldenSummaryLiteralParentSha = "---\n" +
		"schema: ctx.page.v1\n" +
		"slug: rate-limits\n" +
		"type: concept\n" +
		"author: frank\n" +
		"agent: claude-code\n" +
		"created: 2026-05-01T00:00:00Z\n" +
		"updated: 2026-05-02T00:00:00Z\n" +
		"parent_sha: 4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f70\n" +
		"reasoning_summary: |-\n" +
		"  Chose a token bucket.\n" +
		"  Rejected a fixed window.\n" +
		"---\n" +
		"# Rate limits\n" +
		"\n" +
		"300/min per IP.\n"
	wp16GoldenSummaryQuotedMultiline = "---\n" +
		"schema: ctx.page.v1\n" +
		"slug: auth-flow\n" +
		"type: concept\n" +
		"author: gina\n" +
		"created: 2026-04-01T00:00:00Z\n" +
		"updated: 2026-04-01T00:00:00Z\n" +
		"parent_sha: \"\"\n" +
		"reasoning_summary: |-\n" +
		"  Line one.\n" +
		"  Line two: \"quoted\".\n" +
		"---\n" +
		"# Auth flow\n"
	wp16GoldenReorderedKeysDates = "---\n" +
		"schema: ctx.page.v1\n" +
		"slug: billing-comparison\n" +
		"type: analysis\n" +
		"author: hank\n" +
		"created: 2026-03-01T00:00:00Z\n" +
		"updated: 2026-03-01T17:04:05.123456789+07:00\n" +
		"parent_sha: \"\"\n" +
		"related:\n" +
		"  - ledger-service\n" +
		"tags:\n" +
		"  - billing\n" +
		"  - stripe\n" +
		"---\n" +
		"# Billing comparison\n"
)

func TestSerialize_ByteIdenticalForSchemaOnlyPages(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"concept_block_tags", wp16InConceptBlock, wp16GoldenConceptBlockTags},
		{"entity_flow_tags_parent_sha", wp16InEntityFlow, wp16GoldenEntityFlowTagsParentSha},
		{"analysis_sources_related", wp16InAnalysisLinks, wp16GoldenAnalysisSourcesRelated},
		{"source_no_agent_empty_body", wp16InSourceNoAgentEmptyBody, wp16GoldenSourceNoAgentEmptyBody},
		{"crlf", wp16InCRLF, wp16GoldenCrlf},
		{"end_trim_empty_body", wp16InEndTrim, wp16GoldenEndTrimEmptyBody},
		{"unicode_body", wp16InUnicodeBody, wp16GoldenUnicodeBody},
		{"summary_literal_parent_sha", wp16InSummaryLiteral, wp16GoldenSummaryLiteralParentSha},
		{"summary_quoted_multiline", wp16InSummaryQuoted, wp16GoldenSummaryQuotedMultiline},
		{"reordered_keys_dates", wp16InReorderedDates, wp16GoldenReorderedKeysDates},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wp16RoundTrip(t, c.in); got != c.want {
				t.Errorf("SerializePage(ParsePage(in)) changed bytes:\n got %s\nwant %s",
					strconv.Quote(got), strconv.Quote(c.want))
			}
			if strings.Contains(c.want, "\r") {
				t.Errorf("golden contains CR: %q", c.want)
			}
		})
	}
}

// wp16BOM is the UTF-8 encoding of U+FEFF.
const wp16BOM = "\xef\xbb\xbf"

// wp16Head is the schema part of the pages below, in serialized form.
const wp16Head = "---\nschema: ctx.page.v1\nslug: x\ntype: concept\nauthor: a\n" +
	"created: 2026-01-01T00:00:00Z\nupdated: 2026-01-01T00:00:00Z\nparent_sha: \"\"\n"

func TestRoundTrip_PreservesUnknownKeys(t *testing.T) {
	in := "---\ntitle: My Page\nschema: ctx.page.v1\nslug: x\ntype: concept\nauthor: a\n" +
		"created: 2026-01-01T00:00:00Z\nupdated: 2026-01-01T00:00:00Z\n" +
		"meta:\n  owner: bob\n  count: 3\naliases: [a, b]\ngone:\n" +
		"reviewed: \"2026-01-01\"\nseen: 2026-01-02\n---\n# X\n"
	// Schema keys in struct order, then the unknown keys sorted. A quoted
	// date-like string stays a quoted string; an unquoted date decodes as a
	// timestamp and re-encodes in RFC 3339.
	want := wp16Head +
		"aliases:\n  - a\n  - b\n" +
		"gone: null\n" +
		"meta:\n  count: 3\n  owner: bob\n" +
		"reviewed: \"2026-01-01\"\n" +
		"seen: 2026-01-02T00:00:00Z\n" +
		"title: My Page\n" +
		"---\n# X\n"

	p, err := schema.ParsePage([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	extra := p.Frontmatter.Extra
	if extra["title"] != "My Page" || extra["reviewed"] != "2026-01-01" {
		t.Errorf("string extras: %#v", extra)
	}
	if !reflect.DeepEqual(extra["meta"], map[string]any{"owner": "bob", "count": 3}) {
		t.Errorf("nested map: %#v", extra["meta"])
	}
	if !reflect.DeepEqual(extra["aliases"], []any{"a", "b"}) {
		t.Errorf("list: %#v", extra["aliases"])
	}
	if v, ok := extra["gone"]; !ok || v != nil {
		t.Errorf("null value: %#v present=%v", v, ok)
	}
	if _, ok := extra["seen"].(time.Time); !ok {
		t.Errorf("unquoted date: %#v, want time.Time", extra["seen"])
	}

	got := wp16RoundTrip(t, in)
	if got != want {
		t.Errorf("first pass:\n got %s\nwant %s", strconv.Quote(got), strconv.Quote(want))
	}
	if again := wp16RoundTrip(t, got); again != got {
		t.Errorf("second pass is not a fixed point:\n got %s\nwant %s", strconv.Quote(again), strconv.Quote(got))
	}
}

// yaml.v3 sorts map keys with a natural-order comparator that is not a
// strict order for every key set (digit runs past int64, NaN keys), so a
// directly encoded map can come out in a different order on every call.
func TestSerialize_ExtraKeyOrderDeterministic(t *testing.T) {
	in := wp16Head + "\"9223372036854775808\": 1\n01x: 0\n9é: 2\n" +
		"meta:\n  .nan: n\n  0: z\n  1: o\n---\n# X\n"
	want := wp16Head + "01x: 0\n\"9223372036854775808\": 1\n9é: 2\n" +
		"meta:\n  .nan: \"n\"\n  0: z\n  1: o\n---\n# X\n"
	p, err := schema.ParsePage([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		out, err := schema.SerializePage(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != want {
			t.Fatalf("call %d:\n got %s\nwant %s", i, strconv.Quote(string(out)), strconv.Quote(want))
		}
	}
	if again := wp16RoundTrip(t, want); again != want {
		t.Errorf("not a fixed point: %s", strconv.Quote(again))
	}
}

func TestSerialize_ExtraSchemaKeyIsError(t *testing.T) {
	p, err := schema.ParsePage([]byte(wp16Head + "---\n# X\n"))
	if err != nil {
		t.Fatal(err)
	}
	p.Frontmatter.Extra = map[string]any{"title": "T", "slug": "y"}
	if out, err := schema.SerializePage(p); err == nil || !strings.Contains(err.Error(), `"slug"`) {
		t.Errorf("SerializePage with Extra[slug] = %q, %v; want an error naming slug", out, err)
	}
}

func TestRoundTrip_QuotedMergeKeyStaysString(t *testing.T) {
	in := wp16Head + "\"<<\": q\nm: \"<<\"\n---\n# X\n"
	got := wp16RoundTrip(t, in)
	if got != in {
		t.Errorf("first pass:\n got %s\nwant %s", strconv.Quote(got), strconv.Quote(in))
	}
	p, err := schema.ParsePage([]byte(got))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if want := map[string]any{"<<": "q", "m": "<<"}; !reflect.DeepEqual(p.Frontmatter.Extra, want) {
		t.Errorf("Extra = %#v, want %#v", p.Frontmatter.Extra, want)
	}
}

func TestParse_AcceptsBOM(t *testing.T) {
	plain, err := schema.ParsePage([]byte(wp16InConceptBlock))
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(wp16InConceptBlock, "\n", "\r\n")
	for name, in := range map[string]string{
		"bom_lf":   wp16BOM + wp16InConceptBlock,
		"bom_crlf": wp16BOM + crlf,
	} {
		p, err := schema.ParsePage([]byte(in))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(p, plain) {
			t.Errorf("%s: parsed %#v, want %#v", name, p, plain)
		}
	}
}

func TestParse_OnlyOneBOMStripped(t *testing.T) {
	_, err := schema.ParsePage([]byte(wp16BOM + wp16BOM + wp16InConceptBlock))
	if err == nil || !strings.Contains(err.Error(), "missing frontmatter") {
		t.Errorf("two BOMs: err = %v, want the missing frontmatter error", err)
	}
}

func TestParse_MidFileFEFFUntouched(t *testing.T) {
	in := wp16Head + "---\n# X\n\nzero" + wp16BOM + "width\n"
	p, err := schema.ParsePage([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Body, "zero"+wp16BOM+"width") {
		t.Errorf("body lost its U+FEFF: %q", p.Body)
	}
	if got := wp16RoundTrip(t, in); got != in {
		t.Errorf("round trip changed a page with a mid-file U+FEFF: %q", got)
	}
}

// Normalize runs on both the working file and the base, so unknown keys in
// a non-canonical form never make a pushed page look modified.
func TestNormalize_ExtraNoFalseModified(t *testing.T) {
	local := []byte("---\nschema: ctx.page.v1\ntitle: T\nslug: x\ntype: concept\nauthor: a\n" +
		"created: 2026-01-01\nupdated: 2026-01-01T00:00:00Z\ntags: [a]\n" +
		"meta: {b: 2, a: 1}\nseen: 2026-01-02\n---\n# X\n")
	base, err := syncstate.Normalize(local) // what push sends and stores as the base
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(base), "\ntitle: T\n") || !strings.Contains(string(base), "\nmeta:\n  a: 1\n  b: 2\n") {
		t.Errorf("Normalize dropped unknown keys: %q", base)
	}
	if !syncstate.Equal(local, base) || syncstate.Hash(local) != syncstate.Hash(base) {
		t.Errorf("local %s != base %s", syncstate.Hash(local), syncstate.Hash(base))
	}
	if again, err := syncstate.Normalize(base); err != nil || string(again) != string(base) {
		t.Errorf("Normalize(base) = %q, %v; want base", again, err)
	}
	edited := []byte(strings.Replace(string(local), "title: T", "title: U", 1))
	if syncstate.Equal(edited, base) {
		t.Error("editing an unknown key is not a change")
	}
}

// pagestore and syncstate.Normalize strip one BOM before ParsePage, which now
// strips another: a file with two leading BOMs parses on those paths, and
// one with three does not.
func TestNormalize_TwoBOMsParseThroughStripPaths(t *testing.T) {
	two := []byte(wp16BOM + wp16BOM + wp16InConceptBlock)
	n, err := syncstate.Normalize(two)
	if err != nil {
		t.Fatalf("Normalize(two BOMs): %v", err)
	}
	if string(n) != wp16GoldenConceptBlockTags || syncstate.Hash(two) != syncstate.Hash([]byte(wp16InConceptBlock)) {
		t.Errorf("Normalize(two BOMs) = %q", n)
	}
	if _, err := syncstate.Normalize([]byte(wp16BOM + string(two))); err == nil {
		t.Error("Normalize(three BOMs) succeeded")
	}

	root := t.TempDir()
	rel := "wiki/concepts/retry-policy.md"
	if err := os.MkdirAll(filepath.Join(root, "wiki", "concepts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), two, 0o644); err != nil {
		t.Fatal(err)
	}
	store := &pagestore.Store{Root: root}
	if p, err := store.Read(rel); err != nil || p.Frontmatter.Slug != "retry-policy" {
		t.Errorf("pagestore.Read(two BOMs) = %v, %v", p, err)
	}
	var walked []string
	if err := store.WalkAll(func(f pagestore.WalkedFile) error {
		if f.ParseErr != nil {
			t.Errorf("WalkAll %s: %v", f.Rel, f.ParseErr)
		}
		walked = append(walked, f.Rel)
		return nil
	}); err != nil || len(walked) != 1 {
		t.Errorf("WalkAll = %v, %v", walked, err)
	}
}

// FuzzSerialize_Deterministic checks that every page that parses also
// serializes, to the same bytes on every call, and that Normalize of it is
// idempotent with a stable Hash. go test runs only the seeds; run it with
// -fuzz to search further.
func FuzzSerialize_Deterministic(f *testing.F) {
	f.Add([]byte(wp16InConceptBlock))
	f.Add([]byte(wp16InReorderedDates))
	f.Add([]byte(wp16Head + "title: T\nmeta: {b: [1, {c: d}], a: ~}\n---\n# X\n"))
	f.Add([]byte(wp16Head + "\"9223372036854775808\": 1\n01x: 0\nm:\n  .nan: n\n  0: z\n  1: o\n---\n"))
	f.Add([]byte(wp16Head + "\"<<\": q\nraw: !!binary /9j/\nz: |+\n  x\n\n---\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := schema.ParsePage(b)
		if err != nil {
			return
		}
		first, err := schema.SerializePage(p)
		if err != nil {
			t.Fatalf("SerializePage of a parsed page: %v", err)
		}
		for i := 0; i < 8; i++ {
			if again, _ := schema.SerializePage(p); string(again) != string(first) {
				t.Fatalf("SerializePage not deterministic:\n%q\n%q", first, again)
			}
		}
		n1, err := syncstate.Normalize(b)
		if err != nil {
			return // e.g. no fixed point within the pass cap: hashes raw
		}
		n2, err := syncstate.Normalize(n1)
		if err != nil || string(n2) != string(n1) {
			t.Fatalf("Normalize not idempotent: %q -> %q (%v)", n1, n2, err)
		}
		if syncstate.Hash(n1) != syncstate.Hash(b) {
			t.Fatalf("Hash(Normalize(b)) != Hash(b) for %q", b)
		}
	})
}
