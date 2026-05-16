package userstore

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// InviteKeyPrefix marks raw repo invite keys (separates them from PATs at a glance).
const InviteKeyPrefix = "ctxi_"

// InviteKey is a stored row (no raw secret).
type InviteKey struct {
	ID        string
	RepoID    string
	Label     string
	CreatedBy string
	CreatedAt time.Time
}

// MintInviteKey creates a new invite key for repo, returning the row and the
// raw key (only returned once).
func (s *Store) MintInviteKey(repoID, createdBy, label string) (*InviteKey, string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("userstore: rand: %w", err)
	}
	rawStr := InviteKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	hash := hashToken(rawStr)
	id := uuid.NewString()
	now := time.Now().UTC()

	_, err := s.db.Exec(
		`INSERT INTO repo_invite_keys (id, repo_id, key_hash, label, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, repoID, hash, label, createdBy, now.Unix(),
	)
	if err != nil {
		return nil, "", fmt.Errorf("userstore: insert invite key: %w", err)
	}
	return &InviteKey{
		ID:        id,
		RepoID:    repoID,
		Label:     label,
		CreatedBy: createdBy,
		CreatedAt: now,
	}, rawStr, nil
}

// ResolveInviteKey returns the repo_id for a raw invite key, or ErrNotFound.
func (s *Store) ResolveInviteKey(raw string) (string, error) {
	hash := hashToken(raw)
	var repoID string
	err := s.db.QueryRow(
		`SELECT repo_id FROM repo_invite_keys WHERE key_hash = ?`,
		hash,
	).Scan(&repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("userstore: lookup invite key: %w", err)
	}
	return repoID, nil
}

// ListInviteKeys returns every invite key for a repo (no raw values).
func (s *Store) ListInviteKeys(repoID string) ([]InviteKey, error) {
	rows, err := s.db.Query(
		`SELECT id, repo_id, label, created_by, created_at FROM repo_invite_keys WHERE repo_id = ? ORDER BY created_at DESC`,
		repoID,
	)
	if err != nil {
		return nil, fmt.Errorf("userstore: list invite keys: %w", err)
	}
	defer rows.Close()
	var out []InviteKey
	for rows.Next() {
		var (
			k         InviteKey
			createdAt int64
		)
		if err := rows.Scan(&k.ID, &k.RepoID, &k.Label, &k.CreatedBy, &createdAt); err != nil {
			return nil, fmt.Errorf("userstore: scan invite key: %w", err)
		}
		k.CreatedAt = time.Unix(createdAt, 0).UTC()
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteInviteKey revokes an invite key. Caller MUST verify the user is an
// owner of the repo.
func (s *Store) DeleteInviteKey(repoID, keyID string) error {
	res, err := s.db.Exec(
		`DELETE FROM repo_invite_keys WHERE id = ? AND repo_id = ?`,
		keyID, repoID,
	)
	if err != nil {
		return fmt.Errorf("userstore: delete invite key: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
