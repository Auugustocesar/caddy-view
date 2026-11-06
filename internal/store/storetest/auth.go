package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// AuthStore is the part of a store the users and sessions contract checks.
type AuthStore interface {
	SaveUser(ctx context.Context, u store.User) (store.User, error)
	UserByName(ctx context.Context, name string) (store.User, error)
	UserByID(ctx context.Context, id int64) (store.User, error)
	CountUsers(ctx context.Context) (int, error)
	AddSession(ctx context.Context, s store.Session) error
	Session(ctx context.Context, tokenHash string) (store.Session, error)
	TouchSession(ctx context.Context, tokenHash string, at time.Time) error
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteUserSessions(ctx context.Context, userID int64) error
	DeleteSessionsBefore(ctx context.Context, expiredBefore, idleBefore time.Time) (int64, error)
	Close() error
}

// session is a session of user id whose token hash is hash, created at base and valid for a day.
func session(hash string, id int64) store.Session {
	return store.Session{TokenHash: hash, UserID: id, CreatedAt: base, LastSeenAt: base, ExpiresAt: base.Add(24 * time.Hour)}
}

// RunAuth checks the users and sessions part of the contract against stores made by newStore. Every subtest gets a
// fresh store.
func RunAuth(t *testing.T, newStore func(t *testing.T) AuthStore) {
	t.Helper()
	fresh := func(t *testing.T) AuthStore {
		t.Helper()
		s := newStore(t)
		t.Cleanup(func() { s.Close() })
		return s
	}
	ctx := context.Background()
	saveUser := func(t *testing.T, s AuthStore, name string) store.User {
		t.Helper()
		u, err := s.SaveUser(ctx, store.User{Username: name, PasswordHash: "hash-of-" + name, CreatedAt: base})
		if err != nil {
			t.Fatalf("SaveUser(%s): %v", name, err)
		}
		return u
	}

	t.Run("an empty store has no users", func(t *testing.T) {
		s := fresh(t)
		if n, err := s.CountUsers(ctx); err != nil || n != 0 {
			t.Errorf("CountUsers = %d, %v, want 0", n, err)
		}
		if _, err := s.UserByName(ctx, "admin"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("UserByName = %v, want ErrNotFound", err)
		}
		if _, err := s.UserByID(ctx, 1); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("UserByID = %v, want ErrNotFound", err)
		}
	})

	t.Run("a saved user reads back by name and by ID, and the name ignores case", func(t *testing.T) {
		s := fresh(t)
		saved := saveUser(t, s, "admin")
		if saved.ID == 0 {
			t.Fatal("SaveUser returned no ID")
		}
		for _, name := range []string{"admin", "ADMIN"} {
			got, err := s.UserByName(ctx, name)
			if err != nil || got.ID != saved.ID || got.Username != "admin" || got.PasswordHash != "hash-of-admin" || !got.CreatedAt.Equal(base) {
				t.Errorf("UserByName(%q) = %+v, %v, want %+v", name, got, err, saved)
			}
		}
		byID, err := s.UserByID(ctx, saved.ID)
		if err != nil || byID.Username != "admin" {
			t.Errorf("UserByID = %+v, %v", byID, err)
		}
		if n, _ := s.CountUsers(ctx); n != 1 {
			t.Errorf("CountUsers = %d, want 1", n)
		}
	})

	t.Run("saving a username again replaces the hash and keeps the ID and the creation time", func(t *testing.T) {
		s := fresh(t)
		first := saveUser(t, s, "admin")
		again, err := s.SaveUser(ctx, store.User{Username: "Admin", PasswordHash: "new-hash", CreatedAt: base.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if again.ID != first.ID || again.PasswordHash != "new-hash" || !again.CreatedAt.Equal(base) {
			t.Errorf("SaveUser again = %+v, want ID %d, the new hash and the first creation time", again, first.ID)
		}
		if n, _ := s.CountUsers(ctx); n != 1 {
			t.Errorf("CountUsers = %d, want 1", n)
		}
		got, _ := s.UserByName(ctx, "admin")
		if got.PasswordHash != "new-hash" {
			t.Errorf("the stored hash is %q, want the new one", got.PasswordHash)
		}
	})

	t.Run("a user needs a username and a hash", func(t *testing.T) {
		s := fresh(t)
		for _, u := range []store.User{{PasswordHash: "h"}, {Username: "admin"}, {Username: "  ", PasswordHash: "h"}} {
			if _, err := s.SaveUser(ctx, u); !errors.Is(err, store.ErrInvalidUser) {
				t.Errorf("SaveUser(%+v) = %v, want ErrInvalidUser", u, err)
			}
		}
		if n, _ := s.CountUsers(ctx); n != 0 {
			t.Errorf("CountUsers = %d, want 0 after refusals", n)
		}
	})

	t.Run("a session reads back as stored, and an unknown one is not found", func(t *testing.T) {
		s := fresh(t)
		u := saveUser(t, s, "admin")
		want := session("h1", u.ID)
		if err := s.AddSession(ctx, want); err != nil {
			t.Fatal(err)
		}
		got, err := s.Session(ctx, "h1")
		if err != nil || got.UserID != u.ID || !got.CreatedAt.Equal(want.CreatedAt) || !got.LastSeenAt.Equal(want.LastSeenAt) || !got.ExpiresAt.Equal(want.ExpiresAt) {
			t.Errorf("Session = %+v, %v, want %+v", got, err, want)
		}
		if _, err := s.Session(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Session(nope) = %v, want ErrNotFound", err)
		}
	})

	t.Run("a session needs a token hash and a user that exists", func(t *testing.T) {
		s := fresh(t)
		u := saveUser(t, s, "admin")
		if err := s.AddSession(ctx, session("", u.ID)); !errors.Is(err, store.ErrInvalidSession) {
			t.Errorf("AddSession without a hash = %v, want ErrInvalidSession", err)
		}
		if err := s.AddSession(ctx, session("h", u.ID+99)); !errors.Is(err, store.ErrInvalidSession) {
			t.Errorf("AddSession for a missing user = %v, want ErrInvalidSession", err)
		}
		if err := s.AddSession(ctx, session("h", u.ID)); err != nil {
			t.Fatal(err)
		}
		if err := s.AddSession(ctx, session("h", u.ID)); !errors.Is(err, store.ErrInvalidSession) {
			t.Errorf("AddSession twice with one hash = %v, want ErrInvalidSession", err)
		}
	})

	t.Run("touching a session moves its last seen time only", func(t *testing.T) {
		s := fresh(t)
		u := saveUser(t, s, "admin")
		orig := session("h", u.ID)
		if err := s.AddSession(ctx, orig); err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Hour)
		if err := s.TouchSession(ctx, "h", at); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Session(ctx, "h")
		if !got.LastSeenAt.Equal(at) || !got.ExpiresAt.Equal(orig.ExpiresAt) || !got.CreatedAt.Equal(orig.CreatedAt) {
			t.Errorf("after the touch: %+v, want only LastSeenAt = %v", got, at)
		}
		if err := s.TouchSession(ctx, "nope", at); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("TouchSession(nope) = %v, want ErrNotFound", err)
		}
	})

	t.Run("deleting a session removes that one only, and deleting it twice says not found", func(t *testing.T) {
		s := fresh(t)
		u := saveUser(t, s, "admin")
		for _, h := range []string{"a", "b"} {
			if err := s.AddSession(ctx, session(h, u.ID)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.DeleteSession(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Session(ctx, "a"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("session a after its delete: %v", err)
		}
		if _, err := s.Session(ctx, "b"); err != nil {
			t.Errorf("session b: %v", err)
		}
		if err := s.DeleteSession(ctx, "a"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("DeleteSession twice = %v, want ErrNotFound", err)
		}
	})

	t.Run("deleting the sessions of a user leaves the others", func(t *testing.T) {
		s := fresh(t)
		a, b := saveUser(t, s, "a"), saveUser(t, s, "b")
		for h, id := range map[string]int64{"a1": a.ID, "a2": a.ID, "b1": b.ID} {
			if err := s.AddSession(ctx, session(h, id)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.DeleteUserSessions(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
		for _, h := range []string{"a1", "a2"} {
			if _, err := s.Session(ctx, h); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("session %s survived: %v", h, err)
			}
		}
		if _, err := s.Session(ctx, "b1"); err != nil {
			t.Errorf("session b1: %v", err)
		}
		if err := s.DeleteUserSessions(ctx, a.ID); err != nil {
			t.Errorf("deleting what is already gone should not fail: %v", err)
		}
	})

	t.Run("pruning removes the expired and the idle sessions and says how many", func(t *testing.T) {
		s := fresh(t)
		u := saveUser(t, s, "admin")
		now := base.Add(48 * time.Hour)
		cases := map[string]store.Session{
			"live":    {TokenHash: "live", UserID: u.ID, CreatedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)},
			"expired": {TokenHash: "expired", UserID: u.ID, CreatedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Minute), ExpiresAt: now},
			"idle":    {TokenHash: "idle", UserID: u.ID, CreatedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(time.Hour)},
		}
		for _, c := range cases {
			if err := s.AddSession(ctx, c); err != nil {
				t.Fatal(err)
			}
		}
		n, err := s.DeleteSessionsBefore(ctx, now, now.Add(-24*time.Hour))
		if err != nil || n != 2 {
			t.Fatalf("DeleteSessionsBefore = %d, %v, want 2", n, err)
		}
		if _, err := s.Session(ctx, "live"); err != nil {
			t.Errorf("the live session was removed: %v", err)
		}
		for _, h := range []string{"expired", "idle"} {
			if _, err := s.Session(ctx, h); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("session %s survived: %v", h, err)
			}
		}
	})

	t.Run("a closed store refuses with ErrClosed", func(t *testing.T) {
		s := newStore(t)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CountUsers(ctx); !errors.Is(err, store.ErrClosed) {
			t.Errorf("CountUsers after Close = %v, want ErrClosed", err)
		}
		if _, err := s.Session(ctx, "h"); !errors.Is(err, store.ErrClosed) {
			t.Errorf("Session after Close = %v, want ErrClosed", err)
		}
	})
}
