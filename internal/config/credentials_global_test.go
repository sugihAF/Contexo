package config

import (
	"path/filepath"
	"testing"
)

// withHomeDir points the global credentials file at a temp dir for the test.
func withHomeDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	prev := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = prev })
	return home
}

func TestLoadCredentialsFallsBackToGlobal(t *testing.T) {
	withHomeDir(t)
	project := t.TempDir() // no project-local credentials here

	if err := SaveGlobalCredentials(&Credentials{Token: "ctxp_global", UserName: "Sugih"}); err != nil {
		t.Fatal(err)
	}

	got, err := LoadCredentials(project)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Bearer() != "ctxp_global" {
		t.Fatalf("expected global fallback creds, got %+v", got)
	}
}

func TestLoadCredentialsPrefersProject(t *testing.T) {
	withHomeDir(t)
	project := t.TempDir()

	if err := SaveGlobalCredentials(&Credentials{Token: "ctxp_global"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveCredentials(project, &Credentials{Token: "ctxp_project"}); err != nil {
		t.Fatal(err)
	}

	got, err := LoadCredentials(project)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Bearer() != "ctxp_project" {
		t.Fatalf("project-local creds must win, got %+v", got)
	}
}

func TestLoadCredentialsNoneReturnsNil(t *testing.T) {
	withHomeDir(t)
	got, err := LoadCredentials(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil creds when none exist, got %+v", got)
	}
}

func TestGlobalCredentialsPath(t *testing.T) {
	home := withHomeDir(t)
	p, err := GlobalCredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ContexoDir, "credentials.json"); p != want {
		t.Errorf("GlobalCredentialsPath = %q, want %q", p, want)
	}
}
