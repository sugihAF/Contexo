package schema

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const frontmatterDelim = "---"

// ParsePage reads a markdown file with YAML frontmatter and returns a Page.
// Accepts LF or CRLF line endings on input.
func ParsePage(data []byte) (*Page, error) {
	s := string(data)
	s = strings.ReplaceAll(s, "\r\n", "\n")

	if !strings.HasPrefix(s, frontmatterDelim+"\n") {
		return nil, fmt.Errorf("page: missing frontmatter (must start with '---\\n')")
	}

	rest := s[len(frontmatterDelim)+1:]
	closingIdx := strings.Index(rest, "\n"+frontmatterDelim+"\n")
	endTrim := false
	if closingIdx < 0 {
		if strings.HasSuffix(rest, "\n"+frontmatterDelim) {
			closingIdx = len(rest) - len(frontmatterDelim) - 1
			endTrim = true
		} else {
			return nil, fmt.Errorf("page: unclosed frontmatter (no '---' terminator)")
		}
	}

	yamlBlock := rest[:closingIdx]
	body := ""
	if !endTrim {
		bodyStart := closingIdx + len("\n"+frontmatterDelim+"\n")
		if bodyStart < len(rest) {
			body = rest[bodyStart:]
		}
	}

	var fm PageFrontmatter
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return nil, fmt.Errorf("page: parse frontmatter: %w", err)
	}

	return &Page{Frontmatter: fm, Body: body}, nil
}

// SerializePage writes a Page to bytes with frontmatter + body, always LF line endings.
func SerializePage(p *Page) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(frontmatterDelim)
	buf.WriteByte('\n')

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&p.Frontmatter); err != nil {
		return nil, fmt.Errorf("page: serialize frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("page: close yaml encoder: %w", err)
	}

	buf.WriteString(frontmatterDelim)
	buf.WriteByte('\n')

	body := strings.ReplaceAll(p.Body, "\r\n", "\n")
	buf.WriteString(body)

	return buf.Bytes(), nil
}
