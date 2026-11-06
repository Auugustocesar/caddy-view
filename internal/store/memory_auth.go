package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// userIndex returns the position of the user with that username, ignoring case, or -1.
func (m *Memory) userIndex(name string) int {
	for i, u := range m.users {
		if strings.EqualFold(u.Username, name) {
			return i
		}
	}
	return -1
}

// SaveUser creates the user, or replaces the password hash of the user with the same username, ignoring case.
func (m *Memory) SaveUser(ctx context.Context, u User) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	if err := validateUser(u); err != nil {
		return User{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return User{}, ErrClosed
	}
	if i := m.userIndex(u.Username); i >= 0 {
		m.users[i].PasswordHash = u.PasswordHash
		return m.users[i], nil
	}
	u = normalizeUser(u)
	u.ID = int64(len(m.users) + 1)
	m.users = append(m.users, u)
	return u, nil
}

// UserByName returns the user with that username, ignoring case.
func (m *Memory) UserByName(ctx context.Context, name string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return User{}, ErrClosed
	}
	if i := m.userIndex(name); i >= 0 {
		return m.users[i], nil
	}
	return User{}, ErrNotFound
}

// UserByID returns the user with that ID.
func (m *Memory) UserByID(ctx context.Context, id int64) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return User{}, ErrClosed
	}
	if id < 1 || id > int64(len(m.users)) {
		return User{}, ErrNotFound
	}
	return m.users[id-1], nil
}

// CountUsers returns how many users there are.
func (m *Memory) CountUsers(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, ErrClosed
	}
	return len(m.users), nil
}

// AddSession stores a session.
func (m *Memory) AddSession(ctx context.Context, s Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.TokenHash == "" {
		return fmt.Errorf("%w: no token hash", ErrInvalidSession)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if s.UserID < 1 || s.UserID > int64(len(m.users)) {
		return fmt.Errorf("%w: user %d does not exist", ErrInvalidSession, s.UserID)
	}
	if _, used := m.sessions[s.TokenHash]; used {
		return fmt.Errorf("%w: the token hash is already in use", ErrInvalidSession)
	}
	m.sessions[s.TokenHash] = normalizeSession(s)
	return nil
}

// Session returns the session with that token hash.
func (m *Memory) Session(ctx context.Context, tokenHash string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Session{}, ErrClosed
	}
	s, ok := m.sessions[tokenHash]
	if !ok {
		return Session{}, ErrNotFound
	}
	return s, nil
}

// TouchSession sets the last seen time of a session.
func (m *Memory) TouchSession(ctx context.Context, tokenHash string, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	s, ok := m.sessions[tokenHash]
	if !ok {
		return ErrNotFound
	}
	s.LastSeenAt = time.UnixMilli(at.UnixMilli()).UTC()
	m.sessions[tokenHash] = s
	return nil
}

// DeleteSession removes a session.
func (m *Memory) DeleteSession(ctx context.Context, tokenHash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if _, ok := m.sessions[tokenHash]; !ok {
		return ErrNotFound
	}
	delete(m.sessions, tokenHash)
	return nil
}

// DeleteUserSessions removes every session of a user. A user without sessions is not an error.
func (m *Memory) DeleteUserSessions(ctx context.Context, userID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	for hash, s := range m.sessions {
		if s.UserID == userID {
			delete(m.sessions, hash)
		}
	}
	return nil
}

// DeleteSessionsBefore removes the sessions that expired at or before expiredBefore, or were last seen at or before
// idleBefore, and returns how many.
func (m *Memory) DeleteSessionsBefore(ctx context.Context, expiredBefore, idleBefore time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, ErrClosed
	}
	var n int64
	for hash, s := range m.sessions {
		if !s.ExpiresAt.After(expiredBefore) || !s.LastSeenAt.After(idleBefore) {
			delete(m.sessions, hash)
			n++
		}
	}
	return n, nil
}
