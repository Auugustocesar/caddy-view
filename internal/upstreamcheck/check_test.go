package upstreamcheck

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func hostPort(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	return strings.TrimPrefix(srv.URL, "http://")
}

func newChecker() *Checker {
	return &Checker{Timeout: 2 * time.Second, Now: func() time.Time { return time.Unix(1700000000, 0) }}
}

func TestCheckAnyHTTPAnswerIsAnswering(t *testing.T) {
	for _, status := range []int{200, 204, 301, 307, 400, 404, 500, 502, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			t.Cleanup(srv.Close)

			got := newChecker().Check(context.Background(), hostPort(t, srv))
			if got.State != Answering {
				t.Fatalf("State = %q, want %q", got.State, Answering)
			}
			if got.Status != status {
				t.Errorf("Status = %d, want %d", got.Status, status)
			}
			if got.Latency < 0 {
				t.Errorf("Latency = %v, want >= 0", got.Latency)
			}
			if !got.CheckedAt.Equal(time.Unix(1700000000, 0)) {
				t.Errorf("CheckedAt = %v, want the injected time", got.CheckedAt)
			}
		})
	}
}

func TestCheckAsksForSlashWithoutCredentialsAndWithoutFollowingRedirects(t *testing.T) {
	var hits atomic.Int32
	var gotPath, gotAuth, gotCookie, gotAgent string
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(100) // a followed redirect would land here
	}))
	t.Cleanup(other.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotPath, gotAuth, gotCookie, gotAgent = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Cookie"), r.UserAgent()
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	got := newChecker().Check(context.Background(), hostPort(t, srv))

	if got.State != Answering {
		t.Fatalf("State = %q, want %q", got.State, Answering)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("hits = %d, want exactly the one request to the upstream and no redirect followed", n)
	}
	if gotPath != "/" {
		t.Errorf("path = %q, want /", gotPath)
	}
	if gotAuth != "" || gotCookie != "" {
		t.Errorf("sent credentials: Authorization=%q Cookie=%q", gotAuth, gotCookie)
	}
	if !strings.HasPrefix(gotAgent, "caddy-view") {
		t.Errorf("User-Agent = %q, want one that names caddy-view so the owner can tell the check apart", gotAgent)
	}
}

func TestCheckRefusedConnectionIsDown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	got := newChecker().Check(context.Background(), addr)
	if got.State != Down {
		t.Errorf("State = %q, want %q", got.State, Down)
	}
}

func TestCheckNoAnswerWithinTheTimeoutIsDown(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs first, so Close does not wait for the handler

	c := newChecker()
	c.Timeout = 50 * time.Millisecond
	got := c.Check(context.Background(), hostPort(t, srv))
	if got.State != Down {
		t.Errorf("State = %q, want %q", got.State, Down)
	}
}

func TestCheckClosedWithoutAnswerIsDown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_ = conn.Close() // accepts and hangs up: something is listening but it does not speak HTTP
		}
	}()

	got := newChecker().Check(context.Background(), l.Addr().String())
	if got.State != Down {
		t.Errorf("State = %q, want %q", got.State, Down)
	}
}

func TestCheckCannotBeKnownIsUnknown(t *testing.T) {
	tests := []struct {
		name    string
		address string
		dialErr error
	}{
		{name: "network unreachable", address: "10.9.9.9:80", dialErr: &net.OpError{Op: "dial", Err: syscall.ENETUNREACH}},
		{name: "no route to host", address: "10.9.9.9:80", dialErr: &net.OpError{Op: "dial", Err: syscall.EHOSTUNREACH}},
		{name: "name does not resolve", address: "nope.invalid:80", dialErr: &net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true}},
		{name: "name lookup times out", address: "slow.invalid:80", dialErr: &net.DNSError{Err: "i/o timeout", Name: "slow.invalid", IsTimeout: true}},
		{name: "unix socket", address: "unix//run/app.sock"},
		{name: "placeholder", address: "{env.UPSTREAM}:80"},
		{name: "placeholder in the port", address: "10.0.0.1:{env.PORT}"},
		{name: "no port", address: "10.0.0.1"},
		{name: "empty", address: ""},
		{name: "port range", address: "10.0.0.1:8000-8010"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newChecker()
			c.Dial = func(context.Context, string, string) (net.Conn, error) {
				if tt.dialErr == nil {
					t.Error("dialled an address that cannot be checked")
					return nil, errors.New("unexpected dial")
				}
				return nil, tt.dialErr
			}
			got := c.Check(context.Background(), tt.address)
			if got.State != Unknown {
				t.Errorf("State = %q, want %q", got.State, Unknown)
			}
		})
	}
}

func TestCheckCancelledContextIsUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := newChecker().Check(ctx, hostPort(t, srv))
	if got.State != Unknown {
		t.Errorf("State = %q, want %q: a stopped check says nothing about the upstream", got.State, Unknown)
	}
}

func TestCheckSchemeInTheAddress(t *testing.T) {
	t.Run("https is checked over TLS without verifying the certificate", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		t.Cleanup(srv.Close)

		got := newChecker().Check(context.Background(), srv.URL) // https://127.0.0.1:port with a self-signed certificate
		if got.State != Answering {
			t.Errorf("State = %q, want %q", got.State, Answering)
		}
	})
	t.Run("http", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		t.Cleanup(srv.Close)

		got := newChecker().Check(context.Background(), srv.URL)
		if got.State != Answering {
			t.Errorf("State = %q, want %q", got.State, Answering)
		}
	})
}
