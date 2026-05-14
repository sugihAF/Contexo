package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// GenerateAPIKey produces a 32-byte random key encoded as base64url.
func GenerateAPIKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("auth: generate key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
}

// HashKey computes the SHA-256 hash of an API key for storage.
func HashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// ValidateKey checks if a raw key matches its stored hash using constant-time comparison.
func ValidateKey(rawKey, storedHash string) bool {
	hash := HashKey(rawKey)
	return subtle.ConstantTimeCompare([]byte(hash), []byte(storedHash)) == 1
}
