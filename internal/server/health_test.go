package server

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestHealthAnswersOKWithoutALogin(t *testing.T) {
	r := newLoginRig(t)
	rec := r.do(t, loginReq{method: http.MethodGet, path: "/api/health"})
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Errorf("GET /api/health = %d %s, want 200 and only the status", rec.Code, rec.Body.String())
	}
	if n := r.caddy.calls.Load(); n != 0 {
		t.Errorf("health asked Caddy %d times: it says whether caddy-view is alive, not whether Caddy is", n)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

func TestHealthWorksWithoutLoginToo(t *testing.T) {
	if rec := do(t, newHandler(&fakeCaddy{}), http.MethodGet, "/api/health"); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestHealthIs503WhileTheDatabaseCannotBeRead(t *testing.T) {
	logs := &strings.Builder{}
	h := New(&fakeCaddy{}, Options{Endpoint: testEndpoint, Logger: textLogger(logs), Database: fakeDatabase{err: errors.New("disk is gone")}})
	rec := do(t, h, http.MethodGet, "/api/health")
	if rec.Code != http.StatusServiceUnavailable || strings.TrimSpace(rec.Body.String()) != `{"status":"unavailable"}` {
		t.Errorf("GET /api/health = %d %s, want 503 and only the status", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "disk is gone") {
		t.Error("the cause is not in the log")
	}
}

func TestHealthIsOKWhenTheDatabaseAnswers(t *testing.T) {
	h := New(&fakeCaddy{}, Options{Endpoint: testEndpoint, Database: fakeDatabase{info: DatabaseInfo{Kind: "sqlite"}}})
	if rec := do(t, h, http.MethodGet, "/api/health"); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestHealthOnlyAnswersGetAndLoopbackHosts(t *testing.T) {
	r := newLoginRig(t)
	if rec := r.do(t, loginReq{method: http.MethodPost, path: "/api/health", body: `{}`}); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", rec.Code)
	}
	if rec := r.do(t, loginReq{method: http.MethodGet, path: "/api/health", host: "evil.example"}); rec.Code != http.StatusForbidden {
		t.Errorf("foreign host = %d, want 403", rec.Code)
	}
}
