package auth

import (
	"strings"
	"testing"
)

func TestSession_RoundTrip(t *testing.T) {
	s, err := NewSessionSigner("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	tok, exp, err := s.Mint("user-1", "alice@example.com")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if tok == "" || exp.IsZero() {
		t.Fatal("expected non-empty token and expiry")
	}

	uid, email, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if uid != "user-1" || email != "alice@example.com" {
		t.Errorf("claims mismatch: uid=%q email=%q", uid, email)
	}
}

func TestSession_SecretTooShort(t *testing.T) {
	if _, err := NewSessionSigner("short"); err == nil {
		t.Error("expected error for short secret")
	}
}

func TestSession_WrongSecretFails(t *testing.T) {
	good, _ := NewSessionSigner("0123456789abcdef0123456789abcdef")
	other, _ := NewSessionSigner("fedcba9876543210fedcba9876543210")
	tok, _, _ := good.Mint("user-1", "alice@example.com")
	if _, _, err := other.Verify(tok); err == nil {
		t.Error("expected verify to fail with different secret")
	}
}

func TestSession_TamperedTokenFails(t *testing.T) {
	s, _ := NewSessionSigner("0123456789abcdef0123456789abcdef")
	tok, _, _ := s.Mint("user-1", "alice@example.com")
	// Flip a single character in the payload section.
	parts := strings.SplitN(tok, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts in JWT, got %d", len(parts))
	}
	tampered := parts[0] + "." + parts[1] + "x" + "." + parts[2]
	if _, _, err := s.Verify(tampered); err == nil {
		t.Error("expected verify to fail on tampered token")
	}
}
