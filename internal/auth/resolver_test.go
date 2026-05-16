package auth

import (
	"path/filepath"
	"testing"

	"github.com/sugihAF/contexo/internal/userstore"
)

func openUsers(t *testing.T) *userstore.Store {
	t.Helper()
	s, err := userstore.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("userstore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestResolver_PAT(t *testing.T) {
	us := openUsers(t)
	signer, _ := NewSessionSigner("0123456789abcdef0123456789abcdef")
	r := NewResolver(signer, us, "legacy-key")

	u, _, _ := us.UpsertGoogleUser("alice@example.com", "Alice", "sub")
	_, raw, _ := us.MintPAT(u.ID, "laptop")

	got, err := r.Resolve(raw)
	if err != nil {
		t.Fatalf("resolve pat: %v", err)
	}
	if got != u.ID {
		t.Errorf("got %q, want %q", got, u.ID)
	}
}

func TestResolver_Session(t *testing.T) {
	us := openUsers(t)
	signer, _ := NewSessionSigner("0123456789abcdef0123456789abcdef")
	r := NewResolver(signer, us, "legacy-key")

	u, _, _ := us.UpsertGoogleUser("alice@example.com", "Alice", "sub")
	tok, _, _ := signer.Mint(u.ID, u.Email)

	got, err := r.Resolve(tok)
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if got != u.ID {
		t.Errorf("got %q, want %q", got, u.ID)
	}
}

func TestResolver_LegacyKey(t *testing.T) {
	signer, _ := NewSessionSigner("0123456789abcdef0123456789abcdef")
	r := NewResolver(signer, nil, "legacy-key-123")

	got, err := r.Resolve("legacy-key-123")
	if err != nil {
		t.Fatalf("resolve legacy: %v", err)
	}
	if got != LegacyAdminID {
		t.Errorf("got %q, want %q", got, LegacyAdminID)
	}
	if !IsLegacy(got) {
		t.Error("IsLegacy returned false for legacy id")
	}
}

func TestResolver_RejectsUnknown(t *testing.T) {
	us := openUsers(t)
	signer, _ := NewSessionSigner("0123456789abcdef0123456789abcdef")
	r := NewResolver(signer, us, "legacy-key")

	if _, err := r.Resolve(""); err == nil {
		t.Error("expected error on empty token")
	}
	if _, err := r.Resolve("not-a-real-token"); err == nil {
		t.Error("expected error on garbage non-pat token (treated as session)")
	}
	if _, err := r.Resolve("ctxp_bogus"); err == nil {
		t.Error("expected error on unrecognized pat")
	}
}
