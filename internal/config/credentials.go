package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Credentials stores CLI authentication credentials. Token is the new
// preferred field (holds a PAT, session JWT, or legacy API key — server
// figures out which). APIKey is kept for back-compat with existing files.
type Credentials struct {
	Token     string `json:"token,omitempty"`
	APIKey    string `json:"api_key,omitempty"`
	ServerURL string `json:"server_url"`
	UserID    string `json:"user_id,omitempty"`
	UserName  string `json:"user_name,omitempty"`
	UserEmail string `json:"user_email,omitempty"`
}

// Bearer returns the value to send in Authorization: Bearer …. Token wins
// over APIKey when both are populated.
func (c *Credentials) Bearer() string {
	if c == nil {
		return ""
	}
	if c.Token != "" {
		return c.Token
	}
	return c.APIKey
}

// Kind describes a Bearer's shape — useful for printing in `auth status`.
func (c *Credentials) Kind() string {
	tok := c.Bearer()
	switch {
	case tok == "":
		return "(none)"
	case strings.HasPrefix(tok, "ctxp_"):
		return "personal access token"
	case strings.HasPrefix(tok, "ctxi_"):
		return "invite key (NOT for CLI use)"
	case strings.Count(tok, ".") == 2:
		return "session token"
	default:
		return "legacy API key"
	}
}

// CredentialsPath returns the project-local .contexo credentials path.
func CredentialsPath(root string) string {
	return filepath.Join(ContexoDirPath(root), "credentials.json")
}

// userHomeDir is a seam over os.UserHomeDir so tests can point the global
// credentials file at a temp dir.
var userHomeDir = os.UserHomeDir

// GlobalCredentialsPath returns the user-level credentials file
// (~/.contexo/credentials.json). A PAT identifies the user, not a repo, so a
// single `ctx auth login` authenticates the CLI/MCP server in every project.
func GlobalCredentialsPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("credentials: locate home dir: %w", err)
	}
	return filepath.Join(home, ContexoDir, "credentials.json"), nil
}

// LoadCredentials reads credentials, preferring the project-local
// .contexo/credentials.json and falling back to the global file. Returns
// (nil, nil) when neither exists.
func LoadCredentials(root string) (*Credentials, error) {
	creds, err := loadCredentialsFile(CredentialsPath(root))
	if err != nil {
		return nil, err
	}
	if creds != nil {
		return creds, nil
	}
	globalPath, err := GlobalCredentialsPath()
	if err != nil {
		// No home dir → just no global fallback; treat as unauthenticated.
		return nil, nil
	}
	return loadCredentialsFile(globalPath)
}

func loadCredentialsFile(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
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

// SaveCredentials writes credentials to the project-local
// .contexo/credentials.json with 0600.
func SaveCredentials(root string, creds *Credentials) error {
	return saveCredentialsFile(CredentialsPath(root), creds)
}

// SaveGlobalCredentials writes credentials to ~/.contexo/credentials.json with
// 0600, so the CLI/MCP server is authenticated regardless of which project
// directory it resolves.
func SaveGlobalCredentials(creds *Credentials) error {
	path, err := GlobalCredentialsPath()
	if err != nil {
		return err
	}
	return saveCredentialsFile(path, creds)
}

func saveCredentialsFile(path string, creds *Credentials) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("credentials: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("credentials: create dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("credentials: write: %w", err)
	}
	return nil
}
