package userstore

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// User represents a row in the users table.
type User struct {
	ID          string
	Email       string
	Name        string
	GoogleSub   string
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

// ErrNotFound is returned when a lookup misses.
var ErrNotFound = errors.New("userstore: not found")

// UpsertGoogleUser creates a user keyed by Google sub/email or updates the
// existing row's name and last_login_at. Returns the resulting User and
// whether this was a fresh insert (the very first sign-in for this email).
func (s *Store) UpsertGoogleUser(email, name, googleSub string) (*User, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, false, fmt.Errorf("userstore: email required")
	}
	now := time.Now().UTC()

	// Try lookup by email first (stable across Google sub migrations).
	existing, err := s.GetUserByEmail(email)
	if err == nil {
		_, err := s.db.Exec(
			`UPDATE users SET name = ?, google_sub = ?, last_login_at = ? WHERE id = ?`,
			name, googleSub, now.Unix(), existing.ID,
		)
		if err != nil {
			return nil, false, fmt.Errorf("userstore: update user: %w", err)
		}
		existing.Name = name
		existing.GoogleSub = googleSub
		existing.LastLoginAt = &now
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}

	id := uuid.NewString()
	_, err = s.db.Exec(
		`INSERT INTO users (id, email, name, google_sub, created_at, last_login_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, email, name, googleSub, now.Unix(), now.Unix(),
	)
	if err != nil {
		return nil, false, fmt.Errorf("userstore: insert user: %w", err)
	}
	return &User{
		ID:          id,
		Email:       email,
		Name:        name,
		GoogleSub:   googleSub,
		CreatedAt:   now,
		LastLoginAt: &now,
	}, true, nil
}

// GetUserByID returns a user by id or ErrNotFound.
func (s *Store) GetUserByID(id string) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, name, COALESCE(google_sub, ''), created_at, last_login_at FROM users WHERE id = ?`,
		id,
	)
	return scanUser(row)
}

// GetUserByEmail returns a user by email (case-insensitive) or ErrNotFound.
func (s *Store) GetUserByEmail(email string) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, name, COALESCE(google_sub, ''), created_at, last_login_at FROM users WHERE email = ?`,
		strings.ToLower(strings.TrimSpace(email)),
	)
	return scanUser(row)
}

// CountUsers returns the total number of users.
func (s *Store) CountUsers() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("userstore: count users: %w", err)
	}
	return n, nil
}

func scanUser(row *sql.Row) (*User, error) {
	var (
		u         User
		createdAt int64
		lastLogin sql.NullInt64
	)
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.GoogleSub, &createdAt, &lastLogin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("userstore: scan user: %w", err)
	}
	u.CreatedAt = time.Unix(createdAt, 0).UTC()
	if lastLogin.Valid {
		t := time.Unix(lastLogin.Int64, 0).UTC()
		u.LastLoginAt = &t
	}
	return &u, nil
}
