package updater

import (
	"crypto/rand"
	"testing"

	"aead.dev/minisign"
)

// TestVerifyMinisigSignature exercises the release-signature verification wiring
// with a freshly generated keypair: a valid signature passes, while tampered
// content, a wrong key, and malformed key text are all rejected.
func TestVerifyMinisigSignature(t *testing.T) {
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubText, err := pub.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("abc123  ctx_1.2.3_linux_amd64.tar.gz\n")
	sig := minisign.Sign(priv, msg)

	if err := verifyMinisigSignature(string(pubText), msg, sig); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := verifyMinisigSignature(string(pubText), []byte("tampered checksums"), sig); err == nil {
		t.Fatal("tampered checksums content was accepted")
	}

	pub2, _, _ := minisign.GenerateKey(rand.Reader)
	pub2Text, _ := pub2.MarshalText()
	if err := verifyMinisigSignature(string(pub2Text), msg, sig); err == nil {
		t.Fatal("signature accepted under the wrong public key")
	}

	if err := verifyMinisigSignature("not-a-valid-key", msg, sig); err == nil {
		t.Fatal("malformed public key text was accepted")
	}
}
