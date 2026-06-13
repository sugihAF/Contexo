package schema

import (
	"strings"
	"testing"
)

func TestSanitizeContent(t *testing.T) {
	// Built from rune values so the test source stays pure ASCII.
	esc := string(rune(0x1b))    // ANSI / terminal escape
	rlo := string(rune(0x202e))  // bidi right-to-left override
	pdf := string(rune(0x202c))  // pop directional formatting
	zwsp := string(rune(0x200b)) // zero-width space
	bom := string(rune(0xfeff))  // BOM / zero-width no-break space
	nul := string(rune(0x00))    // NUL
	zwj := string(rune(0x200d))  // zero-width joiner (legit in emoji + Indic)
	zwnj := string(rune(0x200c)) // zero-width non-joiner (legit in Persian/Arabic)

	in := "Hello\tworld\nNext line.\r\n" +
		"ansi:" + esc + "[31mred" + esc + "[0m " +
		"bidi:" + rlo + "EVIL" + pdf + " " +
		"zw:foo" + zwsp + "bar " +
		"emoji:a" + zwj + "b nonjoin:x" + zwnj + "y " +
		"bom:" + bom + " end " +
		"nul:" + nul + "here"
	out := SanitizeContent(in)

	// Visible text and tab/newline/CR are preserved.
	for _, keep := range []string{"Hello\tworld", "Next line.\r\n", "red", "EVIL", "foobar", "end", "nul:here"} {
		if !strings.Contains(out, keep) {
			t.Errorf("expected output to keep %q; got %q", keep, out)
		}
	}
	// Legitimate joiners (ZWJ/ZWNJ) MUST be preserved — they are valid in emoji
	// sequences and Persian/Indic text; stripping them corrupts content.
	for _, keep := range []string{zwj, zwnj} {
		if !strings.Contains(out, keep) {
			t.Errorf("expected joiner %q to be preserved", keep)
		}
	}
	// Obfuscation / control characters are removed.
	for _, bad := range []string{esc, rlo, pdf, zwsp, bom, nul} {
		if strings.Contains(out, bad) {
			t.Errorf("expected %q to be stripped; got %q", bad, out)
		}
	}

	if SanitizeContent("") != "" {
		t.Error("empty input should stay empty")
	}
	clean := "Just normal markdown.\n## Heading\n- item\n"
	if SanitizeContent(clean) != clean {
		t.Error("clean content should be returned unchanged")
	}
}
