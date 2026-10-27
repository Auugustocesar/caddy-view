package server

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newLogged returns a handler that logs at the given level into the returned buffer.
func newLogged(f *fakeCaddy, level slog.Level) (http.Handler, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level}))
	return New(f, Options{Endpoint: testEndpoint, Logger: logger}), &buf
}

func TestLogsCaddyFailures(t *testing.T) {
	for _, path := range []string{"/api/config", "/api/sites"} {
		t.Run(path, func(t *testing.T) {
			h, logs := newLogged(&fakeCaddy{cfgErr: errors.New("boom")}, slog.LevelInfo)
			rec := do(t, h, http.MethodGet, path)
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", rec.Code)
			}
			for _, want := range []string{"level=WARN", "boom", path} {
				if !strings.Contains(logs.String(), want) {
					t.Errorf("log %q does not contain %q", logs.String(), want)
				}
			}
		})
	}
}

func TestLogsRejectedHosts(t *testing.T) {
	h, logs := newLogged(&fakeCaddy{cfg: parse(t, `{}`)}, slog.LevelInfo)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Host = "evil.example.com"
	h.ServeHTTP(httptest.NewRecorder(), req)
	for _, want := range []string{"level=WARN", "evil.example.com", "/api/status"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log %q does not contain %q", logs.String(), want)
		}
	}
}

func TestLogsWhenUpstreamStateIsUnavailable(t *testing.T) {
	h, logs := newLogged(&fakeCaddy{cfg: parse(t, twoSitesConfig), upsErr: errors.New("upstreams down")}, slog.LevelInfo)
	if rec := do(t, h, http.MethodGet, "/api/sites"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	for _, want := range []string{"level=WARN", "upstreams down"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log %q does not contain %q", logs.String(), want)
		}
	}
}

func TestHealthyTrafficIsQuietAtInfoLevel(t *testing.T) {
	t.Run("successful requests", func(t *testing.T) {
		h, logs := newLogged(&fakeCaddy{cfg: parse(t, `{}`)}, slog.LevelInfo)
		do(t, h, http.MethodGet, "/api/config")
		do(t, h, http.MethodGet, "/api/sites")
		do(t, h, http.MethodGet, "/api/status")
		if logs.Len() != 0 {
			t.Errorf("successful requests logged at info level: %q", logs.String())
		}
	})
	t.Run("polling /api/status while Caddy is down", func(t *testing.T) {
		h, logs := newLogged(&fakeCaddy{cfgErr: errors.New("refused")}, slog.LevelInfo)
		do(t, h, http.MethodGet, "/api/status")
		if logs.Len() != 0 {
			t.Errorf("a status poll logged at info level, which would flood the log: %q", logs.String())
		}
	})
}

func TestDebugLevelLogsEveryRequest(t *testing.T) {
	h, logs := newLogged(&fakeCaddy{cfg: parse(t, `{}`)}, slog.LevelDebug)
	do(t, h, http.MethodGet, "/api/config")
	do(t, h, http.MethodGet, "/api/nope")
	got := logs.String()
	for _, want := range []string{"level=DEBUG", "msg=request", "method=GET", "path=/api/config", "status=200", "path=/api/nope", "status=404", "duration="} {
		if !strings.Contains(got, want) {
			t.Errorf("debug log %q does not contain %q", got, want)
		}
	}
}

func TestWithoutALoggerNothingBreaks(t *testing.T) {
	h := New(&fakeCaddy{cfgErr: errors.New("boom")}, Options{Endpoint: testEndpoint})
	if rec := do(t, h, http.MethodGet, "/api/config"); rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
