package cli

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestShouldRunUpdateCheck(t *testing.T) {
	noEnv := func(string) string { return "" }
	ci := func(k string) string {
		if k == "CI" {
			return "true"
		}
		return ""
	}
	optOut := func(k string) string {
		if k == "CONTEXO_NO_UPDATE_CHECK" {
			return "1"
		}
		return ""
	}

	cases := []struct {
		desc    string
		cmdName string
		isTTY   bool
		isDev   bool
		env     func(string) string
		want    bool
	}{
		{"interactive push", "push", true, false, noEnv, true},
		{"not a tty", "push", false, false, noEnv, false},
		{"dev build", "push", true, true, noEnv, false},
		{"opt out", "push", true, false, optOut, false},
		{"ci", "push", true, false, ci, false},
		{"mcp suppressed", "mcp", true, false, noEnv, false},
		{"capture suppressed", "capture", true, false, noEnv, false},
		{"version suppressed", "version", true, false, noEnv, false},
		{"update suppressed", "update", true, false, noEnv, false},
	}
	for _, c := range cases {
		if got := shouldRunUpdateCheck(c.cmdName, c.isTTY, c.isDev, c.env); got != c.want {
			t.Errorf("%s: shouldRunUpdateCheck = %v, want %v", c.desc, got, c.want)
		}
	}
}

func TestCacheFresh(t *testing.T) {
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	ttl := 24 * time.Hour
	if !cacheFresh(updateCache{CheckedAt: now.Add(-1 * time.Hour)}, now, ttl) {
		t.Error("1h-old cache should be fresh")
	}
	if cacheFresh(updateCache{CheckedAt: now.Add(-25 * time.Hour)}, now, ttl) {
		t.Error("25h-old cache should be stale")
	}
}

func TestRootChildName(t *testing.T) {
	root := &cobra.Command{Use: "ctx"}
	capture := &cobra.Command{Use: "capture"}
	turn := &cobra.Command{Use: "turn"}
	capture.AddCommand(turn)
	root.AddCommand(capture)
	push := &cobra.Command{Use: "push"}
	root.AddCommand(push)

	if got := rootChildName(turn); got != "capture" {
		t.Errorf("rootChildName(turn) = %q, want capture", got)
	}
	if got := rootChildName(push); got != "push" {
		t.Errorf("rootChildName(push) = %q, want push", got)
	}
	if got := rootChildName(root); got != "ctx" {
		t.Errorf("rootChildName(root) = %q, want ctx", got)
	}
}
