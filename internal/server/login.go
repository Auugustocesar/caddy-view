package server

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/auth"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// Authenticator is what the API needs from the login service. When Options.Auth is set, every route of the API except
// login and logout needs a session.
type Authenticator interface {
	// Login returns the token of a new session. client names who asks, for the count of failures.
	Login(ctx context.Context, username, password, client string) (string, error)
	// Authenticate returns the user of a session token, or an error wrapping auth.ErrNoSession.
	Authenticate(ctx context.Context, token string) (store.User, error)
	// Logout ends a session.
	Logout(ctx context.Context, token string) error
}

// sessionCookie is the name of the cookie that holds the session token.
const sessionCookie = "caddy_view_session"

type userKey struct{}

// requireSession lets a request through only with a valid session, and puts its user in the context. It reports
// whether the request may go on; when not, the answer is written. An outage of the login service is a 500, never a
// 401, so that it is not mistaken for being logged out.
func (a *api) requireSession(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	var token string
	if c, err := r.Cookie(sessionCookie); err == nil {
		token = c.Value
	}
	user, err := a.opts.Auth.Authenticate(r.Context(), token)
	switch {
	case err == nil:
		return r.WithContext(context.WithValue(r.Context(), userKey{}, user)), true
	case errors.Is(err, auth.ErrNoSession):
		writeError(w, http.StatusUnauthorized, "login required")
	default:
		a.log.Error("could not check the session", "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, "could not check the session")
	}
	return r, false
}

// opensWithoutSession reports whether a path of the API is answered without a session: the way in, the way out, and the
// health probe, which a container runtime calls without credentials and which says nothing but "ok".
func opensWithoutSession(p string) bool {
	return p == "/api/login" || p == "/api/logout" || p == "/api/health"
}

type userJSON struct {
	Username string `json:"username"`
}

func (a *api) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readBody(w, r, &body, false) {
		return
	}
	token, err := a.opts.Auth.Login(r.Context(), body.Username, body.Password, clientAddress(r))
	var limited *auth.RateLimitError
	switch {
	case errors.As(err, &limited):
		a.log.Warn("login held back", "client", clientAddress(r), "retry_after", limited.RetryAfter)
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(limited.RetryAfter.Seconds()))))
		writeError(w, http.StatusTooManyRequests, limited.Error())
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		a.log.Warn("login refused", "client", clientAddress(r))
		writeError(w, http.StatusUnauthorized, auth.ErrInvalidCredentials.Error())
		return
	case err != nil:
		a.log.Error("login failed", "error", err)
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}
	// The name as stored, which may differ in case from the one typed.
	user, err := a.opts.Auth.Authenticate(r.Context(), token)
	if err != nil {
		a.log.Error("login failed: the new session was not found", "error", err)
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}
	http.SetCookie(w, a.sessionCookie(r, token, int(auth.SessionLifetime/time.Second)))
	writeJSON(w, http.StatusOK, map[string]userJSON{"user": {Username: user.Username}})
}

func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := a.opts.Auth.Logout(r.Context(), c.Value); err != nil {
			a.log.Error("logout failed", "error", err)
			writeError(w, http.StatusInternalServerError, "logout failed")
			return
		}
	}
	http.SetCookie(w, a.sessionCookie(r, "", -1))
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) me(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userKey{}).(store.User)
	writeJSON(w, http.StatusOK, map[string]userJSON{"user": {Username: user.Username}})
}

// sessionCookie builds the cookie of the session: out of reach of scripts, never sent cross-site, and only over TLS
// when the request came over TLS. A negative maxAge expires it.
func (a *api) sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || a.opts.SecureCookie,
	}
}

// clientAddress is the address of the connection, without its port. A header such as X-Forwarded-For is not used: the
// caller writes it, so counting failures by it would let a guesser pick a new identity for each try.
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
