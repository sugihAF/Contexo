package redaction

import (
	"regexp"

	"github.com/sugihAF/contexo/internal/schema"
)

// Pipeline performs local-first redaction on session events.
type Pipeline struct {
	patterns  []Pattern
	denyPaths []string
}

// NewPipeline creates a pipeline with default patterns.
func NewPipeline() *Pipeline {
	return &Pipeline{
		patterns:  DefaultPatterns(),
		denyPaths: DefaultDenyPaths(),
	}
}

// AddPattern adds a custom redaction pattern.
func (p *Pipeline) AddPattern(name, regex, replace string) error {
	pat, err := compilePattern(name, regex, replace)
	if err != nil {
		return err
	}
	p.patterns = append(p.patterns, pat)
	return nil
}

// SetDenyPaths replaces the deny path list.
func (p *Pipeline) SetDenyPaths(paths []string) {
	p.denyPaths = paths
}

// Redact returns a copy of the event with secrets redacted and denied paths removed.
// The original event is not mutated.
func (p *Pipeline) Redact(event *schema.SessionEvent) *schema.SessionEvent {
	// Deep copy
	redacted := *event
	redacted.Content = p.redactContent(event.Content)
	redacted.Session = event.Session // shallow copy is fine for SessionRef
	return &redacted
}

func (p *Pipeline) redactContent(c schema.Content) schema.Content {
	result := schema.Content{
		Text: p.redactText(c.Text),
	}

	// Filter refs by denylist
	for _, ref := range c.Refs {
		if ref.Path != "" && MatchesDenyList(ref.Path, p.denyPaths) {
			continue
		}
		result.Refs = append(result.Refs, ref)
	}

	return result
}

func (p *Pipeline) redactText(text string) string {
	result := text
	for _, pat := range p.patterns {
		result = pat.Regex.ReplaceAllString(result, pat.Replace)
	}
	return result
}

func compilePattern(name, regex, replace string) (Pattern, error) {
	r, err := compileRegex(regex)
	if err != nil {
		return Pattern{}, err
	}
	return Pattern{Name: name, Regex: r, Replace: replace}, nil
}

func compileRegex(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}
