package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestValidateListen(t *testing.T) {
	accepted := []string{
		"127.0.0.1:8484",
		"localhost:8484",
		"LOCALHOST:8484",
		"[::1]:8484",
		"127.0.0.1:0",
		"127.1.2.3:80",
	}
	for _, addr := range accepted {
		t.Run("accepts "+addr, func(t *testing.T) {
			if err := ValidateListen(addr); err != nil {
				t.Errorf("ValidateListen(%q) = %v, want nil", addr, err)
			}
		})
	}

	notLoopback := []string{
		"0.0.0.0:8484",
		":8484",
		"[::]:8484",
		"192.168.50.10:8484",
		"example.com:8484",
		"localhost.evil.com:8484",
		"127.0.0.1.evil.com:8484",
	}
	for _, addr := range notLoopback {
		t.Run("rejects "+addr, func(t *testing.T) {
			err := ValidateListen(addr)
			if err == nil {
				t.Fatalf("ValidateListen(%q) = nil, want an error", addr)
			}
			if !errors.Is(err, ErrNotLoopback) {
				t.Errorf("error %q is not ErrNotLoopback", err)
			}
			if errors.Is(err, ErrInvalidListen) {
				t.Errorf("error %q is also ErrInvalidListen; a well-formed address must not be reported as malformed", err)
			}
		})
	}

	malformed := []string{"", "8484", "127.0.0.1", "127.0.0.1:99999", "127.0.0.1:abc", "127.0.0.1:-1"}
	for _, addr := range malformed {
		t.Run("rejects malformed "+addr, func(t *testing.T) {
			err := ValidateListen(addr)
			if err == nil {
				t.Fatalf("ValidateListen(%q) = nil, want an error", addr)
			}
			if !errors.Is(err, ErrInvalidListen) {
				t.Errorf("error %q is not ErrInvalidListen", err)
			}
			if errors.Is(err, ErrNotLoopback) {
				t.Errorf("error %q is also ErrNotLoopback; a malformed address says nothing about loopback", err)
			}
		})
	}
}

// startServe runs Serve on a free loopback port and returns its base URL, a cancel func, and the channel that
// receives Serve's result.
func startServe(t *testing.T, h http.Handler) (baseURL string, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	addrCh := make(chan net.Addr, 1)
	result := make(chan error, 1)
	go func() {
		result <- Serve(ctx, "127.0.0.1:0", h, func(a net.Addr) { addrCh <- a })
	}()
	select {
	case a := <-addrCh:
		return "http://" + a.String(), cancel, result
	case err := <-result:
		t.Fatalf("Serve returned before listening: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not report a listening address")
	}
	return "", cancel, result
}

func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return")
		return nil
	}
}

func TestServeServesAndStopsOnCancel(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) })
	base, cancel, done := startServe(t, h)

	if !strings.HasPrefix(base, "http://127.0.0.1:") || strings.HasSuffix(base, ":0") {
		t.Fatalf("reported address %q is not a real loopback address", base)
	}
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	cancel()
	if err := waitDone(t, done); err != nil {
		t.Errorf("Serve returned %v after a clean shutdown, want nil", err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	if resp, err := client.Get(base + "/"); err == nil {
		resp.Body.Close()
		t.Error("the server still accepts connections after shutdown")
	}
}

func TestServeLetsRequestsInFlightFinish(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Write([]byte("finished"))
	})
	base, cancel, done := startServe(t, h)

	type result struct {
		status int
		err    error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err != nil {
			got <- result{err: err}
			return
		}
		resp.Body.Close()
		got <- result{status: resp.StatusCode}
	}()
	<-started

	cancel()
	select {
	case err := <-done:
		t.Fatalf("Serve returned (%v) while a request was still in flight", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	r := <-got
	if r.err != nil || r.status != http.StatusOK {
		t.Errorf("in-flight request: status %d, err %v, want 200 and no error", r.status, r.err)
	}
	if err := waitDone(t, done); err != nil {
		t.Errorf("Serve returned %v, want nil", err)
	}
}

func TestServeRefusesNonLoopbackWithoutListening(t *testing.T) {
	called := false
	err := Serve(context.Background(), "0.0.0.0:0", http.NotFoundHandler(), func(net.Addr) { called = true })
	if !errors.Is(err, ErrNotLoopback) {
		t.Fatalf("Serve = %v, want ErrNotLoopback", err)
	}
	if called {
		t.Error("onListen was called for an address that must be refused")
	}
}

func TestServeReportsAddressInUse(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	called := false
	err = Serve(context.Background(), busy.Addr().String(), http.NotFoundHandler(), func(net.Addr) { called = true })
	if err == nil {
		t.Fatal("Serve = nil, want an error for an address that is in use")
	}
	if !strings.Contains(err.Error(), busy.Addr().String()) {
		t.Errorf("error %q does not name the address that is in use", err)
	}
	if called {
		t.Error("onListen was called although the listener could not be created")
	}
}

func TestServeWorksWithoutOnListen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, "127.0.0.1:0", http.NotFoundHandler(), nil) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := waitDone(t, done); err != nil {
		t.Errorf("Serve = %v, want nil", err)
	}
}

// A long-lived response, such as the log stream, must not hold up the shutdown until the timeout.
func TestServeEndsLongLivedRequestsOnShutdown(t *testing.T) {
	started := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	base, cancel, done := startServe(t, h)

	go func() {
		if resp, err := http.Get(base + "/stream"); err == nil {
			resp.Body.Close()
		}
	}()
	<-started

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil after ending the open request", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the open request kept the server from shutting down")
	}
}
