package syncstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/sugihAF/contexo/internal/schema"
)

// utf8BOM is the UTF-8 byte-order mark.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Hash prefixes: a normalized page, or the raw bytes of one that does not
// normalize.
const (
	normHashPrefix = "sha256:"
	rawHashPrefix  = "raw-sha256:"
)

// StripBOM returns b without a leading UTF-8 byte-order mark.
func StripBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, utf8BOM)
}

// normalizeMaxPasses caps the ParsePage/SerializePage passes Normalize makes
// looking for a fixed point. A lossy pass drops one trailing newline of a
// final block scalar (or one CR of a CR run), so this settles a summary
// ending in a few dozen blank lines while bounding the cost of a hostile
// page. ScanLocal reports a page that does not settle as invalid.
const normalizeMaxPasses = 64

// Normalize is the one comparison form of a page (E6): the fixed point of
// the pass schema.SerializePage(schema.ParsePage(.)), starting from
// StripBOM(b). A single pass is not always stable: it drops the line break
// ending the YAML block, so a trailing block scalar loses a newline each
// time, and a CR run needs more than one CRLF replacement. Repeating the
// pass until the bytes stop changing makes Normalize idempotent, so
// Hash(Normalize(x)) == Hash(x); a page one pass already settles gets
// exactly that pass's bytes. A pass that fails, or no fixed point within
// normalizeMaxPasses passes, is an error. It does NOT sanitize content:
// push sends exactly these bytes and stores them as the base.
func Normalize(b []byte) ([]byte, error) {
	cur := StripBOM(b)
	for pass := 0; pass < normalizeMaxPasses; pass++ {
		p, err := schema.ParsePage(cur)
		if err != nil {
			return nil, err
		}
		next, err := schema.SerializePage(p)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(next, cur) {
			return next, nil
		}
		cur = next
	}
	return nil, fmt.Errorf("syncstate: normalize: no fixed point within %d passes", normalizeMaxPasses)
}

// Hash returns "sha256:<hex>" of Normalize(b), or "raw-sha256:<hex>" of b
// itself when b does not normalize.
func Hash(b []byte) string {
	if n, err := Normalize(b); err == nil {
		return hashNormalized(n)
	}
	sum := sha256.Sum256(b)
	return rawHashPrefix + hex.EncodeToString(sum[:])
}

// hashNormalized returns "sha256:<hex>" of n, a Normalize result.
func hashNormalized(n []byte) string {
	sum := sha256.Sum256(n)
	return normHashPrefix + hex.EncodeToString(sum[:])
}

// Equal reports whether a and b are the same page under the comparison rule:
// Hash(a) == Hash(b).
func Equal(a, b []byte) bool {
	return Hash(a) == Hash(b)
}
