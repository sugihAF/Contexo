package capture

import "testing"

func TestPendingPromptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := WritePendingPrompt(dir, "s1", "hello world"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := TakePendingPrompt(dir, "s1")
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
	// Consumed: a second take is empty.
	again, err := TakePendingPrompt(dir, "s1")
	if err != nil {
		t.Fatalf("take2: %v", err)
	}
	if again != "" {
		t.Errorf("expected empty after consume, got %q", again)
	}
}

func TestTakePendingPromptAbsent(t *testing.T) {
	got, err := TakePendingPrompt(t.TempDir(), "nope")
	if err != nil || got != "" {
		t.Errorf("absent: got (%q,%v), want (\"\",nil)", got, err)
	}
}

func TestWritePendingPromptOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := WritePendingPrompt(dir, "s1", "first"); err != nil {
		t.Fatal(err)
	}
	if err := WritePendingPrompt(dir, "s1", "second"); err != nil {
		t.Fatal(err)
	}
	got, _ := TakePendingPrompt(dir, "s1")
	if got != "second" {
		t.Errorf("got %q, want second", got)
	}
}
