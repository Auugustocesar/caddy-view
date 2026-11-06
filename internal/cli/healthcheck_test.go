package cli

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestHealthcheckPassesAgainstARunningServeThatAsksForALogin(t *testing.T) {
	admin := fakeAdmin(t, goodConfig)
	base, stop, _, _ := startServeWithLogin(t, "--admin", admin.URL)
	defer stop()
	u, _ := url.Parse(base)
	code, stdout, stderr := run(t, "healthcheck", "--address", u.Host)
	if code != 0 {
		t.Errorf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

func TestHealthcheckFailsWhenNothingListens(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	code, _, stderr := run(t, "healthcheck", "--address", addr, "--timeout", "1s")
	if code != 1 || stderr == "" {
		t.Errorf("exit %d, stderr %q, want exit 1 and a message", code, stderr)
	}
}

func TestHealthcheckFailsOnAnythingButAnOKStatus(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"unavailable": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"status":"unavailable"}`, http.StatusServiceUnavailable)
		},
		"a login page": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("<html>hi</html>"))
		},
		"wrong status": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"nope"}`))
		},
		"unauthorized": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusUnauthorized) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			if code, _, stderr := run(t, "healthcheck", "--address", u.Host); code != 1 || stderr == "" {
				t.Errorf("exit %d, stderr %q, want exit 1 and a message", code, stderr)
			}
		})
	}
}

func TestHealthcheckPassesOnTheOKStatus(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	if code, _, stderr := run(t, "healthcheck", "--address", u.Host); code != 0 {
		t.Fatalf("exit %d, stderr %q, want 0", code, stderr)
	}
	if path != "/api/health" {
		t.Errorf("asked %q, want /api/health", path)
	}
}

func TestHealthcheckFlagErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown flag":    {"healthcheck", "--nope"},
		"no port":         {"healthcheck", "--address", "127.0.0.1"},
		"bad timeout":     {"healthcheck", "--timeout", "soon"},
		"timeout of zero": {"healthcheck", "--timeout", "0s"},
	} {
		t.Run(name, func(t *testing.T) {
			if code, _, stderr := run(t, args...); code != 2 {
				t.Errorf("exit %d, stderr %q, want 2", code, stderr)
			}
		})
	}
}

func TestUsageListsHealthcheck(t *testing.T) {
	if _, stdout, _ := run(t, "help"); !strings.Contains(stdout, "healthcheck") {
		t.Errorf("usage %q does not list healthcheck", stdout)
	}
}
