package contract

import (
	"bytes"
	"encoding/json"
	"io"
)

// WriteDoc writes a stdout document: 2-space indent, no HTML escaping,
// trailing newline. Byte-for-byte what the golden fixtures contain.
func WriteDoc(w io.Writer, doc any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// WriteEnvelope writes the stderr envelope as exactly one line.
func WriteEnvelope(w io.Writer, e *Error) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e.Envelope()); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}
