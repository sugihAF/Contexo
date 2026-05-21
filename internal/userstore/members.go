package userstore

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RoleOwner has full control of a repo (mint invite keys, delete repo).
// RoleMember can read and write pages.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// Membership describes one (repo_id, user_id) row.
type Membership struct {
	RepoID  string
	UserID  string
	Role    string
	AddedAt time.Time
	// Email is populated by ListRepoMembers (joined from the users table).
	// Queries that do not join users leave it empty.
	Email string
}

// AddMember adds userID to repoID with the given role (idempotent). If a row
// already exists, the role is preserved (call with RoleOwner first when
// claiming a brand-new repo).
func (s *Store) AddMember(repoID, userID, role string) error {
	if role != RoleOwner && role != RoleMember {
		role = RoleMember
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO repo_members (repo_id, user_id, role, added_at) VALUES (?, ?, ?, ?)`,
		repoID, userID, role, time.Now().UTC().Unix(),
	)
	if err != nil {
		return fmt.Errorf("userstore: add member: %w", err)
	}
	return nil
}

// GetRole returns the role of user in repo, or ErrNotFound.
func (s *Store) GetRole(repoID, userID string) (string, error) {
	var role string
	err := s.db.QueryRow(
		`SELECT role FROM repo_members WHERE repo_id = ? AND user_id = ?`,
		repoID, userID,
	).Scan(&role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("userstore: get role: %w", err)
	}
	return role, nil
}

// IsMember reports whether user has any role in repo.
func (s *Store) IsMember(repoID, userID string) (bool, error) {
	_, err := s.GetRole(repoID, userID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, err
}

// ListUserRepos returns every repo_id the user belongs to, with their role.
func (s *Store) ListUserRepos(userID string) ([]Membership, error) {
	rows, err := s.db.Query(
		`SELECT repo_id, user_id, role, added_at FROM repo_members WHERE user_id = ? ORDER BY added_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("userstore: list user repos: %w", err)
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var (
			m       Membership
			addedAt int64
		)
		if err := rows.Scan(&m.RepoID, &m.UserID, &m.Role, &addedAt); err != nil {
			return nil, fmt.Errorf("userstore: scan membership: %w", err)
		}
		m.AddedAt = time.Unix(addedAt, 0).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListRepoMembers returns every member of a repo, with each member's email
// joined in from the users table.
func (s *Store) ListRepoMembers(repoID string) ([]Membership, error) {
	rows, err := s.db.Query(
		`SELECT m.repo_id, m.user_id, m.role, m.added_at, u.email
		   FROM repo_members m
		   JOIN users u ON u.id = m.user_id
		  WHERE m.repo_id = ?
		  ORDER BY m.added_at ASC`,
		repoID,
	)
	if err != nil {
		return nil, fmt.Errorf("userstore: list repo members: %w", err)
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var (
			m       Membership
			addedAt int64
		)
		if err := rows.Scan(&m.RepoID, &m.UserID, &m.Role, &addedAt, &m.Email); err != nil {
			return nil, fmt.Errorf("userstore: scan membership: %w", err)
		}
		m.AddedAt = time.Unix(addedAt, 0).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// RepoHasOwner reports whether at least one row exists for repo with role=owner.
func (s *Store) RepoHasOwner(repoID string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM repo_members WHERE repo_id = ? AND role = 'owner'`,
		repoID,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("userstore: count owners: %w", err)
	}
	return n > 0, nil
}

// RemoveMember removes userID from repoID. It returns ErrNotFound if the user
// is not a member, or ErrLastOwner if removing them would leave the repo with
// no owner.
func (s *Store) RemoveMember(repoID, userID string) error {
	role, err := s.GetRole(repoID, userID)
	if err != nil {
		return err // ErrNotFound when the user is not a member
	}
	if role == RoleOwner {
		var owners int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM repo_members WHERE repo_id = ? AND role = ?`,
			repoID, RoleOwner,
		).Scan(&owners); err != nil {
			return fmt.Errorf("userstore: count owners: %w", err)
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	if _, err := s.db.Exec(
		`DELETE FROM repo_members WHERE repo_id = ? AND user_id = ?`,
		repoID, userID,
	); err != nil {
		return fmt.Errorf("userstore: remove member: %w", err)
	}
	return nil
}
