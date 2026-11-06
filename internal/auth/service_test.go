package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/auth"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

var start = time.Date(2026, time.November, 6, 12, 0, 0, 0, time.UTC)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// rig is a service over an in-memory store with a clock the test moves.
type rig struct {
	svc   *auth.Service
	store *store.Memory
	clock *fakeClock
}

func newRig(t *testing.T) *rig {
	t.Helper()
	return newRigWith(t, store.NewMemory())
}

func newRigWith(t *testing.T, st auth.Store) *rig {
	t.Helper()
	c := &fakeClock{now: start}
	mem, _ := st.(*store.Memory)
	return &rig{svc: auth.New(st, auth.Options{Now: c.Now, Params: cheap}), store: mem, clock: c}
}

// addUser stores a user whose password is pw.
func (r *rig) addUser(t *testing.T, name, pw string) store.User {
	t.Helper()
	hash, err := auth.HashPassword(pw, cheap)
	if err != nil {
		t.Fatal(err)
	}
	u, err := r.store.SaveUser(context.Background(), store.User{Username: name, PasswordHash: hash, CreatedAt: start})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func hashOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func TestLoginGivesATokenThatAuthenticatesTheUser(t *testing.T) {
	r := newRig(t)
	u := r.addUser(t, "admin", "secret")
	ctx := context.Background()

	token, err := r.svc.Login(ctx, "admin", "secret", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 40 {
		t.Errorf("token %q is too short to be unguessable", token)
	}
	got, err := r.svc.Authenticate(ctx, token)
	if err != nil || got.ID != u.ID || got.Username != "admin" {
		t.Errorf("Authenticate = %+v, %v, want the user %+v", got, err, u)
	}
	if got.PasswordHash != "" {
		t.Error("Authenticate returns the password hash; the caller has no use for it")
	}
}

func TestLoginMatchesTheUsernameIgnoringCase(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	if _, err := r.svc.Login(context.Background(), "ADMIN", "secret", "c"); err != nil {
		t.Errorf("Login as ADMIN: %v", err)
	}
}

func TestLoginKeepsOnlyTheHashOfTheToken(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	ctx := context.Background()
	token, err := r.svc.Login(ctx, "admin", "secret", "c")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.Session(ctx, token); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the store knows the session by the token itself: %v", err)
	}
	sess, err := r.store.Session(ctx, hashOf(token))
	if err != nil {
		t.Fatalf("no session under the SHA-256 of the token: %v", err)
	}
	if !sess.CreatedAt.Equal(start) || !sess.LastSeenAt.Equal(start) || !sess.ExpiresAt.Equal(start.Add(auth.SessionLifetime)) {
		t.Errorf("session times = %v, %v, %v, want now, now and now + SessionLifetime", sess.CreatedAt, sess.LastSeenAt, sess.ExpiresAt)
	}
}

func TestEachLoginGetsItsOwnToken(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	a, errA := r.svc.Login(context.Background(), "admin", "secret", "c")
	b, errB := r.svc.Login(context.Background(), "admin", "secret", "c")
	if errA != nil || errB != nil || a == b {
		t.Errorf("tokens %q and %q (%v, %v), want two different ones", a, b, errA, errB)
	}
}

func TestLoginRefusesWrongCredentialsWithOneError(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	for name, c := range map[string][2]string{
		"wrong password":   {"admin", "nope"},
		"unknown user":     {"root", "secret"},
		"empty username":   {"", "secret"},
		"empty password":   {"admin", ""},
		"huge password":    {"admin", strings.Repeat("x", auth.MaxPasswordLength+1)},
		"password prefix":  {"admin", "secre"},
		"password suffix":  {"admin", "secret "},
		"blank username":   {"  ", "secret"},
		"username as pass": {"admin", "admin"},
	} {
		t.Run(name, func(t *testing.T) {
			token, err := r.svc.Login(context.Background(), c[0], c[1], "c"+name)
			if !errors.Is(err, auth.ErrInvalidCredentials) || token != "" {
				t.Errorf("Login(%q, %q) = %q, %v, want ErrInvalidCredentials and no token", c[0], c[1], token, err)
			}
		})
	}
}

func TestLoginStopsAfterTooManyFailuresFromOneClient(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	ctx := context.Background()
	for range auth.MaxFailures {
		if _, err := r.svc.Login(ctx, "admin", "wrong", "1.2.3.4"); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("a failure before the limit: %v", err)
		}
	}
	// The right password is refused too while the client is held back: that is what makes guessing slow.
	_, err := r.svc.Login(ctx, "admin", "secret", "1.2.3.4")
	var limited *auth.RateLimitError
	if !errors.Is(err, auth.ErrRateLimited) || !errors.As(err, &limited) {
		t.Fatalf("Login after %d failures = %v, want ErrRateLimited", auth.MaxFailures, err)
	}
	if limited.RetryAfter <= 0 || limited.RetryAfter > auth.FailureWindow {
		t.Errorf("RetryAfter = %v, want a positive time of at most %v", limited.RetryAfter, auth.FailureWindow)
	}
	if _, err := r.svc.Login(ctx, "admin", "secret", "5.6.7.8"); err != nil {
		t.Errorf("another client is held back as well: %v", err)
	}

	r.clock.advance(auth.FailureWindow)
	if _, err := r.svc.Login(ctx, "admin", "secret", "1.2.3.4"); err != nil {
		t.Errorf("Login after the window passed: %v", err)
	}
}

func TestASuccessfulLoginClearsTheFailures(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	ctx := context.Background()
	for range auth.MaxFailures - 1 {
		_, _ = r.svc.Login(ctx, "admin", "wrong", "c")
	}
	if _, err := r.svc.Login(ctx, "admin", "secret", "c"); err != nil {
		t.Fatal(err)
	}
	for range auth.MaxFailures - 1 {
		if _, err := r.svc.Login(ctx, "admin", "wrong", "c"); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("a failure after the success: %v", err)
		}
	}
}

func TestLoginReportsAStoreFailureAndNotWrongCredentials(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	boom := errors.New("disk is gone")
	svc := auth.New(failingStore{Store: r.store, err: boom}, auth.Options{Now: r.clock.Now, Params: cheap})
	_, err := svc.Login(context.Background(), "admin", "secret", "c")
	if !errors.Is(err, boom) || errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("Login = %v, want the store's error and not ErrInvalidCredentials", err)
	}
}

// failingStore fails to add a session; everything else goes to the store.
type failingStore struct {
	auth.Store
	err error
}

func (f failingStore) AddSession(context.Context, store.Session) error { return f.err }

func TestLoginRefusesAStoredHashThatIsNotAHash(t *testing.T) {
	r := newRig(t)
	if _, err := r.store.SaveUser(context.Background(), store.User{Username: "admin", PasswordHash: "plain", CreatedAt: start}); err != nil {
		t.Fatal(err)
	}
	_, err := r.svc.Login(context.Background(), "admin", "plain", "c")
	if !errors.Is(err, auth.ErrInvalidHash) {
		t.Errorf("Login = %v, want ErrInvalidHash: a corrupt hash must be visible, and never match", err)
	}
}

func TestAuthenticateRefusesWhatIsNotASession(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	for name, token := range map[string]string{"empty": "", "unknown": "AAAA", "a hash used as a token": hashOf("x")} {
		if _, err := r.svc.Authenticate(context.Background(), token); !errors.Is(err, auth.ErrNoSession) {
			t.Errorf("%s: Authenticate = %v, want ErrNoSession", name, err)
		}
	}
}

func TestASessionEndsAfterItsLifetimeWhateverItsUse(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	ctx := context.Background()
	token, _ := r.svc.Login(ctx, "admin", "secret", "c")
	// Use it every few hours, so it is never idle, until just before the end.
	for elapsed := time.Duration(0); elapsed+6*time.Hour < auth.SessionLifetime; elapsed += 6 * time.Hour {
		r.clock.advance(6 * time.Hour)
		if _, err := r.svc.Authenticate(ctx, token); err != nil {
			t.Fatalf("after %v of use: %v", elapsed+6*time.Hour, err)
		}
	}
	r.clock.now = start.Add(auth.SessionLifetime)
	if _, err := r.svc.Authenticate(ctx, token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("Authenticate at the end of the lifetime = %v, want ErrNoSession", err)
	}
}

func TestASessionEndsAfterItIsIdle(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	ctx := context.Background()
	token, _ := r.svc.Login(ctx, "admin", "secret", "c")
	r.clock.advance(auth.IdleTimeout - time.Minute)
	if _, err := r.svc.Authenticate(ctx, token); err != nil {
		t.Fatalf("just before the idle timeout: %v", err)
	}
	// That use counts: the session lives another IdleTimeout from it.
	r.clock.advance(auth.IdleTimeout - time.Minute)
	if _, err := r.svc.Authenticate(ctx, token); err != nil {
		t.Fatalf("a session in use was ended as idle: %v", err)
	}
	r.clock.advance(auth.IdleTimeout)
	if _, err := r.svc.Authenticate(ctx, token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("after being idle = %v, want ErrNoSession", err)
	}
}

func TestAuthenticateWritesTheLastSeenTimeAtMostOncePerMinute(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	ctx := context.Background()
	token, _ := r.svc.Login(ctx, "admin", "secret", "c")
	lastSeen := func() time.Time {
		t.Helper()
		s, err := r.store.Session(ctx, hashOf(token))
		if err != nil {
			t.Fatal(err)
		}
		return s.LastSeenAt
	}
	r.clock.advance(10 * time.Second)
	_, _ = r.svc.Authenticate(ctx, token)
	if got := lastSeen(); !got.Equal(start) {
		t.Errorf("last seen after 10s = %v, want it unchanged: every request must not write to the database", got)
	}
	r.clock.advance(time.Minute)
	_, _ = r.svc.Authenticate(ctx, token)
	if got := lastSeen(); !got.Equal(r.clock.now) {
		t.Errorf("last seen after 70s = %v, want %v", got, r.clock.now)
	}
}

func TestAuthenticateRefusesASessionOfAUserThatIsGone(t *testing.T) {
	// A store cannot hold a session without its user, but a service must not trust that to authenticate.
	r := newRig(t)
	u := r.addUser(t, "admin", "secret")
	ctx := context.Background()
	token, _ := r.svc.Login(ctx, "admin", "secret", "c")
	svc := auth.New(orphanStore{Store: r.store, missing: u.ID}, auth.Options{Now: r.clock.Now, Params: cheap})
	if _, err := svc.Authenticate(ctx, token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("Authenticate = %v, want ErrNoSession", err)
	}
}

// orphanStore behaves as if the user with ID missing had been deleted without its sessions.
type orphanStore struct {
	auth.Store
	missing int64
}

func (o orphanStore) UserByID(ctx context.Context, id int64) (store.User, error) {
	if id == o.missing {
		return store.User{}, store.ErrNotFound
	}
	return o.Store.UserByID(ctx, id)
}

func TestLogoutEndsTheSessionAndIsRepeatable(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	ctx := context.Background()
	token, _ := r.svc.Login(ctx, "admin", "secret", "c")
	other, _ := r.svc.Login(ctx, "admin", "secret", "c")
	if err := r.svc.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Authenticate(ctx, token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("Authenticate after Logout = %v, want ErrNoSession", err)
	}
	if _, err := r.svc.Authenticate(ctx, other); err != nil {
		t.Errorf("another session ended with it: %v", err)
	}
	if err := r.svc.Logout(ctx, token); err != nil {
		t.Errorf("Logout twice = %v, want nil: leaving twice is not a failure", err)
	}
	if err := r.svc.Logout(ctx, ""); err != nil {
		t.Errorf("Logout of no session = %v, want nil", err)
	}
}

func TestEnsureAdminCreatesTheFirstUserOnceWithARandomPassword(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	pw, created, err := r.svc.EnsureAdmin(ctx)
	if err != nil || !created {
		t.Fatalf("EnsureAdmin = %q, %v, %v, want a created user", pw, created, err)
	}
	if len(pw) < 20 {
		t.Errorf("password %q is too short to be safe", pw)
	}
	if _, err := r.svc.Login(ctx, auth.AdminUsername, pw, "c"); err != nil {
		t.Errorf("the printed password does not log in: %v", err)
	}
	u, err := r.store.UserByName(ctx, auth.AdminUsername)
	if err != nil || strings.Contains(u.PasswordHash, pw) {
		t.Errorf("stored user = %+v, %v: the hash must not contain the password", u, err)
	}

	again, created, err := r.svc.EnsureAdmin(ctx)
	if err != nil || created || again != "" {
		t.Errorf("EnsureAdmin again = %q, %v, %v, want nothing created and no password", again, created, err)
	}
	if _, err := r.svc.Login(ctx, auth.AdminUsername, pw, "c"); err != nil {
		t.Errorf("the first password stopped working: %v", err)
	}
}

func TestEnsureAdminLeavesAStoreWithAnyUserAlone(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "someone", "pw")
	if pw, created, err := r.svc.EnsureAdmin(context.Background()); err != nil || created || pw != "" {
		t.Errorf("EnsureAdmin = %q, %v, %v, want nothing created", pw, created, err)
	}
	if n, _ := r.store.CountUsers(context.Background()); n != 1 {
		t.Errorf("users = %d, want 1", n)
	}
}

func TestGeneratedPasswordsComeFromTheRandomSource(t *testing.T) {
	c := &fakeClock{now: start}
	fixed := bytes.Repeat([]byte{7}, 64)
	mk := func() *auth.Service {
		return auth.New(store.NewMemory(), auth.Options{Now: c.Now, Params: cheap, Rand: bytes.NewReader(fixed)})
	}
	a, _, errA := mk().EnsureAdmin(context.Background())
	b, _, errB := mk().EnsureAdmin(context.Background())
	if errA != nil || errB != nil || a != b || a == "" {
		t.Errorf("passwords %q and %q (%v, %v), want the same one from the same bytes", a, b, errA, errB)
	}
}

func TestEnsureAdminFailsWhenThereIsNoRandomness(t *testing.T) {
	svc := auth.New(store.NewMemory(), auth.Options{Params: cheap, Rand: bytes.NewReader([]byte{1, 2, 3})})
	pw, created, err := svc.EnsureAdmin(context.Background())
	if err == nil || created || pw != "" {
		t.Errorf("EnsureAdmin with too few random bytes = %q, %v, %v, want an error and no user", pw, created, err)
	}
}

func TestResetPasswordChangesItAndEndsEverySession(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "old")
	ctx := context.Background()
	token, _ := r.svc.Login(ctx, "admin", "old", "c")

	pw, err := r.svc.ResetPassword(ctx, "admin")
	if err != nil || len(pw) < 20 {
		t.Fatalf("ResetPassword = %q, %v", pw, err)
	}
	if _, err := r.svc.Authenticate(ctx, token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("a session survived the reset: %v", err)
	}
	if _, err := r.svc.Login(ctx, "admin", "old", "c1"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("the old password still works: %v", err)
	}
	if _, err := r.svc.Login(ctx, "admin", pw, "c2"); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
}

func TestResetPasswordOfAnUnknownUser(t *testing.T) {
	r := newRig(t)
	pw, err := r.svc.ResetPassword(context.Background(), "ghost")
	if !errors.Is(err, auth.ErrUnknownUser) || pw != "" {
		t.Errorf("ResetPassword = %q, %v, want ErrUnknownUser and no password", pw, err)
	}
}

func TestPruneRemovesEndedSessionsOnly(t *testing.T) {
	r := newRig(t)
	r.addUser(t, "admin", "secret")
	ctx := context.Background()
	old, _ := r.svc.Login(ctx, "admin", "secret", "c")
	r.clock.advance(auth.IdleTimeout + time.Hour)
	live, _ := r.svc.Login(ctx, "admin", "secret", "c")

	n, err := r.svc.Prune(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v, want 1", n, err)
	}
	if _, err := r.store.Session(ctx, hashOf(old)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the idle session was kept: %v", err)
	}
	if _, err := r.store.Session(ctx, hashOf(live)); err != nil {
		t.Errorf("the live session was removed: %v", err)
	}
}
