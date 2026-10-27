package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// safeBuffer is an io.Writer that can be read while the command is still writing to it.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var listeningLine = regexp.MustCompile(`http://127\.0\.0\.1:\d+`)

// startServe runs `caddy-view serve` in the background on a free loopback port.
func startServe(t *testing.T, extraArgs ...string) (baseURL string, stop func() int, stdout, stderr *safeBuffer) {
	t.Helper()
	stdout, stderr = &safeBuffer{}, &safeBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	args := append([]string{"serve", "--listen", "127.0.0.1:0"}, extraArgs...)
	done := make(chan int, 1)
	go func() { done <- Run(ctx, args, stdout, stderr) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := listeningLine.FindString(stdout.String()); m != "" {
			stop = func() int {
				cancel()
				select {
				case code := <-done:
					return code
				case <-time.After(10 * time.Second):
					t.Fatal("serve did not stop after the context was cancelled")
					return -1
				}
			}
			return m, stop, stdout, stderr
		}
		select {
		case code := <-done:
			t.Fatalf("serve exited with %d before listening\nstdout: %s\nstderr: %s", code, stdout, stderr)
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("serve never reported its address\nstdout: %s\nstderr: %s", stdout, stderr)
	return
}

func TestUsageListsServe(t *testing.T) {
	_, stdout, _ := run(t, "help")
	if !strings.Contains(stdout, "serve") {
		t.Errorf("usage %q does not list the serve command", stdout)
	}
}

func TestServeRefusesNonLoopback(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:0", ":0", "192.168.50.10:8484"} {
		t.Run(listen, func(t *testing.T) {
			code, stdout, stderr := run(t, "serve", "--listen", listen)
			if code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if !strings.Contains(stderr, "loopback") {
				t.Errorf("stderr %q does not explain that only loopback is allowed", stderr)
			}
			if listeningLine.MatchString(stdout) || strings.Contains(stdout, "listening") {
				t.Errorf("stdout %q suggests the server started", stdout)
			}
		})
	}
}

func TestServeFlagErrors(t *testing.T) {
	t.Run("unknown flag", func(t *testing.T) {
		code, _, stderr := run(t, "serve", "--nope")
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if !strings.Contains(stderr, "nope") {
			t.Errorf("stderr %q does not name the unknown flag", stderr)
		}
	})
	t.Run("invalid admin address", func(t *testing.T) {
		code, _, stderr := run(t, "serve", "--admin", "ftp://example.com", "--listen", "127.0.0.1:0")
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if !strings.Contains(stderr, "ftp") {
			t.Errorf("stderr %q does not explain the address problem", stderr)
		}
	})
}

func TestServeServesTheAPIAndStopsCleanly(t *testing.T) {
	admin := fakeAdmin(t, goodConfig)
	base, stop, stdout, _ := startServe(t, "--admin", admin.URL)

	if !strings.Contains(stdout.String(), admin.URL) {
		t.Errorf("startup output %q does not mention the Admin API %s", stdout.String(), admin.URL)
	}

	resp, err := http.Get(base + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	var status struct {
		Caddy struct {
			Reachable bool   `json:"reachable"`
			Endpoint  string `json:"endpoint"`
		} `json:"caddy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.Caddy.Reachable || status.Caddy.Endpoint != admin.URL {
		t.Errorf("status = %+v, want reachable at %s", status.Caddy, admin.URL)
	}

	if code := stop(); code != 0 {
		t.Errorf("exit code after a clean shutdown = %d, want 0", code)
	}
}

func TestServeStartsEvenWhenCaddyIsDown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	down := l.Addr().String()
	l.Close()

	base, stop, _, _ := startServe(t, "--admin", down)
	defer stop()

	resp, err := http.Get(base + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	var status struct {
		Caddy struct {
			Reachable bool   `json:"reachable"`
			Error     string `json:"error"`
		} `json:"caddy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if resp.StatusCode != http.StatusOK || status.Caddy.Reachable || status.Caddy.Error == "" {
		t.Errorf("status %d, caddy %+v, want 200 with an unreachable Caddy and an error message", resp.StatusCode, status.Caddy)
	}
}

func TestServeReportsAddressInUse(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	code, _, stderr := run(t, "serve", "--listen", busy.Addr().String())
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, busy.Addr().String()) {
		t.Errorf("stderr %q does not name the address that is in use", stderr)
	}
}
