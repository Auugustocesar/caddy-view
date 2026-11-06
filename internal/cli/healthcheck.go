package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// runHealthcheck asks a running caddy-view for /api/health and exits 0 only when it answers 200 with the status "ok". It
// exists for a container's HEALTHCHECK, since a minimal image has no curl. It needs no login: the route is open.
func runHealthcheck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	address := fs.String("address", "127.0.0.1:8484", "host:port of caddy-view; it must be a name caddy-view accepts, such as 127.0.0.1")
	timeout := fs.Duration("timeout", 5*time.Second, "how long to wait for the answer")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if _, _, err := net.SplitHostPort(*address); err != nil {
		fmt.Fprintf(stderr, "invalid --address %q: %v\n", *address, err)
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "invalid --timeout: it must be longer than zero")
		return 2
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if err := checkHealth(ctx, *address); err != nil {
		fmt.Fprintf(stderr, "unhealthy: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}

// checkHealth returns nil when the caddy-view at address says it is healthy.
func checkHealth(ctx context.Context, address string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/api/health", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", address, resp.Status)
	}
	var body struct {
		Status string `json:"status"`
	}
	// A small body: this is a status, and anything bigger is not caddy-view.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<10)).Decode(&body); err != nil || body.Status != "ok" {
		return fmt.Errorf("%s did not answer with the status ok: it may not be caddy-view", address)
	}
	return nil
}
