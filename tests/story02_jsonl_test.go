package tests

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/jsonl"
)

func makeEvent(turn int) *schema.SessionEvent {
	return &schema.SessionEvent{
		Schema:  "ctx.session_event.v1",
		EventID: fmt.Sprintf("evt-%03d", turn),
		Ts:      time.Now().UTC().Truncate(time.Millisecond),
		Session: schema.SessionRef{ID: "sess-test", Source: "test"},
		Type:    "user_message",
		Turn:    turn,
		Content: schema.Content{Text: fmt.Sprintf("Turn %d message", turn)},
	}
}

func TestStory02_InterfacesCompile(t *testing.T) {
	// This test verifies interfaces compile by importing the store package.
	// If this compiles, the interfaces are valid Go.
	t.Log("store interfaces compile successfully")
}

func TestStory02_WriterAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")
	w, err := jsonl.NewWriter(path)
	require.NoError(t, err)

	evt := makeEvent(1)
	err = w.Append(evt)
	require.NoError(t, err)
}

func TestStory02_WriterCreatesParentDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "dir", "test.jsonl")
	w, err := jsonl.NewWriter(path)
	require.NoError(t, err)

	err = w.Append(makeEvent(1))
	require.NoError(t, err)
}

func TestStory02_ReaderReadAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")
	w, err := jsonl.NewWriter(path)
	require.NoError(t, err)

	for i := 1; i <= 5; i++ {
		require.NoError(t, w.Append(makeEvent(i)))
	}

	r := jsonl.NewReader(path)
	events, err := r.ReadAll()
	require.NoError(t, err)
	assert.Len(t, events, 5)

	for i, evt := range events {
		assert.Equal(t, i+1, evt.Turn)
		assert.Equal(t, fmt.Sprintf("evt-%03d", i+1), evt.EventID)
	}
}

func TestStory02_ReaderTurnRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")
	w, err := jsonl.NewWriter(path)
	require.NoError(t, err)

	for i := 1; i <= 20; i++ {
		require.NoError(t, w.Append(makeEvent(i)))
	}

	r := jsonl.NewReader(path)

	// Filter turns 5-10
	events, err := r.ReadRange(5, 10)
	require.NoError(t, err)
	assert.Len(t, events, 6)
	assert.Equal(t, 5, events[0].Turn)
	assert.Equal(t, 10, events[len(events)-1].Turn)
}

func TestStory02_RoundTrip20Events(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")
	w, err := jsonl.NewWriter(path)
	require.NoError(t, err)

	originals := make([]*schema.SessionEvent, 20)
	for i := 0; i < 20; i++ {
		originals[i] = makeEvent(i + 1)
		require.NoError(t, w.Append(originals[i]))
	}

	r := jsonl.NewReader(path)
	events, err := r.ReadAll()
	require.NoError(t, err)
	require.Len(t, events, 20)

	for i, evt := range events {
		assert.Equal(t, originals[i].EventID, evt.EventID)
		assert.Equal(t, originals[i].Session.ID, evt.Session.ID)
		assert.Equal(t, originals[i].Type, evt.Type)
		assert.Equal(t, originals[i].Turn, evt.Turn)
		assert.Equal(t, originals[i].Content.Text, evt.Content.Text)
	}
}
