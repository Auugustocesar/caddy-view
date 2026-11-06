package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/netutil"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// ErrInvalidListen means a listen address is malformed: no port, or a port that is not a number from 0 to 65535.
var ErrInvalidListen = errors.New("invalid listen address")

// ErrNotLoopback means a listen address is well formed but does not name this machine.
var ErrNotLoopback = errors.New("listen address is not a loopback address")

// ValidateListen checks the syntax of a listen address: a host, which may be empty, and a port that is a number from 0
// to 65535, such as 127.0.0.1:8484, :8484 or [::1]:8484. Whether the address may be one that others can reach is
// RequireLoopback's question, and depends on whether there is a login.
func ValidateListen(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%w %q: %w", ErrInvalidListen, addr, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("%w %q: port %q must be a number from 0 to 65535", ErrInvalidListen, addr, port)
	}
	return nil
}

// RequireLoopback accepts only a loopback host with a port, such as 127.0.0.1:8484, localhost:8484 or [::1]:8484. A
// malformed address is ErrInvalidListen, and a well formed one that is not loopback is ErrNotLoopback.
func RequireLoopback(addr string) error {
	if err := ValidateListen(addr); err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(addr) // ValidateListen has checked it
	if !netutil.IsLoopbackHost(host) {
		return fmt.Errorf("%w: %q", ErrNotLoopback, addr)
	}
	return nil
}

// Serve listens on addr (which must pass ValidateListen; the caller decides whether it may be reachable by others), serves h, and returns nil after a graceful shutdown
// triggered by ctx. Requests in flight are allowed to finish, except long-lived ones, whose context is cancelled.
// onListen, when not nil, receives the bound address once the listener is ready, which matters when the port is 0.
func Serve(ctx context.Context, addr string, h http.Handler, onListen func(net.Addr)) error {
	if err := ValidateListen(addr); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	if onListen != nil {
		onListen(ln.Addr())
	}

	// Requests share a context that ends when the shutdown starts, so long-lived responses (the log stream) stop
	// instead of holding the shutdown until its timeout.
	requests, endRequests := context.WithCancel(context.Background())
	defer endRequests()
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return requests },
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	endRequests()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close() // the shutdown error is the one worth reporting
		return fmt.Errorf("shut down: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
