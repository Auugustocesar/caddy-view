package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// The rules of login. They are constants, not settings: a login that can be configured loose is a login that gets
// configured loose.
const (
	// SessionLifetime is how long a session lasts at most, however much it is used.
	SessionLifetime = 7 * 24 * time.Hour
	// IdleTimeout ends a session that has not been used for this long.
	IdleTimeout = 24 * time.Hour
	// MaxFailures failed logins from one client within FailureWindow hold that client back.
	MaxFailures = 5
	// FailureWindow is how long a failed login counts against its client.
	FailureWindow = 15 * time.Minute
	// MaxPasswordLength is the longest password looked at. Anything longer is refused without hashing it.
	MaxPasswordLength = 1024
	// AdminUsername is the account created at the first start.
	AdminUsername = "admin"

	// touchEvery is the least time between two writes of a session's last seen time.
	touchEvery = time.Minute
	// generatedPasswordBytes of randomness make a generated password of 24 characters.
	generatedPasswordBytes = 18
	tokenBytes             = 32
)

var (
	// ErrInvalidCredentials means the username or the password is wrong. It does not say which.
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrRateLimited means a client has failed too often and must wait; the error is a *RateLimitError.
	ErrRateLimited = errors.New("too many failed logins")
	// ErrNoSession means a token is not a valid session: unknown, ended, expired, or idle for too long.
	ErrNoSession = errors.New("no valid session")
	// ErrUnknownUser means there is no user with that name.
	ErrUnknownUser = errors.New("unknown user")
)

// RateLimitError is the error of a login refused because its client failed too often.
type RateLimitError struct {
	// RetryAfter is how long until the client may try again.
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("too many failed logins, try again in %s", e.RetryAfter.Round(time.Second))
}

// Unwrap makes errors.Is(err, ErrRateLimited) true.
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

// Store is what the service needs from the database.
type Store interface {
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
}

// Options configures a Service. The zero value is what serve uses.
type Options struct {
	// Now gives the current time. Zero means time.Now.
	Now func() time.Time
	// Params is the cost of new password hashes. Zero means DefaultParams.
	Params Params
	// Rand is the source of generated passwords. Nil means crypto/rand. Session tokens and salts always come from
	// crypto/rand.
	Rand io.Reader
}

// Service logs users in and keeps their sessions. It is safe for concurrent use.
type Service struct {
	store   Store
	now     func() time.Time
	params  Params
	rand    io.Reader
	limiter *limiter
	// verify checks a password against a hash. It is a field so that a test can count the checks.
	verify func(hash, password string) (bool, error)
	// decoy is a valid hash that no password matches, checked when the username is unknown so that the answer takes as
	// long as for a known one.
	decoy string
}

// New returns a Service over st.
func New(st Store, opts Options) *Service {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	params := opts.Params
	if params == (Params{}) {
		params = DefaultParams
	}
	random := opts.Rand
	if random == nil {
		random = rand.Reader
	}
	// A failure here means invalid parameters; the decoy is then empty, and a check against it fails at once, which
	// only costs the timing, while hashing a real password reports the same error loudly.
	decoy, _ := HashPassword("decoy", params)
	s := &Service{store: st, now: now, params: params, rand: random, verify: VerifyPassword, decoy: decoy}
	s.limiter = newLimiter(MaxFailures, FailureWindow, now)
	return s
}

// hashToken is how a session is known to the database: the lowercase hex SHA-256 of its token.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Login checks the credentials and starts a session, returning its token. client names who is asking (an address), so
// that failures are counted per client. It returns ErrInvalidCredentials for a wrong username or password, a
// *RateLimitError for a client held back, and any other error for a problem that is not the caller's.
func (s *Service) Login(ctx context.Context, username, password, client string) (string, error) {
	if wait := s.limiter.wait(client); wait > 0 {
		return "", &RateLimitError{RetryAfter: wait}
	}
	if len(password) > MaxPasswordLength {
		s.limiter.fail(client)
		return "", ErrInvalidCredentials
	}
	hash, found := s.decoy, false
	user, err := s.store.UserByName(ctx, username)
	switch {
	case err == nil:
		hash, found = user.PasswordHash, true
	case !errors.Is(err, store.ErrNotFound):
		return "", fmt.Errorf("login: %w", err)
	}
	ok, err := s.verify(hash, password)
	if err != nil {
		if !found {
			// The decoy is damaged only if the parameters are; the caller sees a plain failure.
			s.limiter.fail(client)
			return "", ErrInvalidCredentials
		}
		return "", fmt.Errorf("login as %s: %w", user.Username, err)
	}
	if !found || !ok {
		s.limiter.fail(client)
		return "", ErrInvalidCredentials
	}
	s.limiter.clear(client)

	token, err := newToken()
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	now := s.now()
	err = s.store.AddSession(ctx, store.Session{
		TokenHash: hashToken(token), UserID: user.ID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(SessionLifetime),
	})
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	return token, nil
}

// newToken returns a random session token, 32 bytes in unpadded URL-safe base64.
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("new session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Authenticate returns the user a session token belongs to, and counts the use towards the idle timeout. It returns
// ErrNoSession for a token that is not a valid session.
func (s *Service) Authenticate(ctx context.Context, token string) (store.User, error) {
	if token == "" {
		return store.User{}, ErrNoSession
	}
	hash := hashToken(token)
	sess, err := s.store.Session(ctx, hash)
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, ErrNoSession
	}
	if err != nil {
		return store.User{}, fmt.Errorf("authenticate: %w", err)
	}
	now := s.now()
	if !now.Before(sess.ExpiresAt) || now.Sub(sess.LastSeenAt) >= IdleTimeout {
		return store.User{}, ErrNoSession
	}
	user, err := s.store.UserByID(ctx, sess.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, ErrNoSession
	}
	if err != nil {
		return store.User{}, fmt.Errorf("authenticate: %w", err)
	}
	if now.Sub(sess.LastSeenAt) >= touchEvery {
		err := s.store.TouchSession(ctx, hash, now)
		if errors.Is(err, store.ErrNotFound) { // ended between the read and the write
			return store.User{}, ErrNoSession
		}
		if err != nil {
			return store.User{}, fmt.Errorf("authenticate: %w", err)
		}
	}
	user.PasswordHash = ""
	return user, nil
}

// Logout ends a session. A token that is not a session is not an error: leaving twice is not a failure.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	err := s.store.DeleteSession(ctx, hashToken(token))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// EnsureAdmin creates the account AdminUsername with a random password when there is no user at all, and returns that
// password, which exists nowhere else, so the caller must show it once. When there is a user it creates nothing and
// returns created false.
func (s *Service) EnsureAdmin(ctx context.Context) (password string, created bool, err error) {
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return "", false, fmt.Errorf("ensure admin: %w", err)
	}
	if n > 0 {
		return "", false, nil
	}
	password, hash, err := s.newPassword()
	if err != nil {
		return "", false, fmt.Errorf("ensure admin: %w", err)
	}
	if _, err := s.store.SaveUser(ctx, store.User{Username: AdminUsername, PasswordHash: hash, CreatedAt: s.now()}); err != nil {
		return "", false, fmt.Errorf("ensure admin: %w", err)
	}
	return password, true, nil
}

// ResetPassword gives a user a new random password, returns it, and ends every session of that user. An unknown user is
// ErrUnknownUser.
func (s *Service) ResetPassword(ctx context.Context, username string) (string, error) {
	user, err := s.store.UserByName(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("%w: %s", ErrUnknownUser, username)
	}
	if err != nil {
		return "", fmt.Errorf("reset password: %w", err)
	}
	password, hash, err := s.newPassword()
	if err != nil {
		return "", fmt.Errorf("reset password: %w", err)
	}
	if _, err := s.store.SaveUser(ctx, store.User{Username: user.Username, PasswordHash: hash, CreatedAt: user.CreatedAt}); err != nil {
		return "", fmt.Errorf("reset password: %w", err)
	}
	if err := s.store.DeleteUserSessions(ctx, user.ID); err != nil {
		return "", fmt.Errorf("reset password: the password changed but its sessions could not be ended: %w", err)
	}
	return password, nil
}

// Prune removes the sessions that have expired or been idle for too long, and returns how many.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	now := s.now()
	n, err := s.store.DeleteSessionsBefore(ctx, now, now.Add(-IdleTimeout))
	if err != nil {
		return 0, fmt.Errorf("prune sessions: %w", err)
	}
	return n, nil
}

// newPassword makes a random password and its hash.
func (s *Service) newPassword() (password, hash string, err error) {
	b := make([]byte, generatedPasswordBytes)
	if _, err := io.ReadFull(s.rand, b); err != nil {
		return "", "", fmt.Errorf("generate a password: %w", err)
	}
	password = base64.RawURLEncoding.EncodeToString(b)
	hash, err = HashPassword(password, s.params)
	if err != nil {
		return "", "", err
	}
	return password, hash, nil
}
