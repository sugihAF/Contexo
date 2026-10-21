package syncstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"

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

// Normalize is the one comparison form of a page (E6):
// schema.SerializePage(schema.ParsePage(StripBOM(b))). It does NOT sanitize
// content: push sends exactly these bytes and stores them as the base.
func Normalize(b []byte) ([]byte, error) {
	p, err := schema.ParsePage(StripBOM(b))
	if err != nil {
		return nil, err
	}
	return schema.SerializePage(p)
}

// Hash returns "sha256:<hex>" of Normalize(b), or "raw-sha256:<hex>" of b
// itself when b does not normalize.
func Hash(b []byte) string {
	prefix := rawHashPrefix
	if n, err := Normalize(b); err == nil {
		prefix, b = normHashPrefix, n
	}
	sum := sha256.Sum256(b)
	return prefix + hex.EncodeToString(sum[:])
}

// Equal reports whether a and b are the same page under the comparison rule:
// Hash(a) == Hash(b).
func Equal(a, b []byte) bool {
	return Hash(a) == Hash(b)
}
