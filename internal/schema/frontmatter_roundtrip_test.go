package schema_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
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
