package version

import "testing"

func TestIsDevBuild(t *testing.T) {
	old := Version
	defer func() { Version = old }()

	Version = "dev"
	if !IsDevBuild() {
		t.Errorf("IsDevBuild() = false, want true for Version=%q", Version)
	}

	Version = "1.0.0"
	if IsDevBuild() {
		t.Errorf("IsDevBuild() = true, want false for Version=%q", Version)
	}
}

func TestFull(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, Date
	defer func() { Version, Commit, Date = oldV, oldC, oldD }()

	Version, Commit, Date = "1.2.3", "abc1234", "2026-06-02"
	got := Full()
	want := "ctx 1.2.3 (abc1234, 2026-06-02)"
	if got != want {
		t.Errorf("Full() = %q, want %q", got, want)
	}
}
