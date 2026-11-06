package store

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidUser means a user cannot be stored: it has no username or no password hash.
var ErrInvalidUser = errors.New("invalid user")

// ErrInvalidSession means a session cannot be stored: it has no token hash, its hash is already used, or its user
// does not exist.
var ErrInvalidSession = errors.New("invalid session")

// User is someone who can log in to the UI.
type User struct {
	// ID is assigned by the store when the user is first saved. It is ignored on a save of a username that exists.
	ID       int64
	Username string
	// PasswordHash is the encoded hash of the password, never the password. The store keeps it as given.
	PasswordHash string
	CreatedAt    time.Time
}

// Session is a login that is still valid. It is known by the hash of its token, never by the token.
type Session struct {
	// TokenHash is the lowercase hex SHA-256 of the token the browser holds.
	TokenHash  string
	UserID     int64
	CreatedAt  time.Time
	LastSeenAt time.Time
	// ExpiresAt is when the session ends whatever its use.
	ExpiresAt time.Time
}

// validateUser checks a user before it is stored.
func validateUser(u User) error {
	switch {
	case strings.TrimSpace(u.Username) == "":
		return fmt.Errorf("%w: no username", ErrInvalidUser)
	case u.PasswordHash == "":
		return fmt.Errorf("%w: no password hash for %s", ErrInvalidUser, u.Username)
	}
	return nil
}

// normalizeUser rounds the creation time to the precision the stores keep.
func normalizeUser(u User) User {
	u.CreatedAt = time.UnixMilli(u.CreatedAt.UnixMilli()).UTC()
	return u
}

// normalizeSession rounds the times of a session to the precision the stores keep.
func normalizeSession(s Session) Session {
	s.CreatedAt = time.UnixMilli(s.CreatedAt.UnixMilli()).UTC()
	s.LastSeenAt = time.UnixMilli(s.LastSeenAt.UnixMilli()).UTC()
	s.ExpiresAt = time.UnixMilli(s.ExpiresAt.UnixMilli()).UTC()
	return s
}
