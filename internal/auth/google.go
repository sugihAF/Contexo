package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// GoogleJWKSURL serves Google's RS256 public keys for ID tokens.
const GoogleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"

// GoogleIssuers are the values Google may set as `iss` on ID tokens.
var GoogleIssuers = map[string]bool{
	"https://accounts.google.com": true,
	"accounts.google.com":         true,
}

// GoogleClaims is the subset of an ID token we care about.
type GoogleClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Subject       string `json:"sub"`
}

// Verifier produces trusted GoogleClaims from a raw ID token. The production
// implementation is *GoogleVerifier; tests can substitute a fake.
type Verifier interface {
	Verify(idToken string) (*GoogleClaims, error)
}

// GoogleVerifier verifies Google ID tokens for a configured OAuth client.
type GoogleVerifier struct {
	clientID string
	jwksURL  string
	mu       sync.RWMutex
	keys     map[string]*rsa.PublicKey
	expires  time.Time
	httpC    *http.Client
}

// NewGoogleVerifier constructs a verifier for the given OAuth Client ID.
func NewGoogleVerifier(clientID string) *GoogleVerifier {
	return &GoogleVerifier{
		clientID: clientID,
		jwksURL:  GoogleJWKSURL,
		httpC:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Verify parses idToken, checks the signature against Google's JWKS, and
// validates issuer/audience/expiry/email_verified. Returns the trusted claims.
func (v *GoogleVerifier) Verify(idToken string) (*GoogleClaims, error) {
	if v.clientID == "" {
		return nil, errors.New("auth: google client id not configured")
	}

	parsed, err := jwt.Parse(idToken, v.keyfunc, jwt.WithValidMethods([]string{"RS256"}))
	if err != nil {
		return nil, fmt.Errorf("auth: parse google id token: %w", err)
	}
	if !parsed.Valid {
		return nil, errors.New("auth: invalid google id token")
	}

	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("auth: unexpected claims type")
	}

	iss, _ := mc["iss"].(string)
	if !GoogleIssuers[iss] {
		return nil, fmt.Errorf("auth: bad issuer %q", iss)
	}
	aud, _ := mc["aud"].(string)
	if aud != v.clientID {
		return nil, fmt.Errorf("auth: audience mismatch (got %q)", aud)
	}
	if exp, ok := mc["exp"].(float64); ok {
		if time.Now().Unix() > int64(exp) {
			return nil, errors.New("auth: token expired")
		}
	}
	email, _ := mc["email"].(string)
	if email == "" {
		return nil, errors.New("auth: missing email claim")
	}
	emailVerified := true
	if v, ok := mc["email_verified"].(bool); ok {
		emailVerified = v
	}
	if !emailVerified {
		return nil, errors.New("auth: email not verified by google")
	}
	name, _ := mc["name"].(string)
	sub, _ := mc["sub"].(string)
	return &GoogleClaims{
		Email:         strings.ToLower(email),
		EmailVerified: emailVerified,
		Name:          name,
		Subject:       sub,
	}, nil
}

func (v *GoogleVerifier) keyfunc(t *jwt.Token) (interface{}, error) {
	kid, _ := t.Header["kid"].(string)
	if kid == "" {
		return nil, errors.New("auth: id token missing kid header")
	}
	key, err := v.lookupKey(kid)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func (v *GoogleVerifier) lookupKey(kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	if time.Now().Before(v.expires) {
		if k, ok := v.keys[kid]; ok {
			v.mu.RUnlock()
			return k, nil
		}
	}
	v.mu.RUnlock()

	if err := v.refresh(); err != nil {
		return nil, err
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("auth: unknown jwks kid %q", kid)
}

func (v *GoogleVerifier) refresh() error {
	req, err := http.NewRequest(http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("auth: build jwks request: %w", err)
	}
	resp, err := v.httpC.Do(req)
	if err != nil {
		return fmt.Errorf("auth: fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth: jwks status %d", resp.StatusCode)
	}

	var doc struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("auth: decode jwks: %w", err)
	}

	out := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := rsaKeyFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return errors.New("auth: jwks had no usable keys")
	}

	v.mu.Lock()
	v.keys = out
	v.expires = time.Now().Add(1 * time.Hour)
	v.mu.Unlock()
	return nil
}

func rsaKeyFromJWK(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, fmt.Errorf("auth: decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, fmt.Errorf("auth: decode e: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	var e int
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, errors.New("auth: zero exponent")
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}
