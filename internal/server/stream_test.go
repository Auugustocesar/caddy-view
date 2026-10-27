package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const streamPath = "/api/logs/stream"

func writeLog(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendLog(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

// streamServer serves the API on a real loopback port, because a stream needs a real connection.
func streamServer(t *testing.T, f *fakeCaddy, opts Options) *httptest.Server {
	t.Helper()
	opts.Endpoint = testEndpoint
	if opts.LogPollInterval == 0 {
		opts.LogPollInterval = 5 * time.Millisecond
	}
	srv := httptest.NewServer(New(f, opts))
	t.Cleanup(srv.Close)
	return srv
}

// logConfig is a Caddy configuration whose only logger writes JSON to path.
func logConfig(path string) map[string]any {
	return map[string]any{"logging": map[string]any{"logs": map[string]any{"log0": map[string]any{
		"writer":  map[string]any{"output": "file", "filename": path},
		"encoder": map[string]any{"format": "json"},
	}}}}
}

type stream struct {
	resp *http.Response
	br   *bufio.Reader
}

// openStream starts a GET and returns without reading the body. The request is cancelled when the test ends.
func openStream(t *testing.T, url string) *stream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return &stream{resp: resp, br: bufio.NewReader(resp.Body)}
}

// next returns the next SSE field line ("data: x" or ": comment") and checks that it ends the event with a blank
// line, as the format requires.
func (s *stream) next(t *testing.T) string {
	t.Helper()
	line, err := s.br.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the stream: %v", err)
	}
	line = strings.TrimRight(line, "\r\n")
	blank, err := s.br.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the end of the event: %v", err)
	}
	if strings.TrimRight(blank, "\r\n") != "" {
		t.Fatalf("event %q is not followed by a blank line (got %q)", line, blank)
	}
	return line
}

// data returns the payload of the next data event, skipping comments such as pings.
func (s *stream) data(t *testing.T) string {
	t.Helper()
	for {
		line := s.next(t)
		if payload, ok := strings.CutPrefix(line, "data: "); ok {
			return payload
		}
	}
}

// comment reads events until the given comment arrives, failing if a data event comes first.
func (s *stream) comment(t *testing.T, want string) {
	t.Helper()
	for {
		line := s.next(t)
		if line == want {
			return
		}
		if strings.HasPrefix(line, "data: ") {
			t.Fatalf("got data %q while waiting for the comment %q", line, want)
		}
	}
}

func TestStreamSendsLiveLines(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "access.log")
	writeLog(t, logPath, "old\n")
	f := &fakeCaddy{}
	srv := streamServer(t, f, Options{AccessLogPath: logPath})

	s := openStream(t, srv.URL+streamPath)
	if s.resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", s.resp.StatusCode)
	}
	h := s.resp.Header
	if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := h.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if xc := h.Get("X-Content-Type-Options"); xc != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", xc)
	}

	// The stream announces itself once the file is followed, so appending after it cannot be missed.
	s.comment(t, ": connected")
	appendLog(t, logPath, `{"status":200}`+"\n"+`{"status":404}`+"\n")
	if got := s.data(t); got != `{"status":200}` {
		t.Errorf("first line = %q, want the first appended line and not the old one", got)
	}
	if got := s.data(t); got != `{"status":404}` {
		t.Errorf("second line = %q", got)
	}
	if n := f.calls.Load(); n != 0 {
		t.Errorf("Caddy was queried %d times although the log path was given", n)
	}
}

func TestStreamBacklog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "access.log")
	writeLog(t, logPath, "l1\nl2\nl3\nl4\nl5\n")
	srv := streamServer(t, &fakeCaddy{}, Options{AccessLogPath: logPath})

	s := openStream(t, srv.URL+streamPath+"?backlog=2")
	s.comment(t, ": connected")
	appendLog(t, logPath, "l6\n")
	for _, want := range []string{"l4", "l5", "l6"} {
		if got := s.data(t); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestStreamRejectsAnInvalidBacklog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "access.log")
	writeLog(t, logPath, "")
	srv := streamServer(t, &fakeCaddy{}, Options{AccessLogPath: logPath})
	for _, value := range []string{"abc", "-1", "1001", "1.5", ""} {
		t.Run(fmt.Sprintf("backlog=%q", value), func(t *testing.T) {
			resp, err := http.Get(srv.URL + streamPath + "?backlog=" + value)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json for an error", ct)
			}
		})
	}
	t.Run("the largest allowed backlog is accepted", func(t *testing.T) {
		s := openStream(t, srv.URL+streamPath+"?backlog=1000")
		if s.resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", s.resp.StatusCode)
		}
	})
}

func TestStreamDiscoversTheLogFromTheConfig(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "access.log")
	writeLog(t, logPath, "")
	srv := streamServer(t, &fakeCaddy{cfg: logConfig(logPath)}, Options{})

	s := openStream(t, srv.URL+streamPath)
	if s.resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", s.resp.StatusCode)
	}
	s.comment(t, ": connected")
	appendLog(t, logPath, "from-config\n")
	if got := s.data(t); got != "from-config" {
		t.Errorf("line = %q, want %q", got, "from-config")
	}
}

func TestStreamPrefersTheExplicitPathOverTheConfig(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit.log")
	configured := filepath.Join(dir, "configured.log")
	writeLog(t, explicit, "")
	writeLog(t, configured, "")
	srv := streamServer(t, &fakeCaddy{cfg: logConfig(configured)}, Options{AccessLogPath: explicit})

	s := openStream(t, srv.URL+streamPath)
	s.comment(t, ": connected")
	appendLog(t, configured, "from-config\n")
	appendLog(t, explicit, "from-flag\n")
	if got := s.data(t); got != "from-flag" {
		t.Errorf("line = %q, want %q", got, "from-flag")
	}
}

func TestStreamWaitsForALogFileThatDoesNotExistYet(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "later.log")
	srv := streamServer(t, &fakeCaddy{}, Options{AccessLogPath: logPath})

	s := openStream(t, srv.URL+streamPath)
	if s.resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", s.resp.StatusCode)
	}
	s.comment(t, ": connected")
	writeLog(t, logPath, "first\n")
	if got := s.data(t); got != "first" {
		t.Errorf("line = %q, want %q", got, "first")
	}
}

func TestStreamErrors(t *testing.T) {
	t.Run("no log file in the config and none given is a 404", func(t *testing.T) {
		srv := streamServer(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{})
		resp, err := http.Get(srv.URL + streamPath)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
		var body struct {
			Error string `json:"error"`
		}
		decode(t, recorderOf(t, resp), &body)
		if !strings.Contains(body.Error, "access log") {
			t.Errorf("error %q does not say that no access log was found", body.Error)
		}
	})
	t.Run("unreadable config and no path given is a bad gateway", func(t *testing.T) {
		srv := streamServer(t, &fakeCaddy{cfgErr: errors.New("boom")}, Options{})
		resp, err := http.Get(srv.URL + streamPath)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", resp.StatusCode)
		}
	})
	t.Run("a directory cannot be followed", func(t *testing.T) {
		srv := streamServer(t, &fakeCaddy{}, Options{AccessLogPath: t.TempDir()})
		resp, err := http.Get(srv.URL + streamPath)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", resp.StatusCode)
		}
	})
}

// recorderOf copies a real response into a recorder so the JSON helpers can read it.
func recorderOf(t *testing.T, resp *http.Response) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	if _, err := rec.Body.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestStreamLimitAndRelease(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "access.log")
	writeLog(t, logPath, "")
	srv := streamServer(t, &fakeCaddy{}, Options{AccessLogPath: logPath, MaxStreams: 1})

	ctx, cancelFirst := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+streamPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first stream status = %d, want 200", first.StatusCode)
	}

	second, err := http.Get(srv.URL + streamPath)
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusTooManyRequests {
		t.Errorf("second stream status = %d, want 429 while the first is open", second.StatusCode)
	}

	// Closing the first stream must free its slot.
	cancelFirst()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(srv.URL + streamPath)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return
		}
		if err == nil {
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatal("the slot of a disconnected stream was never released")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStreamSendsHeartbeats(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "access.log")
	writeLog(t, logPath, "")
	srv := streamServer(t, &fakeCaddy{}, Options{AccessLogPath: logPath, HeartbeatInterval: 20 * time.Millisecond})

	s := openStream(t, srv.URL+streamPath)
	s.comment(t, ": connected")
	s.comment(t, ": ping")
	s.comment(t, ": ping")
}

func TestStreamRefusesForeignHosts(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "access.log")
	writeLog(t, logPath, "secret\n")
	h := New(&fakeCaddy{}, Options{AccessLogPath: logPath})
	req := httptest.NewRequest(http.MethodGet, streamPath, nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
