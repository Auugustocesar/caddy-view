package caddy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

// newTCPClient serves handler on a loopback TCP port and returns a client pointed at it.
func newTCPClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL)
	if err != nil {
		t.Fatalf("New(%q): %v", srv.URL, err)
	}
	return c
}

// newUnixClient serves handler on a unix socket and returns a client pointed at it.
func newUnixClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "cv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "admin.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("unix sockets not available: %v", err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	c, err := New("unix://" + sock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func jsonHandler(t *testing.T, wantMethod, wantPath string, status int, body string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != wantMethod || r.URL.Path != wantPath {
			t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, wantMethod, wantPath)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
}

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		endpoint string
		wantErr  bool
	}{
		{"unix URL", "unix:///run/caddy/admin.sock", "unix:///run/caddy/admin.sock", false},
		{"unix Caddy syntax", "unix//run/caddy/admin.sock", "unix:///run/caddy/admin.sock", false},
		{"http URL", "http://127.0.0.1:2019", "http://127.0.0.1:2019", false},
		{"trailing slash is dropped", "http://127.0.0.1:2019/", "http://127.0.0.1:2019", false},
		{"bare host and port", "localhost:2019", "http://localhost:2019", false},
		{"empty", "", "", true},
		{"unsupported scheme", "ftp://example.com", "", true},
		{"unix without a path", "unix://", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("New(%q) succeeded, want an error", tt.addr)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%q): %v", tt.addr, err)
			}
			if got := c.Endpoint(); got != tt.endpoint {
				t.Errorf("Endpoint() = %q, want %q", got, tt.endpoint)
			}
		})
	}
}

func TestConfig(t *testing.T) {
	const body = `{"apps":{"http":{"servers":{"srv0":{"listen":[":443"]}}}}}`
	transports := map[string]func(*testing.T, http.Handler) *Client{"tcp": newTCPClient, "unix": newUnixClient}
	for name, newClient := range transports {
		t.Run(name, func(t *testing.T) {
			c := newClient(t, jsonHandler(t, http.MethodGet, "/config/", http.StatusOK, body))
			got, err := c.Config(context.Background())
			if err != nil {
				t.Fatalf("Config: %v", err)
			}
			want := map[string]any{"apps": map[string]any{"http": map[string]any{"servers": map[string]any{
				"srv0": map[string]any{"listen": []any{":443"}},
			}}}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Config = %v, want %v", got, want)
			}
		})
	}
}

func TestConfigErrors(t *testing.T) {
	t.Run("API error carries status and message and is not unreachable", func(t *testing.T) {
		c := newTCPClient(t, jsonHandler(t, http.MethodGet, "/config/", http.StatusInternalServerError, `{"error":"boom"}`))
		_, err := c.Config(context.Background())
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
		if apiErr.Status != http.StatusInternalServerError || apiErr.Message != "boom" {
			t.Errorf("APIError = %+v, want status 500 message %q", apiErr, "boom")
		}
		if errors.Is(err, ErrUnreachable) {
			t.Error("an API error must not be reported as unreachable")
		}
	})
	t.Run("API error without a JSON body still reports the status", func(t *testing.T) {
		c := newTCPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "gateway down", http.StatusBadGateway)
		}))
		_, err := c.Config(context.Background())
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway {
			t.Fatalf("err = %v, want *APIError with status 502", err)
		}
	})
	t.Run("invalid JSON", func(t *testing.T) {
		c := newTCPClient(t, jsonHandler(t, http.MethodGet, "/config/", http.StatusOK, `{not json`))
		if _, err := c.Config(context.Background()); err == nil {
			t.Fatal("Config succeeded on invalid JSON, want an error")
		}
	})
}

func TestUnreachable(t *testing.T) {
	t.Run("tcp connection refused", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := l.Addr().String()
		l.Close()
		c, err := New(addr)
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Config(context.Background())
		if !errors.Is(err, ErrUnreachable) {
			t.Fatalf("err = %v, want ErrUnreachable", err)
		}
	})
	t.Run("missing unix socket", func(t *testing.T) {
		sock := filepath.Join(t.TempDir(), "missing.sock")
		c, err := New("unix://" + sock)
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Config(context.Background())
		if !errors.Is(err, ErrUnreachable) {
			t.Fatalf("err = %v, want ErrUnreachable", err)
		}
	})
}

func TestContextDeadline(t *testing.T) {
	c := newTCPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Config(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("client did not honor the context deadline")
	}
}

func TestAdapt(t *testing.T) {
	const caddyfile = "example.com {\n\trespond ok\n}\n"
	t.Run("success returns config and warnings", func(t *testing.T) {
		c := newTCPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/adapt" {
				t.Errorf("request = %s %s, want POST /adapt", r.Method, r.URL.Path)
			}
			if ct := r.Header.Get("Content-Type"); ct != "text/caddyfile" {
				t.Errorf("Content-Type = %q, want text/caddyfile", ct)
			}
			if b, _ := io.ReadAll(r.Body); string(b) != caddyfile {
				t.Errorf("body = %q, want %q", b, caddyfile)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"result":{"apps":{}},"warnings":[{"file":"Caddyfile","line":2,"message":"input is not formatted"}]}`)
		}))
		got, err := c.Adapt(context.Background(), caddyfile)
		if err != nil {
			t.Fatalf("Adapt: %v", err)
		}
		if got == nil {
			t.Fatal("Adapt returned a nil result")
		}
		if !reflect.DeepEqual(got.Config, map[string]any{"apps": map[string]any{}}) {
			t.Errorf("Config = %v", got.Config)
		}
		want := []Warning{{File: "Caddyfile", Line: 2, Message: "input is not formatted"}}
		if !reflect.DeepEqual(got.Warnings, want) {
			t.Errorf("Warnings = %+v, want %+v", got.Warnings, want)
		}
	})
	t.Run("syntax error reports the line", func(t *testing.T) {
		msg := "adapting config using caddyfile: Caddyfile:3: unrecognized directive: nope"
		c := newTCPClient(t, jsonHandler(t, http.MethodPost, "/adapt", http.StatusBadRequest, `{"error":"`+msg+`"}`))
		_, err := c.Adapt(context.Background(), caddyfile)
		var adaptErr *AdaptError
		if !errors.As(err, &adaptErr) {
			t.Fatalf("err = %v, want *AdaptError", err)
		}
		if adaptErr.Line != 3 {
			t.Errorf("Line = %d, want 3", adaptErr.Line)
		}
		if adaptErr.Message != msg {
			t.Errorf("Message = %q, want %q", adaptErr.Message, msg)
		}
	})
	t.Run("syntax error without a line number", func(t *testing.T) {
		c := newTCPClient(t, jsonHandler(t, http.MethodPost, "/adapt", http.StatusBadRequest, `{"error":"adapting config using caddyfile: empty"}`))
		_, err := c.Adapt(context.Background(), "")
		var adaptErr *AdaptError
		if !errors.As(err, &adaptErr) {
			t.Fatalf("err = %v, want *AdaptError", err)
		}
		if adaptErr.Line != 0 {
			t.Errorf("Line = %d, want 0", adaptErr.Line)
		}
	})
	t.Run("other failures are API errors, not adapt errors", func(t *testing.T) {
		c := newTCPClient(t, jsonHandler(t, http.MethodPost, "/adapt", http.StatusInternalServerError, `{"error":"boom"}`))
		_, err := c.Adapt(context.Background(), caddyfile)
		var adaptErr *AdaptError
		if errors.As(err, &adaptErr) {
			t.Fatalf("err = %v, a 500 must not be an *AdaptError", err)
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusInternalServerError {
			t.Fatalf("err = %v, want *APIError with status 500", err)
		}
	})
}

func TestLoad(t *testing.T) {
	const caddyfile = "example.com {\n\trespond ok\n}\n"
	t.Run("success", func(t *testing.T) {
		var called atomic.Bool
		c := newTCPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called.Store(true)
			if r.Method != http.MethodPost || r.URL.Path != "/load" {
				t.Errorf("request = %s %s, want POST /load", r.Method, r.URL.Path)
			}
			if ct := r.Header.Get("Content-Type"); ct != "text/caddyfile" {
				t.Errorf("Content-Type = %q, want text/caddyfile", ct)
			}
			if b, _ := io.ReadAll(r.Body); string(b) != caddyfile {
				t.Errorf("body = %q, want %q", b, caddyfile)
			}
		}))
		if err := c.Load(context.Background(), caddyfile); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !called.Load() {
			t.Error("Load never sent a request")
		}
	})
	t.Run("rejected config is an API error with Caddy's message", func(t *testing.T) {
		c := newTCPClient(t, jsonHandler(t, http.MethodPost, "/load", http.StatusBadRequest, `{"error":"loading config: port already in use"}`))
		err := c.Load(context.Background(), caddyfile)
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
		if apiErr.Status != http.StatusBadRequest || apiErr.Message != "loading config: port already in use" {
			t.Errorf("APIError = %+v", apiErr)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		c, err := New("unix://" + filepath.Join(t.TempDir(), "missing.sock"))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Load(context.Background(), caddyfile); !errors.Is(err, ErrUnreachable) {
			t.Fatalf("err = %v, want ErrUnreachable", err)
		}
	})
}

func TestMetrics(t *testing.T) {
	const text = "# HELP caddy_http_requests_total Counter of HTTP requests.\ncaddy_http_requests_total 7\n"
	c := newTCPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/metrics" {
			t.Errorf("request = %s %s, want GET /metrics", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		io.WriteString(w, text)
	}))
	got, err := c.Metrics(context.Background())
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if string(got) != text {
		t.Errorf("Metrics = %q, want %q", got, text)
	}
}

func TestUpstreams(t *testing.T) {
	c := newTCPClient(t, jsonHandler(t, http.MethodGet, "/reverse_proxy/upstreams", http.StatusOK,
		`[{"address":"10.0.0.5:8080","num_requests":2,"fails":1},{"address":"10.0.0.6:8080","num_requests":0,"fails":0}]`))
	got, err := c.Upstreams(context.Background())
	if err != nil {
		t.Fatalf("Upstreams: %v", err)
	}
	want := []Upstream{
		{Address: "10.0.0.5:8080", NumRequests: 2, Fails: 1},
		{Address: "10.0.0.6:8080", NumRequests: 0, Fails: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Upstreams = %+v, want %+v", got, want)
	}
}

func TestUnreachableKeepsTheUnderlyingCause(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	c, err := New(addr)
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Config(context.Background())
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) || opErr.Op != "dial" {
		t.Errorf("the dial error is lost from the chain: %v", err)
	}
}

func TestErrorMessages(t *testing.T) {
	apiErr := &APIError{Status: http.StatusInternalServerError, Message: "boom"}
	if got, want := apiErr.Error(), "caddy admin API returned 500: boom"; got != want {
		t.Errorf("APIError.Error() = %q, want %q", got, want)
	}
	adaptErr := &AdaptError{Message: "Caddyfile:3: unrecognized directive: nope", Line: 3}
	if got, want := adaptErr.Error(), "Caddyfile:3: unrecognized directive: nope"; got != want {
		t.Errorf("AdaptError.Error() = %q, want %q", got, want)
	}
}

func TestErrorMessageFallbacks(t *testing.T) {
	t.Run("plain text body is used as the message", func(t *testing.T) {
		c := newTCPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "gateway down", http.StatusBadGateway)
		}))
		_, err := c.Config(context.Background())
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Message != "gateway down" {
			t.Fatalf("err = %v, want *APIError with message %q", err, "gateway down")
		}
	})
	t.Run("empty body falls back to the status text", func(t *testing.T) {
		c := newTCPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		_, err := c.Config(context.Background())
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Message != "Service Unavailable" {
			t.Fatalf("err = %v, want *APIError with message %q", err, "Service Unavailable")
		}
	})
	t.Run("JSON body without an error field falls back to the raw body", func(t *testing.T) {
		c := newTCPClient(t, jsonHandler(t, http.MethodGet, "/config/", http.StatusInternalServerError, `{"detail":"x"}`))
		_, err := c.Config(context.Background())
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Message != `{"detail":"x"}` {
			t.Fatalf("err = %v, want *APIError with the raw body as message", err)
		}
	})
}

func TestUpstreamsErrors(t *testing.T) {
	t.Run("API error", func(t *testing.T) {
		c := newTCPClient(t, jsonHandler(t, http.MethodGet, "/reverse_proxy/upstreams", http.StatusNotFound, `{"error":"no reverse proxy"}`))
		_, err := c.Upstreams(context.Background())
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
			t.Fatalf("err = %v, want *APIError with status 404", err)
		}
	})
	t.Run("invalid JSON", func(t *testing.T) {
		c := newTCPClient(t, jsonHandler(t, http.MethodGet, "/reverse_proxy/upstreams", http.StatusOK, `not json`))
		if _, err := c.Upstreams(context.Background()); err == nil {
			t.Fatal("Upstreams succeeded on invalid JSON, want an error")
		}
	})
}

func TestNewRejectsMalformedURLs(t *testing.T) {
	for _, addr := range []string{"http://[::1", "http://", "http:///config"} {
		t.Run(addr, func(t *testing.T) {
			if _, err := New(addr); err == nil {
				t.Errorf("New(%q) succeeded, want an error", addr)
			}
		})
	}
}
