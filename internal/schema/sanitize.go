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
		case r >= 0x200b && r <= 0x200f: // zero-width + LTR/RTL marks
			return -1
		case r >= 0x202a && r <= 0x202e: // bidi embeddings / overrides
			return -1
		case r >= 0x2060 && r <= 0x2064: // word joiner + invisible operators
			return -1
		case r >= 0x2066 && r <= 0x2069: // bidi isolates
			return -1
		case r == 0xfeff: // zero-width no-break space / BOM
			return -1
		default:
			return r
		}
	}, s)
}
