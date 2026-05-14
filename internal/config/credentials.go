package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Credentials stores CLI authentication credentials.
type Credentials struct {
	APIKey    string `json:"api_key"`
	ServerURL string `json:"server_url"`
	UserID    string `json:"user_id,omitempty"`
	UserName  string `json:"user_name,omitempty"`
	UserEmail string `json:"user_email,omitempty"`
}

// CredentialsPath returns the legacy .ctx credentials path.
// Deprecated: prefer CredentialsHubPath for new code.
func CredentialsPath(root string) string {
	return filepath.Join(CtxDirPath(root), "credentials.json")
}

// CredentialsHubPath returns the .ctxhub credentials path.
func CredentialsHubPath(root string) string {
	return filepath.Join(CtxhubDirPath(root), "credentials.json")
}

// LoadCredentialsHub reads credentials from .ctxhub/credentials.json.
// Returns (nil, nil) if absent.
func LoadCredentialsHub(root string) (*Credentials, error) {
	data, err := os.ReadFile(CredentialsHubPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("credentials: read: %w", err)
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("credentials: parse: %w", err)
	}
	return &creds, nil
}

// SaveCredentialsHub writes credentials to .ctxhub/credentials.json with 0600.
func SaveCredentialsHub(root string, creds *Credentials) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("credentials: marshal: %w", err)
	}
	path := CredentialsHubPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("credentials: create dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("credentials: write: %w", err)
	}
	return nil
}

// LoadCredentials reads credentials from .ctx/credentials.json.
func LoadCredentials(root string) (*Credentials, error) {
	data, err := os.ReadFile(CredentialsPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("credentials: read: %w", err)
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("credentials: parse: %w", err)
	}
	return &creds, nil
}

// SaveCredentials writes credentials to .ctx/credentials.json.
func SaveCredentials(root string, creds *Credentials) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("credentials: marshal: %w", err)
	}

	path := CredentialsPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("credentials: create dir: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("credentials: write: %w", err)
	}
	return nil
}
