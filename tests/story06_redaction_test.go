package tests

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/redaction"
	"github.com/sugihAF/contexo/internal/schema"
)

func makeTestEvent(text string, refs []schema.ContentRef) *schema.SessionEvent {
	return &schema.SessionEvent{
		Schema:  "ctx.session_event.v1",
		EventID: "evt-test",
		Ts:      time.Now(),
		Session: schema.SessionRef{ID: "sess-test"},
		Type:    "user_message",
		Content: schema.Content{
			Text: text,
			Refs: refs,
		},
	}
}

func TestStory06_RedactAWSKey(t *testing.T) {
	p := redaction.NewPipeline()
	evt := makeTestEvent("My key is AKIAIOSFODNN7EXAMPLE", nil)

	result := p.Redact(evt)
	assert.Contains(t, result.Content.Text, "[REDACTED:aws_key]")
	assert.NotContains(t, result.Content.Text, "AKIAIOSFODNN7EXAMPLE")
}

func TestStory06_RedactGenericAPIToken(t *testing.T) {
	p := redaction.NewPipeline()
	evt := makeTestEvent("api_key=sk_test_abcdefghijklmnopqrstuvwxyz", nil)

	result := p.Redact(evt)
	assert.Contains(t, result.Content.Text, "[REDACTED:api_token]")
}

func TestStory06_RedactPlaceholder(t *testing.T) {
	p := redaction.NewPipeline()
	evt := makeTestEvent("Set AKIAIOSFODNN7EXAMPLE as your key", nil)

	result := p.Redact(evt)
	assert.Contains(t, result.Content.Text, "[REDACTED:aws_key]")
	assert.Contains(t, result.Content.Text, "Set ")
	assert.Contains(t, result.Content.Text, " as your key")
}

func TestStory06_DenyListRemovesPaths(t *testing.T) {
	p := redaction.NewPipeline()
	evt := makeTestEvent("check these files", []schema.ContentRef{
		{Path: "src/main.go", Type: "file"},
		{Path: ".env", Type: "file"},
		{Path: "config/secrets.yaml", Type: "file"},
		{Path: "src/handler.go", Type: "file"},
	})

	result := p.Redact(evt)
	assert.Len(t, result.Content.Refs, 2)
	for _, ref := range result.Content.Refs {
		assert.NotEqual(t, ".env", ref.Path)
		assert.NotEqual(t, "config/secrets.yaml", ref.Path)
	}
}

func TestStory06_CustomPattern(t *testing.T) {
	p := redaction.NewPipeline()
	err := p.AddPattern("custom_secret", `SECRET_[A-Z0-9]{10}`, "[REDACTED:custom]")
	require.NoError(t, err)

	evt := makeTestEvent("Found SECRET_ABCDEF1234 in config", nil)
	result := p.Redact(evt)
	assert.Contains(t, result.Content.Text, "[REDACTED:custom]")
	assert.NotContains(t, result.Content.Text, "SECRET_ABCDEF1234")
}

func TestStory06_OriginalNotMutated(t *testing.T) {
	p := redaction.NewPipeline()
	original := makeTestEvent("Key: AKIAIOSFODNN7EXAMPLE", []schema.ContentRef{
		{Path: ".env", Type: "file"},
	})
	originalText := original.Content.Text
	originalRefCount := len(original.Content.Refs)

	_ = p.Redact(original)

	// Original should be unchanged
	assert.Equal(t, originalText, original.Content.Text)
	assert.Len(t, original.Content.Refs, originalRefCount)
}
