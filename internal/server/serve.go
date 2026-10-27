package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// ValidateListen accepts only a loopback host with a port, such as 127.0.0.1:8484, localhost:8484 or [::1]:8484.
// Until the UI has authentication, listening on any other address would expose the Caddy configuration.
func ValidateListen(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %v", addr, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("invalid listen address %q: port %q must be a number from 0 to 65535", addr, port)
	}
	if !isLoopbackName(host) {
		return fmt.Errorf("listen address %q is not a loopback address; caddy-view has no authentication yet, "+
			"so it only listens on 127.0.0.1, localhost or [::1] (use an SSH tunnel to reach it from another machine)", addr)
	}
	return nil
}

func isLoopbackName(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Serve listens on addr (which must pass ValidateListen), serves h, and returns nil after a graceful shutdown
// triggered by ctx. Requests in flight are allowed to finish. onListen, when not nil, receives the bound address
// once the listener is ready, which matters when the port is 0.
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

	srv := &http.Server{Handler: h, ReadHeaderTimeout: readHeaderTimeout}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		srv.Close()
		return fmt.Errorf("shut down: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
