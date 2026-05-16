package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// SessionTTL is how long a session JWT stays valid after mint.
const SessionTTL = 30 * 24 * time.Hour

// SessionIssuer identifies sessions minted by this server (claim "iss").
const SessionIssuer = "contexo"

// SessionClaims are the contents of a session JWT minted by MintSession.
type SessionClaims struct {
	UserID string `json:"sub"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

// SessionSigner mints and verifies HS256 session JWTs using a shared secret.
type SessionSigner struct {
	secret []byte
}

// NewSessionSigner constructs a signer from a server secret.
func NewSessionSigner(secret string) (*SessionSigner, error) {
	if len(secret) < 32 {
		return nil, errors.New("auth: session secret must be at least 32 bytes")
	}
	return &SessionSigner{secret: []byte(secret)}, nil
}

// Mint creates a session JWT for the user.
func (s *SessionSigner) Mint(userID, email string) (string, time.Time, error) {
	exp := time.Now().Add(SessionTTL)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, SessionClaims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    SessionIssuer,
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	})
	str, err := tok.SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: sign session: %w", err)
	}
	return str, exp, nil
}

// Verify parses and validates a session JWT, returning the user id and email.
func (s *SessionSigner) Verify(raw string) (userID, email string, err error) {
	claims := &SessionClaims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: unexpected session alg %v", t.Header["alg"])
		}
		return s.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return "", "", err
	}
	if !tok.Valid {
		return "", "", errors.New("auth: invalid session")
	}
	if claims.Issuer != SessionIssuer {
		return "", "", fmt.Errorf("auth: bad session issuer %q", claims.Issuer)
	}
	return claims.UserID, claims.Email, nil
}
