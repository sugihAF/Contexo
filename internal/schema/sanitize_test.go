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

	in := "Hello\tworld\nNext line.\r\n" +
		"ansi:" + esc + "[31mred" + esc + "[0m " +
		"bidi:" + rlo + "EVIL" + pdf + " " +
		"zw:foo" + zwsp + "bar " +
		"bom:" + bom + " end " +
		"nul:" + nul + "here"
	out := SanitizeContent(in)

	// Visible text and tab/newline/CR are preserved.
	for _, keep := range []string{"Hello\tworld", "Next line.\r\n", "red", "EVIL", "foobar", "end", "nul:here"} {
		if !strings.Contains(out, keep) {
			t.Errorf("expected output to keep %q; got %q", keep, out)
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
