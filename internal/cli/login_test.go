package cli

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/auth"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

var (
	createdLine  = regexp.MustCompile(`created the user "admin" with the password: (\S+)`)
	resetLine    = regexp.MustCompile(`new password for (\S+): (\S+)`)
	cheapParams  = auth.Params{Time: 1, Memory: 8, Threads: 1}
	serveTimeout = 10 * time.Second
)

// client returns an HTTP client with a cookie jar, so a login carries over to the next request.
func client(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: serveTimeout}
}

func login(t *testing.T, c *http.Client, base, password string) int {
	t.Helper()
	body := `{"username":"admin","password":"` + password + `"}`
	req, _ := http.NewRequest(http.MethodPost, base+"/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func status(t *testing.T, c *http.Client, base string) int {
	t.Helper()
	resp, err := c.Get(base + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestServeAsksForALoginByDefaultAndPrintsTheFirstPassword(t *testing.T) {
	admin := fakeAdmin(t, goodConfig)
	base, stop, _, stderr := startServeWithLogin(t, "--admin", admin.URL)
	defer stop()

	m := createdLine.FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("stderr does not show the generated password once:\n%s", stderr)
	}
	c := client(t)
	if got := status(t, c, base); got != http.StatusUnauthorized {
		t.Errorf("GET /api/status without a login = %d, want 401", got)
	}
	if got := login(t, c, base, "wrong"); got != http.StatusUnauthorized {
		t.Errorf("login with a wrong password = %d, want 401", got)
	}
	if got := login(t, c, base, m[1]); got != http.StatusOK {
		t.Fatalf("login with the printed password = %d, want 200", got)
	}
	if got := status(t, c, base); got != http.StatusOK {
		t.Errorf("GET /api/status after the login = %d, want 200", got)
	}
}

func TestServeShowsThePasswordOnlyTheFirstTime(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	admin := fakeAdmin(t, goodConfig)

	_, stop, _, stderr := startServeWithLogin(t, "--admin", admin.URL, "--data-dir", dir)
	m := createdLine.FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("no password on the first start:\n%s", stderr)
	}
	stop()

	base, stop, _, stderr := startServeWithLogin(t, "--admin", admin.URL, "--data-dir", dir)
	defer stop()
	if strings.Contains(stderr.String(), "created the user") || strings.Contains(stderr.String(), m[1]) {
		t.Errorf("the second start shows a password again:\n%s", stderr)
	}
	if got := login(t, client(t), base, m[1]); got != http.StatusOK {
		t.Errorf("the first password after a restart = %d, want 200", got)
	}
}

func TestServeWithNoAuthOpensTheAPIAndSaysSo(t *testing.T) {
	admin := fakeAdmin(t, goodConfig)
	base, stop, _, stderr := startServe(t, "--admin", admin.URL)
	defer stop()
	if got := status(t, client(t), base); got != http.StatusOK {
		t.Errorf("GET /api/status with --no-auth = %d, want 200", got)
	}
	if !strings.Contains(stderr.String(), "--no-auth") {
		t.Errorf("stderr does not warn that the login is off:\n%s", stderr)
	}
	if strings.Contains(stderr.String(), "created the user") {
		t.Error("a user was created although the login is off")
	}
}

func TestServeStartsWithoutAnAdminWhenTheStoreHasAUser(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	seedUser(t, dir, "someone", "pw")
	admin := fakeAdmin(t, goodConfig)
	_, stop, _, stderr := startServeWithLogin(t, "--admin", admin.URL, "--data-dir", dir)
	defer stop()
	if strings.Contains(stderr.String(), "created the user") {
		t.Errorf("an admin was created next to an existing user:\n%s", stderr)
	}
}

// seedUser stores a user with a password, in the database of dir, and returns the user.
func seedUser(t *testing.T, dir, name, password string) store.User {
	t.Helper()
	s, err := store.OpenSQLite(context.Background(), filepath.Join(dir, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	hash, err := auth.HashPassword(password, cheapParams)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.SaveUser(context.Background(), store.User{Username: name, PasswordHash: hash, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestServePrunesEndedSessionsAtStart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	u := seedUser(t, dir, "admin", "pw")
	seed, err := store.OpenSQLite(context.Background(), filepath.Join(dir, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	long := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now()
	for hash, sess := range map[string]store.Session{
		"old":  {TokenHash: "old", UserID: u.ID, CreatedAt: long, LastSeenAt: long, ExpiresAt: long.Add(time.Hour)},
		"live": {TokenHash: "live", UserID: u.ID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)},
	} {
		if err := seed.AddSession(context.Background(), sess); err != nil {
			t.Fatalf("seed %s: %v", hash, err)
		}
	}
	seed.Close()

	admin := fakeAdmin(t, goodConfig)
	_, stop, _, _ := startServeWithLogin(t, "--admin", admin.URL, "--data-dir", dir)
	stop()

	after, err := store.OpenSQLite(context.Background(), filepath.Join(dir, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	if _, err := after.Session(context.Background(), "old"); err == nil {
		t.Error("the ended session is still stored after start")
	}
	if _, err := after.Session(context.Background(), "live"); err != nil {
		t.Errorf("the live session was removed: %v", err)
	}
}

func TestListenFlagsDecideWhereLoginIsRequired(t *testing.T) {
	cases := []struct {
		name          string
		listen        string
		noAuth, plain bool
		wantErr       string // a piece of the message, empty for none
	}{
		{"loopback, default", "127.0.0.1:8484", false, false, ""},
		{"loopback, no auth", "127.0.0.1:8484", true, false, ""},
		{"localhost, no auth", "localhost:8484", true, false, ""},
		{"ipv6 loopback", "[::1]:8484", false, false, ""},
		{"lan, plain HTTP not allowed", "192.168.1.10:8484", false, false, "--allow-insecure-http"},
		{"all interfaces, plain HTTP not allowed", "0.0.0.0:8484", false, false, "--allow-insecure-http"},
		{"lan, plain HTTP allowed", "192.168.1.10:8484", false, true, ""},
		{"lan, no auth", "192.168.1.10:8484", true, true, "--no-auth"},
		{"all interfaces, no auth", ":8484", true, true, "--no-auth"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkListenFlags(c.listen, c.noAuth, c.plain)
			switch {
			case c.wantErr == "" && err != nil:
				t.Errorf("checkListenFlags = %v, want nil", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Errorf("checkListenFlags = %v, want an error naming %s", err, c.wantErr)
			}
		})
	}
}

func TestServeRefusesWhatListenFlagsForbid(t *testing.T) {
	for name, args := range map[string][]string{
		"plain HTTP beyond loopback": {"serve", "--listen", "0.0.0.0:0"},
		"no auth beyond loopback":    {"serve", "--listen", "0.0.0.0:0", "--no-auth", "--allow-insecure-http"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := run(t, args...)
			if code != 2 || strings.Contains(stdout, "listening") || stderr == "" {
				t.Errorf("exit %d, stdout %q, stderr %q, want exit 2, nothing listening, and a message", code, stdout, stderr)
			}
		})
	}
}

func TestAllowedHostsAreCheckedAndKeepTheListenHost(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		got, err := allowedHosts("192.168.1.10:8484", []string{"View.Example.com", "view.example.com", "10.0.0.5"})
		want := []string{"view.example.com", "10.0.0.5", "192.168.1.10"}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("allowedHosts = %v, %v, want %v: lower case, without repeats, and the host that was bound", got, err, want)
		}
	})
	t.Run("wildcard address names no host", func(t *testing.T) {
		got, err := allowedHosts("0.0.0.0:8484", nil)
		if err != nil || len(got) != 0 {
			t.Errorf("allowedHosts = %v, %v, want none", got, err)
		}
	})
	t.Run("loopback needs no entry", func(t *testing.T) {
		if got, err := allowedHosts("127.0.0.1:8484", nil); err != nil || len(got) != 0 {
			t.Errorf("allowedHosts = %v, %v, want none", got, err)
		}
	})
	for _, bad := range []string{"", " ", "http://view.example.com", "view.example.com/", "view.example.com:8443", "*.example.com", "a b", "[::1]"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			if _, err := allowedHosts("127.0.0.1:8484", []string{bad}); err == nil || !strings.Contains(err.Error(), "--allowed-host") {
				t.Errorf("allowedHosts(%q) = %v, want an error naming --allowed-host", bad, err)
			}
		})
	}
}

func TestServeBeyondLoopbackNeedsTheHostToBeAllowed(t *testing.T) {
	admin := fakeAdmin(t, goodConfig)
	base, stop, _, stderr := launchServe(t, []string{"--admin", admin.URL, "--listen", "0.0.0.0:0",
		"--allow-insecure-http", "--allowed-host", "view.example.com"})
	defer stop()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	get := func(host string) int {
		req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+u.Port()+"/api/status", nil)
		req.Host = host
		resp, err := (&http.Client{Timeout: serveTimeout}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := get("view.example.com"); got != http.StatusUnauthorized {
		t.Errorf("the allowed host = %d, want 401 (reached, and asking for a login)", got)
	}
	if got := get("evil.example.com"); got != http.StatusForbidden {
		t.Errorf("another host = %d, want 403", got)
	}
	if !strings.Contains(stderr.String(), "plain HTTP") {
		t.Errorf("stderr does not warn about plain HTTP:\n%s", stderr)
	}
}

func TestResetPasswordSetsANewOneAndEndsTheSessions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	u := seedUser(t, dir, "admin", "old")
	seed, err := store.OpenSQLite(context.Background(), filepath.Join(dir, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := seed.AddSession(context.Background(), store.Session{TokenHash: "t", UserID: u.ID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	seed.Close()

	code, stdout, stderr := run(t, "reset-password", "--data-dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d\nstderr: %s", code, stderr)
	}
	m := resetLine.FindStringSubmatch(stdout)
	if m == nil || m[1] != "admin" {
		t.Fatalf("stdout does not show the new password:\n%s", stdout)
	}

	check, err := store.OpenSQLite(context.Background(), filepath.Join(dir, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	got, err := check.UserByName(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := auth.VerifyPassword(got.PasswordHash, m[2]); err != nil || !ok {
		t.Errorf("the printed password does not match the stored hash: %v, %v", ok, err)
	}
	if ok, _ := auth.VerifyPassword(got.PasswordHash, "old"); ok {
		t.Error("the old password still matches")
	}
	if _, err := check.Session(context.Background(), "t"); err == nil {
		t.Error("a session survived the reset")
	}
}

func TestResetPasswordFailures(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	seedUser(t, dir, "admin", "pw")
	t.Run("unknown user", func(t *testing.T) {
		code, stdout, stderr := run(t, "reset-password", "--data-dir", dir, "--username", "ghost")
		if code != 1 || !strings.Contains(stderr, "ghost") || resetLine.MatchString(stdout) {
			t.Errorf("exit %d, stdout %q, stderr %q, want exit 1 naming the user and no password", code, stdout, stderr)
		}
	})
	t.Run("unknown flag", func(t *testing.T) {
		if code, _, _ := run(t, "reset-password", "--nope"); code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
	})
	t.Run("no database yet says to start serve", func(t *testing.T) {
		code, _, stderr := run(t, "reset-password", "--data-dir", filepath.Join(t.TempDir(), "empty"))
		if code != 1 || !strings.Contains(stderr, "serve") {
			t.Errorf("exit %d, stderr %q, want exit 1 and a pointer to serve", code, stderr)
		}
	})
}

func TestUsageListsResetPassword(t *testing.T) {
	_, stdout, _ := run(t, "help")
	if !strings.Contains(stdout, "reset-password") {
		t.Errorf("usage %q does not list reset-password", stdout)
	}
}
