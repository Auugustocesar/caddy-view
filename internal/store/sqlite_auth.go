package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// guard returns the error to give when the context is done or the store is closed, and nil otherwise.
func (s *SQLite) guard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed.Load() {
		return ErrClosed
	}
	return nil
}

const userColumns = "id, username, password_hash, created_at"

// scanUser reads a user row, turning an empty result into ErrNotFound.
func scanUser(row *sql.Row) (User, error) {
	var (
		u         User
		createdAt int64
	)
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.CreatedAt = time.UnixMilli(createdAt).UTC()
	return u, nil
}

// SaveUser creates the user, or replaces the password hash of the user with the same username, ignoring case.
func (s *SQLite) SaveUser(ctx context.Context, u User) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	if err := validateUser(u); err != nil {
		return User{}, err
	}
	if err := s.guard(ctx); err != nil {
		return User{}, err
	}
	u = normalizeUser(u)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("save user: %w", err)
	}
	defer tx.Rollback() // after a commit this does nothing
	saved, err := scanUser(tx.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE username = ?", u.Username))
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx, "UPDATE users SET password_hash = ? WHERE id = ?", u.PasswordHash, saved.ID); err != nil {
			return User{}, fmt.Errorf("save user: %w", err)
		}
		saved.PasswordHash = u.PasswordHash
	case errors.Is(err, ErrNotFound):
		res, err := tx.ExecContext(ctx, "INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)",
			u.Username, u.PasswordHash, u.CreatedAt.UnixMilli())
		if err != nil {
			return User{}, fmt.Errorf("save user: %w", err)
		}
		if u.ID, err = res.LastInsertId(); err != nil {
			return User{}, fmt.Errorf("save user: %w", err)
		}
		saved = u
	default:
		return User{}, fmt.Errorf("save user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("save user: %w", err)
	}
	return saved, nil
}

// UserByName returns the user with that username, ignoring case.
func (s *SQLite) UserByName(ctx context.Context, name string) (User, error) {
	if err := s.guard(ctx); err != nil {
		return User{}, err
	}
	u, err := scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE username = ?", name))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return User{}, fmt.Errorf("find user: %w", err)
	}
	return u, err
}

// UserByID returns the user with that ID.
func (s *SQLite) UserByID(ctx context.Context, id int64) (User, error) {
	if err := s.guard(ctx); err != nil {
		return User{}, err
	}
	u, err := scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id = ?", id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return User{}, fmt.Errorf("find user: %w", err)
	}
	return u, err
}

// CountUsers returns how many users there are.
func (s *SQLite) CountUsers(ctx context.Context) (int, error) {
	if err := s.guard(ctx); err != nil {
		return 0, err
	}
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// AddSession stores a session. The user must exist and the token hash must be new.
func (s *SQLite) AddSession(ctx context.Context, sess Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sess.TokenHash == "" {
		return fmt.Errorf("%w: no token hash", ErrInvalidSession)
	}
	if err := s.guard(ctx); err != nil {
		return err
	}
	sess = normalizeSession(sess)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("add session: %w", err)
	}
	defer tx.Rollback() // after a commit this does nothing
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE id = ?)", sess.UserID).Scan(&exists); err != nil {
		return fmt.Errorf("add session: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: user %d does not exist", ErrInvalidSession, sess.UserID)
	}
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM sessions WHERE token_hash = ?)", sess.TokenHash).Scan(&exists); err != nil {
		return fmt.Errorf("add session: %w", err)
	}
	if exists {
		return fmt.Errorf("%w: the token hash is already in use", ErrInvalidSession)
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		sess.TokenHash, sess.UserID, sess.CreatedAt.UnixMilli(), sess.LastSeenAt.UnixMilli(), sess.ExpiresAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("add session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("add session: %w", err)
	}
	return nil
}

// Session returns the session with that token hash.
func (s *SQLite) Session(ctx context.Context, tokenHash string) (Session, error) {
	if err := s.guard(ctx); err != nil {
		return Session{}, err
	}
	var (
		sess                             Session
		createdAt, lastSeenAt, expiresAt int64
	)
	err := s.db.QueryRowContext(ctx, "SELECT token_hash, user_id, created_at, last_seen_at, expires_at FROM sessions WHERE token_hash = ?", tokenHash).
		Scan(&sess.TokenHash, &sess.UserID, &createdAt, &lastSeenAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("find session: %w", err)
	}
	sess.CreatedAt, sess.LastSeenAt, sess.ExpiresAt = time.UnixMilli(createdAt).UTC(), time.UnixMilli(lastSeenAt).UTC(), time.UnixMilli(expiresAt).UTC()
	return sess, nil
}

// execOne runs a statement that must change exactly one row, and gives ErrNotFound when it changes none.
func (s *SQLite) execOne(ctx context.Context, what, query string, args ...any) error {
	if err := s.guard(ctx); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchSession sets the last seen time of a session.
func (s *SQLite) TouchSession(ctx context.Context, tokenHash string, at time.Time) error {
	return s.execOne(ctx, "touch session", "UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?", at.UnixMilli(), tokenHash)
}

// DeleteSession removes a session.
func (s *SQLite) DeleteSession(ctx context.Context, tokenHash string) error {
	return s.execOne(ctx, "delete session", "DELETE FROM sessions WHERE token_hash = ?", tokenHash)
}

// DeleteUserSessions removes every session of a user. A user without sessions is not an error.
func (s *SQLite) DeleteUserSessions(ctx context.Context, userID int64) error {
	if err := s.guard(ctx); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}

// DeleteSessionsBefore removes the sessions that expired at or before expiredBefore, or were last seen at or before
// idleBefore, and returns how many.
func (s *SQLite) DeleteSessionsBefore(ctx context.Context, expiredBefore, idleBefore time.Time) (int64, error) {
	if err := s.guard(ctx); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?", expiredBefore.UnixMilli(), idleBefore.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("delete old sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete old sessions: %w", err)
	}
	return n, nil
}
