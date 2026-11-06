package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/auth"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

const (
	testToken  = "tok-1234567890"
	cookieName = "caddy_view_session"
)

// fakeAuth is the login service the handler talks to: one valid token, and what it was asked.
type fakeAuth struct {
	mu        sync.Mutex
	token     string // the one valid token; empty means none
	loginErr  error
	authErr   error // returned by Authenticate instead of the lookup, when set
	loggedOut []string
	gotUser   string
	gotPass   string
	gotClient string
	logins    int
}

func (f *fakeAuth) Login(_ context.Context, username, password, client string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins++
	f.gotUser, f.gotPass, f.gotClient = username, password, client
	if f.loginErr != nil {
		return "", f.loginErr
	}
	f.token = testToken
	return testToken, nil
}

func (f *fakeAuth) Authenticate(_ context.Context, token string) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.authErr != nil {
		return store.User{}, f.authErr
	}
	if token == "" || token != f.token {
		return store.User{}, auth.ErrNoSession
	}
	return store.User{ID: 1, Username: "admin"}, nil
}

func (f *fakeAuth) Logout(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loggedOut = append(f.loggedOut, token)
	if token == f.token {
		f.token = ""
	}
	return nil
}

type loginRig struct {
	h     http.Handler
	auth  *fakeAuth
	caddy *fakeCaddy
	log   *bytes.Buffer
}

func newLoginRig(t *testing.T) *loginRig {
	t.Helper()
	f := &fakeAuth{token: testToken}
	c := &fakeCaddy{cfg: parse(t, twoSitesConfig)}
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := New(c, Options{Endpoint: testEndpoint, Auth: f, Logger: logger, UI: uiStub})
	return &loginRig{h: h, auth: f, caddy: c, log: logs}
}

type loginReq struct {
	method, path, body string
	cookie             string
	headers            map[string]string
	host               string
	tls                bool
}

func (r *loginRig) do(t *testing.T, q loginReq) *httptest.ResponseRecorder {
	t.Helper()
	host := q.host
	if host == "" {
		host = "localhost:8484"
	}
	scheme := "http"
	if q.tls {
		scheme = "https"
	}
	hr := httptest.NewRequest(q.method, scheme+"://"+host+q.path, strings.NewReader(q.body))
	if q.tls {
		hr.TLS = &tls.ConnectionState{}
	}
	if q.method != http.MethodGet {
		hr.Header.Set("Content-Type", "application/json")
	}
	if q.cookie != "" {
		hr.AddCookie(&http.Cookie{Name: cookieName, Value: q.cookie})
	}
	for k, v := range q.headers {
		hr.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, hr)
	return rec
}

func cookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	return nil
}

const goodLogin = `{"username":"admin","password":"s3cret-pw"}`

func TestWithoutAnAuthenticatorThereIsNoLogin(t *testing.T) {
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint})
	for _, path := range []string{"/api/login", "/api/logout", "/api/me"} {
		if rec := do(t, h, http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 when login is off", path, rec.Code)
		}
	}
	if rec := do(t, h, http.MethodGet, "/api/status"); rec.Code != http.StatusOK {
		t.Errorf("GET /api/status = %d, want 200 when login is off", rec.Code)
	}
}

func TestEveryAPIRouteNeedsASessionExceptLogin(t *testing.T) {
	r := newLoginRig(t)
	for _, q := range []loginReq{
		{method: http.MethodGet, path: "/api/status"},
		{method: http.MethodGet, path: "/api/config"},
		{method: http.MethodGet, path: "/api/sites"},
		{method: http.MethodGet, path: "/api/system"},
		{method: http.MethodGet, path: "/api/logs/stream"},
		{method: http.MethodGet, path: "/api/me"},
		{method: http.MethodPost, path: "/api/caddyfile/apply", body: `{}`},
		{method: http.MethodGet, path: "/api/nothing-here"}, // an unknown path must not say it is unknown
		{method: http.MethodGet, path: "/api/status", cookie: "not-the-token"},
	} {
		rec := r.do(t, q)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s (cookie %q) = %d, want 401", q.method, q.path, q.cookie, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "login required") {
			t.Errorf("%s %s: body = %s, want the login required error", q.method, q.path, rec.Body.String())
		}
	}
	if n := r.caddy.calls.Load(); n != 0 {
		t.Errorf("Caddy was asked %d times on behalf of requests with no session, want 0", n)
	}
}

func TestTheUIFilesNeedNoSessionSoTheLoginScreenCanLoad(t *testing.T) {
	r := newLoginRig(t)
	if rec := r.do(t, loginReq{method: http.MethodGet, path: "/"}); rec.Code != http.StatusOK {
		t.Errorf("GET / = %d, want 200 without a session", rec.Code)
	}
}

func TestAValidSessionCookieOpensTheAPI(t *testing.T) {
	r := newLoginRig(t)
	if rec := r.do(t, loginReq{method: http.MethodGet, path: "/api/status", cookie: testToken}); rec.Code != http.StatusOK {
		t.Errorf("GET /api/status with the session = %d, want 200", rec.Code)
	}
}

func TestAuthenticationFailureOfTheServiceIsNotALogout(t *testing.T) {
	r := newLoginRig(t)
	r.auth.authErr = errors.New("database is locked")
	rec := r.do(t, loginReq{method: http.MethodGet, path: "/api/status", cookie: testToken})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500: an outage must not look like being logged out", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "database is locked") {
		t.Errorf("body %s leaks the cause", rec.Body.String())
	}
	if !strings.Contains(r.log.String(), "database is locked") {
		t.Error("the cause is not in the log")
	}
}

func TestLoginSetsAHardenedCookieAndNeverReturnsTheToken(t *testing.T) {
	r := newLoginRig(t)
	rec := r.do(t, loginReq{method: http.MethodPost, path: "/api/login", body: goodLogin})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	decode(t, rec, &got)
	if got.User.Username != "admin" {
		t.Errorf("user = %q, want admin", got.User.Username)
	}
	if strings.Contains(rec.Body.String(), testToken) {
		t.Error("the token is in the body: it belongs in the HttpOnly cookie only")
	}
	c := cookieOf(t, rec)
	if c == nil {
		t.Fatal("no session cookie")
	}
	if c.Value != testToken || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" ||
		c.MaxAge != int(auth.SessionLifetime/time.Second) || c.Secure {
		t.Errorf("cookie = %+v, want the token, HttpOnly, SameSite=Strict, Path=/, the session lifetime, and no Secure over plain HTTP", c)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if r.auth.gotUser != "admin" || r.auth.gotPass != "s3cret-pw" {
		t.Errorf("Login got %q and %q", r.auth.gotUser, r.auth.gotPass)
	}
}

func TestTheLoginCookieIsSecureOverTLS(t *testing.T) {
	r := newLoginRig(t)
	rec := r.do(t, loginReq{method: http.MethodPost, path: "/api/login", body: goodLogin, tls: true,
		headers: map[string]string{"Origin": "https://localhost:8484"}})
	if c := cookieOf(t, rec); c == nil || !c.Secure {
		t.Errorf("cookie = %+v, want Secure over TLS", c)
	}
}

func TestTheCookieOpensTheAPIAfterLogin(t *testing.T) {
	r := newLoginRig(t)
	r.auth.token = ""
	login := r.do(t, loginReq{method: http.MethodPost, path: "/api/login", body: goodLogin})
	c := cookieOf(t, login)
	if c == nil {
		t.Fatal("no cookie")
	}
	if rec := r.do(t, loginReq{method: http.MethodGet, path: "/api/status", cookie: c.Value}); rec.Code != http.StatusOK {
		t.Errorf("GET /api/status with the cookie from login = %d, want 200", rec.Code)
	}
}

func TestLoginCountsFailuresPerConnectionNotPerForwardedHeader(t *testing.T) {
	r := newLoginRig(t)
	r.do(t, loginReq{method: http.MethodPost, path: "/api/login", body: goodLogin,
		headers: map[string]string{"X-Forwarded-For": "6.6.6.6"}})
	// httptest gives requests the address 192.0.2.1:1234.
	if r.auth.gotClient != "192.0.2.1" {
		t.Errorf("client = %q, want the address of the connection without its port: a header the caller writes is not an identity", r.auth.gotClient)
	}
}

func TestLoginRefusesWithoutSayingWhy(t *testing.T) {
	r := newLoginRig(t)
	r.auth.loginErr = auth.ErrInvalidCredentials
	rec := r.do(t, loginReq{method: http.MethodPost, path: "/api/login", body: goodLogin})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid username or password") {
		t.Errorf("body = %s", rec.Body.String())
	}
	if c := cookieOf(t, rec); c != nil {
		t.Errorf("a refused login set a cookie: %+v", c)
	}
	if strings.Contains(r.log.String(), "s3cret-pw") {
		t.Error("the password is in the log")
	}
}

func TestLoginHeldBackAnswers429WithRetryAfter(t *testing.T) {
	r := newLoginRig(t)
	r.auth.loginErr = &auth.RateLimitError{RetryAfter: 90*time.Second + 200*time.Millisecond}
	rec := r.do(t, loginReq{method: http.MethodPost, path: "/api/login", body: goodLogin})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "91" {
		t.Errorf("Retry-After = %q, want 91: whole seconds, rounded up", got)
	}
	if c := cookieOf(t, rec); c != nil {
		t.Errorf("a held back login set a cookie: %+v", c)
	}
}

func TestLoginServiceFailureIsA500WithoutTheCause(t *testing.T) {
	r := newLoginRig(t)
	r.auth.loginErr = errors.New("disk is gone")
	rec := r.do(t, loginReq{method: http.MethodPost, path: "/api/login", body: goodLogin})
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "disk is gone") {
		t.Errorf("status %d, body %s, want a 500 that keeps the cause to the log", rec.Code, rec.Body.String())
	}
	if !strings.Contains(r.log.String(), "disk is gone") {
		t.Error("the cause is not in the log")
	}
}

func TestLoginReadsOnlyAWellFormedJSONPost(t *testing.T) {
	r := newLoginRig(t)
	cases := map[string]struct {
		q    loginReq
		want int
	}{
		"get":            {loginReq{method: http.MethodGet, path: "/api/login"}, http.StatusMethodNotAllowed},
		"form":           {loginReq{method: http.MethodPost, path: "/api/login", body: "username=a&password=b", headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}}, http.StatusUnsupportedMediaType},
		"foreign origin": {loginReq{method: http.MethodPost, path: "/api/login", body: goodLogin, headers: map[string]string{"Origin": "http://evil.example"}}, http.StatusForbidden},
		"malformed":      {loginReq{method: http.MethodPost, path: "/api/login", body: `{"username":`}, http.StatusBadRequest},
		"unknown field":  {loginReq{method: http.MethodPost, path: "/api/login", body: `{"username":"a","password":"b","admin":true}`}, http.StatusBadRequest},
		"empty body":     {loginReq{method: http.MethodPost, path: "/api/login", body: ``}, http.StatusBadRequest},
		"foreign host":   {loginReq{method: http.MethodPost, path: "/api/login", body: goodLogin, host: "evil.example:8484"}, http.StatusForbidden},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			before := r.auth.logins
			if rec := r.do(t, c.q); rec.Code != c.want {
				t.Errorf("status = %d, want %d", rec.Code, c.want)
			}
			if r.auth.logins != before {
				t.Error("the service was asked to log in a request that was refused first")
			}
		})
	}
}

func TestMeSaysWhoIsLoggedIn(t *testing.T) {
	r := newLoginRig(t)
	rec := r.do(t, loginReq{method: http.MethodGet, path: "/api/me", cookie: testToken})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"admin"`) {
		t.Errorf("GET /api/me = %d %s, want 200 and the username", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "assword") {
		t.Errorf("body %s mentions a password", rec.Body.String())
	}
}

func TestLogoutEndsTheSessionAndClearsTheCookie(t *testing.T) {
	r := newLoginRig(t)
	rec := r.do(t, loginReq{method: http.MethodPost, path: "/api/logout", body: `{}`, cookie: testToken})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if len(r.auth.loggedOut) != 1 || r.auth.loggedOut[0] != testToken {
		t.Errorf("Logout was called with %v, want the token of the cookie", r.auth.loggedOut)
	}
	c := cookieOf(t, rec)
	if c == nil || c.Value != "" || c.MaxAge >= 0 || !c.HttpOnly || c.Path != "/" {
		t.Errorf("cookie = %+v, want an expired, empty cookie for the same path", c)
	}
	if after := r.do(t, loginReq{method: http.MethodGet, path: "/api/status", cookie: testToken}); after.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/status after logout = %d, want 401", after.Code)
	}
}

func TestLogoutWithoutASessionStillClearsTheCookie(t *testing.T) {
	r := newLoginRig(t)
	rec := r.do(t, loginReq{method: http.MethodPost, path: "/api/logout", body: `{}`})
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204: leaving twice is not a failure", rec.Code)
	}
	if len(r.auth.loggedOut) != 0 {
		t.Errorf("Logout was called with %v for a request with no cookie", r.auth.loggedOut)
	}
}

func TestLogoutIsProtectedFromCrossSiteRequests(t *testing.T) {
	r := newLoginRig(t)
	rec := r.do(t, loginReq{method: http.MethodPost, path: "/api/logout", body: `{}`, cookie: testToken,
		headers: map[string]string{"Origin": "http://evil.example"}})
	if rec.Code != http.StatusForbidden || len(r.auth.loggedOut) != 0 {
		t.Errorf("status %d, logged out %v, want a refused request and a session left alone", rec.Code, r.auth.loggedOut)
	}
}

func TestCookiesOfOtherNamesAreNotASession(t *testing.T) {
	r := newLoginRig(t)
	hr := httptest.NewRequest(http.MethodGet, "http://localhost:8484/api/status", nil)
	hr.AddCookie(&http.Cookie{Name: "session", Value: testToken})
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, hr)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// textLogger returns a logger that writes everything to w.
func textLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestSecureCookieOptionMarksTheCookieEvenOverPlainHTTP(t *testing.T) {
	// Behind a proxy that ends TLS, the request reaches caddy-view as plain HTTP, but the browser is on HTTPS.
	f := &fakeAuth{}
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint, Auth: f, SecureCookie: true})
	r := &loginRig{h: h, auth: f}
	rec := r.do(t, loginReq{method: http.MethodPost, path: "/api/login", body: goodLogin})
	if c := cookieOf(t, rec); c == nil || !c.Secure {
		t.Errorf("cookie = %+v, want Secure", c)
	}
	out := r.do(t, loginReq{method: http.MethodPost, path: "/api/logout", body: `{}`, cookie: testToken})
	if c := cookieOf(t, out); c == nil || !c.Secure {
		t.Errorf("the cookie that clears the session = %+v, want Secure too, or a browser may keep the old one", c)
	}
}
