package ingest_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/ingest"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// accessTime is the time of the sample lines: 2025-10-27 12:00:00.123 UTC.
var accessTime = time.Date(2025, time.October, 27, 12, 0, 0, 123_000_000, time.UTC)

// caddyLine builds a JSON access log line the way Caddy writes it, with the fields ingestion must ignore included.
func caddyLine(t testing.TB, host, uri string, status int) string {
	t.Helper()
	return jsonLine(t, map[string]any{
		"level": "info", "ts": 1761566400.123, "logger": "http.log.access.log0", "msg": "handled request",
		"request": map[string]any{
			"remote_ip": "10.0.0.7", "remote_port": "5555", "client_ip": "10.0.0.7", "proto": "HTTP/2.0",
			"method": "GET", "host": host, "uri": uri,
			"headers": map[string]any{"Authorization": []string{"Bearer secret"}, "Cookie": []string{"session=secret"}},
		},
		"bytes_read": 0, "user_id": "", "duration": 0.012345, "size": 4096, "status": status,
		"resp_headers": map[string]any{"Set-Cookie": []string{"session=secret"}},
	})
}

func jsonLine(t testing.TB, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseLine(t *testing.T) {
	t.Run("a Caddy access log line", func(t *testing.T) {
		got, err := ingest.ParseLine([]byte(caddyLine(t, "app.example.com", "/v1/items?token=secret", 502)), false)
		if err != nil {
			t.Fatal(err)
		}
		want := store.LogEntry{
			Time: accessTime, Host: "app.example.com", Method: "GET", Path: "/v1/items", Status: 502,
			Duration: 12345 * time.Microsecond, Bytes: 4096, ClientIP: "10.0.0.7",
		}
		if !got.Time.Equal(want.Time) {
			t.Errorf("Time = %v, want %v", got.Time, want.Time)
		}
		got.Time, want.Time = time.Time{}, time.Time{}
		if got != want {
			t.Errorf("entry = %+v, want %+v", got, want)
		}
	})

	t.Run("the query string is kept only on request", func(t *testing.T) {
		line := []byte(caddyLine(t, "h", "/login?token=secret&x=1", 200))
		dropped, err := ingest.ParseLine(line, false)
		if err != nil || dropped.Path != "/login" {
			t.Errorf("default: path = %q, %v, want /login", dropped.Path, err)
		}
		kept, err := ingest.ParseLine(line, true)
		if err != nil || kept.Path != "/login?token=secret&x=1" {
			t.Errorf("keep query: path = %q, %v, want the full URI", kept.Path, err)
		}
	})

	t.Run("the client IP falls back to the remote IP", func(t *testing.T) {
		line := jsonLine(t, map[string]any{
			"ts": 1761566400.5, "status": 200,
			"request": map[string]any{"remote_ip": "192.0.2.9", "host": "h", "method": "GET", "uri": "/"},
		})
		got, err := ingest.ParseLine([]byte(line), false)
		if err != nil || got.ClientIP != "192.0.2.9" {
			t.Errorf("ClientIP = %q, %v, want 192.0.2.9", got.ClientIP, err)
		}
	})

	t.Run("lines that are not access log entries", func(t *testing.T) {
		valid := map[string]any{"host": "h", "method": "GET", "uri": "/"}
		tests := map[string]string{
			"empty":                      ``,
			"not JSON":                   `this is not json`,
			"truncated JSON":             `{"ts":1761566400.1,"request":{"host":"h"`,
			"no request":                 `{"ts":1761566400.1,"msg":"server running"}`,
			"no host":                    jsonLine(t, map[string]any{"ts": 1761566400.1, "request": map[string]any{"method": "GET", "uri": "/"}}),
			"no time":                    jsonLine(t, map[string]any{"request": valid}),
			"a zero time":                jsonLine(t, map[string]any{"ts": 0, "request": valid}),
			"a text time":                jsonLine(t, map[string]any{"ts": "yesterday", "request": valid}),
			"a request that is a string": `{"ts":1761566400.1,"request":"GET /"}`,
		}
		for name, line := range tests {
			if _, err := ingest.ParseLine([]byte(line), false); !errors.Is(err, ingest.ErrBadLine) {
				t.Errorf("%s: error = %v, want ErrBadLine", name, err)
			}
		}
	})
}
