package userstore

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PATPrefix is prepended to every personal access token's raw value so they
// are visually distinct from session JWTs / invite keys when leaked.
const PATPrefix = "ctxp_"

// PAT describes a personal access token without the raw secret.
type PAT struct {
	ID         string
	UserID     string
	Label      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// MintPAT creates a new PAT for user_id with the given label and returns
// both the database record and the raw token (only returned once).
func (s *Store) MintPAT(userID, label string) (*PAT, string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("userstore: rand: %w", err)
	}
	rawStr := PATPrefix + base64.RawURLEncoding.EncodeToString(raw)
	hash := hashToken(rawStr)
	id := uuid.NewString()
	now := time.Now().UTC()

	_, err := s.db.Exec(
		`INSERT INTO personal_access_tokens (id, user_id, token_hash, label, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, userID, hash, label, now.Unix(),
	)
	if err != nil {
		return nil, "", fmt.Errorf("userstore: insert pat: %w", err)
	}
	return &PAT{
		ID:        id,
		UserID:    userID,
		Label:     label,
		CreatedAt: now,
	}, rawStr, nil
}

// ResolvePAT returns the user_id for a raw PAT, or ErrNotFound. Also bumps
// last_used_at.
func (s *Store) ResolvePAT(raw string) (string, error) {
	hash := hashToken(raw)
	var userID, id string
	err := s.db.QueryRow(
		`SELECT id, user_id FROM personal_access_tokens WHERE token_hash = ?`,
		hash,
	).Scan(&id, &userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("userstore: lookup pat: %w", err)
	}
	_, _ = s.db.Exec(`UPDATE personal_access_tokens SET last_used_at = ? WHERE id = ?`, time.Now().UTC().Unix(), id)
	return userID, nil
}

// ListPATs returns PAT records (no raw values) for a user, newest first.
func (s *Store) ListPATs(userID string) ([]PAT, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, label, created_at, last_used_at
		 FROM personal_access_tokens WHERE user_id = ? ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("userstore: list pats: %w", err)
	}
	defer rows.Close()

	var out []PAT
	for rows.Next() {
		var (
			p         PAT
			createdAt int64
			lastUsed  *int64
		)
		if err := rows.Scan(&p.ID, &p.UserID, &p.Label, &createdAt, &lastUsed); err != nil {
			return nil, fmt.Errorf("userstore: scan pat: %w", err)
		}
		p.CreatedAt = time.Unix(createdAt, 0).UTC()
		if lastUsed != nil {
			t := time.Unix(*lastUsed, 0).UTC()
			p.LastUsedAt = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePAT revokes a PAT. Caller MUST verify the PAT belongs to the user.
func (s *Store) DeletePAT(userID, patID string) error {
	res, err := s.db.Exec(
		`DELETE FROM personal_access_tokens WHERE id = ? AND user_id = ?`,
		patID, userID,
	)
	if err != nil {
		return fmt.Errorf("userstore: delete pat: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
