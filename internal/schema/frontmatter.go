package schema

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const frontmatterDelim = "---"

// utf8BOM is the UTF-8 encoding of U+FEFF, the byte-order mark.
const utf8BOM = "\xef\xbb\xbf"

// ParsePage reads a markdown file with YAML frontmatter and returns a Page.
// Accepts LF or CRLF line endings and one leading UTF-8 BOM on input.
// Frontmatter keys the schema does not define go to Frontmatter.Extra.
func ParsePage(data []byte) (*Page, error) {
	s := string(data)
	s = strings.TrimPrefix(s, utf8BOM)
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
	if len(fm.Extra) == 0 {
		// No unknown keys: keep parsed pages reflect.DeepEqual to pages
		// built without Extra.
		fm.Extra = nil
	}

	return &Page{Frontmatter: fm, Body: body}, nil
}

// SerializePage writes a Page to bytes with frontmatter + body, always LF line endings.
// The schema keys come first, in struct order, encoded exactly as they were
// before Extra existed; the Extra keys follow in byte order.
func SerializePage(p *Page) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(frontmatterDelim)
	buf.WriteByte('\n')

	fm := p.Frontmatter
	fm.Extra = nil
	if err := encodeYAML(&buf, &fm); err != nil {
		return nil, err
	}
	if len(p.Frontmatter.Extra) > 0 {
		extra, err := extraNode(p.Frontmatter.Extra)
		if err != nil {
			return nil, err
		}
		if err := encodeYAML(&buf, extra); err != nil {
			return nil, err
		}
	}

	buf.WriteString(frontmatterDelim)
	buf.WriteByte('\n')

	body := strings.ReplaceAll(p.Body, "\r\n", "\n")
	buf.WriteString(body)

	return buf.Bytes(), nil
}

// encodeYAML appends v to buf as a YAML block with a 2-space indent.
func encodeYAML(buf *bytes.Buffer, v any) error {
	enc := yaml.NewEncoder(buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("page: serialize frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("page: close yaml encoder: %w", err)
	}
	return nil
}

// extraNode returns extra as a YAML mapping. A schema key in extra is an
// error: the frontmatter would define that key twice.
func extraNode(extra map[string]any) (*yaml.Node, error) {
	for k := range extra {
		if schemaKeys[k] {
			return nil, fmt.Errorf("page: serialize frontmatter: Extra has schema key %q", k)
		}
	}
	return sortedNode(extra)
}

// sortedNode returns v as a YAML node in which every mapping, at any depth,
// lists its keys in a fixed order (byte order for string keys; see
// sortedMapping). yaml.v3 sorts map keys itself with a
// natural-order comparator that is not a strict order for every key set
// (digit runs past int64, NaN keys), so a map encoded directly can come out
// in a different order on each call, and the page's Hash with it.
func sortedNode(v any) (*yaml.Node, error) {
	rv := reflect.ValueOf(v)
	switch {
	case rv.Kind() == reflect.Map:
		return sortedMapping(rv)
	case rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() != reflect.Uint8:
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for i := 0; i < rv.Len(); i++ {
			item, err := sortedNode(rv.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			seq.Content = append(seq.Content, item)
		}
		return seq, nil
	}
	n := new(yaml.Node)
	if err := n.Encode(v); err != nil {
		return nil, fmt.Errorf("page: serialize frontmatter: %w", err)
	}
	if n.Kind == yaml.ScalarNode && n.Tag == "!!merge" {
		// yaml.v3 writes the string "<<" plain, which reads back as a
		// merge key; quote it so it stays a string.
		n.Tag, n.Style = "!!str", yaml.DoubleQuotedStyle
	}
	return n, nil
}

// sortedMapping is sortedNode for a map: entries ordered by the key's tag,
// then its text, then (for keys that encode alike, such as two NaNs) the
// value's text.
func sortedMapping(m reflect.Value) (*yaml.Node, error) {
	entries := make([]mappingEntry, 0, m.Len())
	for it := m.MapRange(); it.Next(); {
		k, err := sortedNode(it.Key().Interface())
		if err != nil {
			return nil, err
		}
		v, err := sortedNode(it.Value().Interface())
		if err != nil {
			return nil, err
		}
		entries = append(entries, mappingEntry{key: k, value: v})
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := &entries[i], &entries[j]
		if a.key.Tag != b.key.Tag {
			return a.key.Tag < b.key.Tag
		}
		if a.key.Value != b.key.Value {
			return a.key.Value < b.key.Value
		}
		return a.valueText() < b.valueText()
	})
	mapping := &yaml.Node{Kind: yaml.MappingNode}
	for _, e := range entries {
		mapping.Content = append(mapping.Content, e.key, e.value)
	}
	return mapping, nil
}

// mappingEntry is one key/value pair of a mapping sortedMapping builds.
type mappingEntry struct {
	key, value *yaml.Node
	text       *string // the encoded value, computed only to break a tie
}

func (e *mappingEntry) valueText() string {
	if e.text == nil {
		out, _ := yaml.Marshal(e.value) // an unencodable value fails the final Encode anyway
		s := string(out)
		e.text = &s
	}
	return *e.text
}

// schemaKeys holds the YAML key of every PageFrontmatter field.
var schemaKeys = func() map[string]bool {
	keys := map[string]bool{}
	t := reflect.TypeOf(PageFrontmatter{})
	for i := 0; i < t.NumField(); i++ {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); name != "" {
			keys[name] = true
		}
	}
	return keys
}()
