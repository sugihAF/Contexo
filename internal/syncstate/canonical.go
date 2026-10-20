package syncstate

import "errors"

var errNotImplemented = errors.New("syncstate: not implemented")

// StripBOM returns b without a leading UTF-8 byte-order mark.
func StripBOM(b []byte) []byte {
	return b
}

// Normalize is the one comparison form of a page (E6):
// schema.SerializePage(schema.ParsePage(StripBOM(b))). It does NOT sanitize
// content: push sends exactly these bytes and stores them as the base.
func Normalize(b []byte) ([]byte, error) {
	return nil, errNotImplemented
}

// Hash returns "sha256:<hex>" of Normalize(b), or "raw-sha256:<hex>" of b
// itself when b does not normalize.
func Hash(b []byte) string {
	return ""
}

// Equal reports whether a and b are the same page under the comparison rule:
// Hash(a) == Hash(b).
func Equal(a, b []byte) bool {
	return false
}
