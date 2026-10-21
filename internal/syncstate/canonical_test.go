package syncstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
)

var wp04cmpBOM = []byte{0xEF, 0xBB, 0xBF}

// wp04cmpPage returns a valid ctx.page.v1 page of type typ and slug slug
// with the given body, in the block-list form Normalize produces.
func wp04cmpPage(typ, slug, body string) []byte {
	return []byte("---\nschema: ctx.page.v1\nslug: " + slug + "\ntype: " + typ +
		"\nauthor: tester\ncreated: 2026-01-02T00:00:00Z\n" +
		"updated: 2026-01-02T03:04:05Z\nparent_sha: \"\"\n---\n" + body)
}

const (
	wp04cmpBlock = "---\nschema: ctx.page.v1\nslug: alpha\ntype: concept\n" +
		"author: tester\ncreated: 2026-01-02T00:00:00Z\n" +
		"updated: 2026-01-02T03:04:05Z\nparent_sha: \"\"\ntags:\n  - a\n  - b\n" +
		"---\n# Alpha\n\nBody text.\n"
	// wp04cmpFlow is wp04cmpBlock with a flow list, a date-only created,
	// reordered keys and no parent_sha: the same page.
	wp04cmpFlow = "---\ntype: concept\nschema: ctx.page.v1\nslug: alpha\n" +
		"tags: [a, b]\nauthor: tester\ncreated: 2026-01-02\n" +
		"updated: 2026-01-02T03:04:05Z\n---\n# Alpha\n\nBody text.\n"
)

func wp04cmpCRLF(s string) []byte {
	return []byte(strings.ReplaceAll(s, "\n", "\r\n"))
}

func wp04cmpWithBOM(b []byte) []byte {
	return append(append([]byte{}, wp04cmpBOM...), b...)
}

func TestWP04StripBOM(t *testing.T) {
	x := []byte("---\nx\n")
	if got := StripBOM(wp04cmpWithBOM(x)); !bytes.Equal(got, x) {
		t.Errorf("StripBOM(BOM+x) = %q, want %q", got, x)
	}
	if got := StripBOM(x); !bytes.Equal(got, x) {
		t.Errorf("StripBOM(x) = %q, want %q", got, x)
	}
	two := wp04cmpWithBOM(wp04cmpWithBOM(x))
	if got := StripBOM(two); !bytes.Equal(got, wp04cmpWithBOM(x)) {
		t.Errorf("StripBOM strips only one BOM; got %q", got)
	}
	if got := StripBOM(nil); len(got) != 0 {
		t.Errorf("StripBOM(nil) = %q, want empty", got)
	}
	partial := []byte{0xEF, 0xBB, 'x'}
	if got := StripBOM(partial); !bytes.Equal(got, partial) {
		t.Errorf("StripBOM(partial BOM) = %q, want unchanged", got)
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	inputs := map[string][]byte{
		"block":       []byte(wp04cmpBlock),
		"flow":        []byte(wp04cmpFlow),
		"crlf":        wp04cmpCRLF(wp04cmpBlock),
		"bom":         wp04cmpWithBOM([]byte(wp04cmpBlock)),
		"bom_crlf":    wp04cmpWithBOM(wp04cmpCRLF(wp04cmpFlow)),
		"date_only":   []byte(strings.Replace(wp04cmpBlock, "2026-01-02T00:00:00Z", "2026-01-02", 1)),
		"offset_nano": []byte(strings.Replace(wp04cmpBlock, "2026-01-02T03:04:05Z", "2026-01-02T10:04:05.123456789+07:00", 1)),
		"no_body":     []byte(strings.TrimSuffix(wp04cmpBlock, "# Alpha\n\nBody text.\n")),
		"end_trim":    []byte(strings.TrimSuffix(wp04cmpBlock, "\n# Alpha\n\nBody text.\n")),
		"invalid_fm":  []byte("---\nslug: x\n---\nno schema, still parses\n"),
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			n1, err := Normalize(in)
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			n2, err := Normalize(n1)
			if err != nil {
				t.Fatalf("Normalize(Normalize): %v", err)
			}
			if !bytes.Equal(n1, n2) {
				t.Errorf("not idempotent:\n first: %q\nsecond: %q", n1, n2)
			}
			if bytes.HasPrefix(n1, wp04cmpBOM) {
				t.Errorf("Normalize output starts with a BOM: %q", n1)
			}
			if bytes.Contains(n1, []byte("\r")) {
				t.Errorf("Normalize output contains CR: %q", n1)
			}
		})
	}

	lf, err := Normalize([]byte(wp04cmpBlock))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"crlf", "bom", "flow", "bom_crlf", "date_only"} {
		got, err := Normalize(inputs[name])
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(got, lf) {
			t.Errorf("Normalize(%s) != Normalize(block):\n got: %q\nwant: %q", name, got, lf)
		}
	}
	if !bytes.Equal(lf, []byte(wp04cmpBlock)) {
		t.Errorf("block form is not already normalized:\n got: %q\nwant: %q", lf, wp04cmpBlock)
	}
}

func TestNormalizeDoesNotSanitize(t *testing.T) {
	body := "Danger \x1b[31mred\x1b[0m, zero​width, bidi ‮evil\n"
	in := wp04cmpPage("concept", "esc", body)
	out, err := Normalize(in)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	for _, want := range []string{"\x1b[31m", "\x1b[0m", "​", "‮"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("Normalize dropped %q: %q", want, out)
		}
	}
	if !bytes.Equal(out, in) {
		t.Errorf("Normalize changed an already-normal page:\n got: %q\nwant: %q", out, in)
	}
}

func TestHashPrefixAndRawFallback(t *testing.T) {
	sha := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	raw := regexp.MustCompile(`^raw-sha256:[0-9a-f]{64}$`)
	hexOf := func(b []byte) string {
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}

	valid := []byte(wp04cmpFlow)
	norm, err := Normalize(valid)
	if err != nil {
		t.Fatal(err)
	}
	h := Hash(valid)
	if !sha.MatchString(h) {
		t.Errorf("Hash(valid) = %q, want sha256:<64 hex>", h)
	}
	if want := "sha256:" + hexOf(norm); h != want {
		t.Errorf("Hash(valid) = %q, want %q (sha256 of Normalize)", h, want)
	}
	if h == "sha256:"+hexOf(valid) {
		t.Errorf("Hash(valid) hashed the raw bytes, not Normalize")
	}

	// Parses but fails Validate (no author): Normalize does not validate.
	noAuthor := []byte("---\nschema: ctx.page.v1\nslug: x\ntype: concept\n---\nbody\n")
	if got := Hash(noAuthor); !sha.MatchString(got) {
		t.Errorf("Hash(parseable but invalid) = %q, want sha256: form", got)
	}

	for name, b := range map[string][]byte{
		"no_frontmatter": []byte("no frontmatter"),
		"unclosed":       []byte("---\nslug: x\nbody\n"),
		"bad_yaml":       []byte("---\nslug: [\n---\nbody\n"),
		"bom_no_fm":      wp04cmpWithBOM([]byte("no frontmatter")),
		"empty":          {},
	} {
		got := Hash(b)
		if !raw.MatchString(got) {
			t.Errorf("%s: Hash = %q, want raw-sha256:<64 hex>", name, got)
		}
		if want := "raw-sha256:" + hexOf(b); got != want {
			t.Errorf("%s: Hash = %q, want %q (sha256 of the raw bytes)", name, got, want)
		}
	}
}

func TestEqualFlowListEqualsBlockList(t *testing.T) {
	block := []byte(wp04cmpBlock)
	cases := []struct {
		name string
		a, b []byte
		want bool
	}{
		{"flow_vs_block", []byte(wp04cmpFlow), block, true},
		{"bom_vs_plain", wp04cmpWithBOM(block), block, true},
		{"crlf_vs_lf", wp04cmpCRLF(wp04cmpBlock), block, true},
		{"different_body", []byte(strings.Replace(wp04cmpBlock, "Body text.", "Other text.", 1)), block, false},
		{"different_tags", []byte(strings.Replace(wp04cmpFlow, "[a, b]", "[b, a]", 1)), block, false},
		{"raw_same", []byte("no frontmatter"), []byte("no frontmatter"), true},
		{"raw_different", []byte("no frontmatter"), []byte("no frontmatter!"), false},
		{"raw_vs_page", []byte("no frontmatter"), block, false},
	}
	for _, c := range cases {
		if got := Equal(c.a, c.b); got != c.want {
			t.Errorf("%s: Equal = %v, want %v", c.name, got, c.want)
		}
		if got := Equal(c.b, c.a); got != c.want {
			t.Errorf("%s (swapped): Equal = %v, want %v", c.name, got, c.want)
		}
	}
}

// wp04cmpLossyPage is a valid concept page x whose last frontmatter key,
// reasoning_summary, is spelled summary (everything after the key's colon),
// followed by body.
func wp04cmpLossyPage(summary, body string) []byte {
	return []byte("---\nschema: ctx.page.v1\nslug: x\ntype: concept\n" +
		"author: a\ncreated: 2026-01-01T00:00:00Z\n" +
		"reasoning_summary:" + summary + "---\n" + body)
}

// wp04cmpLossy is an input on which one ParsePage/SerializePage pass is
// lossy. valid is true when it is a valid page at wiki/concepts/x.md.
type wp04cmpLossy struct {
	in    []byte
	valid bool
}

// wp04cmpLossyInputs returns pages one pass does not settle: the pass drops
// the line break ending the YAML block, so a trailing block scalar loses a
// newline each pass, and a CR run needs more than one CRLF replacement.
func wp04cmpLossyInputs(t testing.TB) map[string]wp04cmpLossy {
	t.Helper()
	serialized, err := schema.SerializePage(&schema.Page{
		Frontmatter: schema.PageFrontmatter{
			Schema:           schema.PageSchemaV1,
			Slug:             "x",
			Type:             schema.TypeConcept,
			Author:           "a",
			Created:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			ReasoningSummary: "x\n\n",
		},
		Body: "# X\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]wp04cmpLossy{
		"serialized_keep":  {serialized, true},
		"clip_blank_line":  {wp04cmpLossyPage(" |\n  Some text\n\n", "# X\n"), true},
		"keep_blank_lines": {wp04cmpLossyPage(" |+\n  Some text\n\n\n", "# X\n"), true},
		"cr_run_body":      {wp04cmpLossyPage(" plain\n", "# X\r\r\r\nmore\n"), true},
		"fuzz_leading_nl":  {[]byte("---\nreasoning_summary: |\n\n 00\n---"), false},
	}
}

func TestWP04NormalizeIdempotentLossyInputs(t *testing.T) {
	for name, c := range wp04cmpLossyInputs(t) {
		t.Run(name, func(t *testing.T) {
			n1, err := Normalize(c.in)
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			n2, err := Normalize(n1)
			if err != nil {
				t.Fatalf("Normalize(Normalize): %v", err)
			}
			if !bytes.Equal(n1, n2) {
				t.Errorf("not idempotent:\n first: %q\nsecond: %q", n1, n2)
			}
			if !Equal(c.in, n1) {
				t.Errorf("Equal(x, Normalize(x)) = false: %s vs %s", Hash(c.in), Hash(n1))
			}
			if bytes.Contains(n1, []byte("\r")) {
				t.Errorf("Normalize output contains CR: %q", n1)
			}
		})
	}
}

// A page that one ParsePage/SerializePage pass already settles keeps exactly
// the bytes (and so the hash) of that single pass.
func TestWP04NormalizeStableInputUnchanged(t *testing.T) {
	for _, in := range [][]byte{[]byte(wp04cmpBlock), []byte(wp04cmpFlow), wp04cmpCRLF(wp04cmpFlow)} {
		p, err := schema.ParsePage(StripBOM(in))
		if err != nil {
			t.Fatal(err)
		}
		one, err := schema.SerializePage(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Normalize(in)
		if err != nil {
			t.Fatalf("Normalize: %v", err)
		}
		if !bytes.Equal(got, one) {
			t.Errorf("Normalize(%q) = %q, want the single pass %q", in, got, one)
		}
	}
}

// A page with no fixed point within the round cap does not normalize, so it
// hashes raw.
func TestWP04NormalizeRoundCap(t *testing.T) {
	in := wp04cmpLossyPage(" |+\n  x\n"+strings.Repeat("\n", 100), "# X\n")
	if n, err := Normalize(in); err == nil {
		t.Fatalf("Normalize of a page needing ~100 passes succeeded: %q", n)
	}
	if got := Hash(in); !strings.HasPrefix(got, "raw-sha256:") {
		t.Errorf("Hash = %q, want the raw-sha256 fallback", got)
	}
	// A summary ending in a few dozen blank lines still settles.
	few := wp04cmpLossyPage(" |+\n  x\n"+strings.Repeat("\n", 40), "# X\n")
	if _, err := Normalize(few); err != nil {
		t.Errorf("Normalize of a page needing ~40 passes: %v", err)
	}
}

// FuzzNormalizeIdempotent checks Normalize(Normalize(b)) == Normalize(b)
// and Hash(Normalize(b)) == Hash(b) for every b that normalizes. go test
// runs only the seeds; run it with -fuzz to search further.
func FuzzNormalizeIdempotent(f *testing.F) {
	for _, c := range wp04cmpLossyInputs(f) {
		f.Add(c.in)
	}
	f.Add([]byte(wp04cmpBlock))
	f.Add([]byte(wp04cmpFlow))
	f.Add([]byte("---\nreasoning_summary: \"\\n\"\n---"))
	f.Fuzz(func(t *testing.T, b []byte) {
		n1, err := Normalize(b)
		if err != nil {
			return
		}
		n2, err := Normalize(n1)
		if err != nil {
			t.Fatalf("Normalize(Normalize(%q)): %v", b, err)
		}
		if !bytes.Equal(n1, n2) {
			t.Fatalf("not idempotent for %q:\n first: %q\nsecond: %q", b, n1, n2)
		}
		if Hash(n1) != Hash(b) {
			t.Fatalf("Hash(Normalize(%q)) = %s, Hash = %s", b, Hash(n1), Hash(b))
		}
	})
}
