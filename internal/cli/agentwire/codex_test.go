package agentwire

import (
	"errors"
	"reflect"
	"testing"
)

func TestWireCodexInvokesAdd(t *testing.T) {
	var got [][]string
	run := func(args ...string) (string, error) {
		got = append(got, args)
		return "", nil
	}
	if err := WireCodex(run); err != nil {
		t.Fatalf("WireCodex: %v", err)
	}
	want := []string{"mcp", "add", ServerName, "--", "ctx", "mcp"}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("invocations = %v, want one call %v", got, want)
	}
}

func TestWireCodexPropagatesError(t *testing.T) {
	run := func(args ...string) (string, error) { return "", errors.New("boom") }
	if err := WireCodex(run); err == nil {
		t.Errorf("expected error to propagate from runner")
	}
}

func TestUnwireCodexInvokesRemove(t *testing.T) {
	var got [][]string
	run := func(args ...string) (string, error) {
		got = append(got, args)
		return "", nil
	}
	if err := UnwireCodex(run); err != nil {
		t.Fatalf("UnwireCodex: %v", err)
	}
	want := []string{"mcp", "remove", ServerName}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("invocations = %v, want one call %v", got, want)
	}
}

func TestCodexWiredTrueWhenGetSucceeds(t *testing.T) {
	run := func(args ...string) (string, error) {
		want := []string{"mcp", "get", ServerName}
		if !reflect.DeepEqual(args, want) {
			t.Errorf("args = %v, want %v", args, want)
		}
		return "contexo -> ctx mcp", nil
	}
	wired, err := CodexWired(run)
	if err != nil || !wired {
		t.Errorf("got wired=%v err=%v, want true,nil", wired, err)
	}
}

func TestCodexWiredFalseWhenGetFails(t *testing.T) {
	run := func(args ...string) (string, error) { return "", errors.New("no such server") }
	wired, err := CodexWired(run)
	if err != nil || wired {
		t.Errorf("got wired=%v err=%v, want false,nil", wired, err)
	}
}
