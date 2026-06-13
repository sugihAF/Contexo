package schema

import "strings"

// SanitizeContent removes characters that have no legitimate place in a
// knowledge page and are classic prompt-injection obfuscation vectors:
// terminal/ANSI escapes and other C0/C1 control codes (tab, newline, and
// carriage return are kept), Unicode bidirectional overrides, and
// zero-width / invisible characters. All visible text is preserved.
//
// It is applied to shared page content before it reaches a consuming agent, so
// an attacker cannot smuggle hidden instructions (e.g. via a bidi override or a
// zero-width-spaced "ignore previous instructions") into a teammate's context.
func SanitizeContent(s string) string {
	if s == "" {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return r
		case r < 0x20: // C0 controls, including ESC (0x1b)
			return -1
		case r == 0x7f: // DEL
			return -1
		case r >= 0x80 && r <= 0x9f: // C1 controls
			return -1
		case r == 0x200b: // zero-width space
			return -1
		case r >= 0x202a && r <= 0x202e: // bidi embeddings / overrides (Trojan Source)
			return -1
		case r >= 0x2066 && r <= 0x2069: // bidi isolates (Trojan Source)
			return -1
		case r == 0xfeff: // BOM / zero-width no-break space
			return -1
		// NOTE: U+200C/U+200D (ZWNJ/ZWJ joiners), U+200E/U+200F (LTR/RTL marks)
		// and U+2060 (word joiner) are intentionally KEPT — they are legitimate in
		// emoji ZWJ sequences and Persian/Arabic/Indic scripts. The Trojan-Source
		// attack relies on the bidi overrides/isolates above, which are stripped.
		default:
			return r
		}
	}, s)
}
