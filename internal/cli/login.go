package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/auth"
	"github.com/Auugustocesar/caddy-view/internal/netutil"
	"github.com/Auugustocesar/caddy-view/internal/server"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// pruneSessionsEvery is how often serve removes the sessions that have ended.
const pruneSessionsEvery = time.Hour

// checkListenFlags decides where caddy-view may listen, from the flags that bear on it. On a loopback address anything
// goes. Beyond it, the login must be on, and plain HTTP must have been accepted explicitly, since the password and the
// session cookie would cross the network in clear text. The messages name the flags.
func checkListenFlags(listen string, noAuth, allowInsecureHTTP bool) error {
	err := server.RequireLoopback(listen)
	if err == nil {
		return nil
	}
	if !errors.Is(err, server.ErrNotLoopback) {
		return err
	}
	switch {
	case noAuth:
		return fmt.Errorf("invalid --no-auth: it needs a loopback --listen (127.0.0.1, localhost or [::1]), and %s is "+
			"reachable by others; without a login anyone who reaches the port can change Caddy's configuration", listen)
	case !allowInsecureHTTP:
		return fmt.Errorf("invalid --listen %s: caddy-view serves plain HTTP, so on an address that others can reach the "+
			"password would cross the network in clear text; put caddy-view behind TLS (a Caddy site that proxies to it) "+
			"and pass --allow-insecure-http to accept that, or use a loopback address", listen)
	}
	return nil
}

// allowedHosts checks the names given with --allowed-host and returns them in lower case, without repeats, followed by
// the host of the listen address when that names a particular machine: whoever binds 192.168.1.10 means to be reached
// by it. A name must be a bare host name or IP address: no scheme, path, port or wildcard.
func allowedHosts(listen string, given []string) ([]string, error) {
	var out []string
	add := func(h string) {
		for _, have := range out {
			if have == h {
				return
			}
		}
		out = append(out, h)
	}
	for _, h := range given {
		h = strings.TrimSpace(h)
		if h == "" || strings.ContainsAny(h, "/:*[]@?# \t") {
			return nil, fmt.Errorf("invalid --allowed-host %q: give a bare host name or IP address, without scheme, port or wildcard", h)
		}
		add(strings.ToLower(h))
	}
	if host, _, err := net.SplitHostPort(listen); err == nil && host != "" {
		if ip := net.ParseIP(host); (ip == nil || !ip.IsUnspecified()) && !netutil.IsLoopbackHost(host) {
			add(strings.ToLower(host))
		}
	}
	return out, nil
}

// loginHistory is what serve's login needs from the store, besides the history.
type loginHistory = auth.Store

// startLogin turns the login on: it creates the first user when there is none and prints its password once, removes the
// sessions that have ended, and keeps removing them while serve runs. The returned stop waits for that to end.
func startLogin(ctx context.Context, st loginHistory, stderr io.Writer, logger *slog.Logger) (*auth.Service, func(), error) {
	svc := auth.New(st, auth.Options{})
	password, created, err := svc.EnsureAdmin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare the login: %w", err)
	}
	if created {
		// Not through the logger: it is shown once, and must not be filtered away by --log-level.
		fmt.Fprintf(stderr, "caddy-view: created the user %q with the password: %s\n"+
			"caddy-view: this is the only time it is shown; to set a new one, stop caddy-view and run: caddy-view reset-password\n",
			auth.AdminUsername, password)
	}
	prune := func(ctx context.Context) {
		n, err := svc.Prune(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			logger.Warn("cannot remove the sessions that have ended", "error", err)
		case n > 0:
			logger.Debug("removed sessions that have ended", "count", n)
		}
	}
	prune(ctx)
	stop := background(ctx, func(ctx context.Context) {
		t := time.NewTicker(pruneSessionsEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				prune(ctx)
			}
		}
	})
	return svc, stop, nil
}

// runResetPassword gives a user a new random password, prints it once, and ends the sessions of that user. It works on
// the database in the data directory, so caddy-view may be running or not.
func runResetPassword(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "", "directory of the history database; default: the user data directory")
	username := fs.String("username", auth.AdminUsername, "the user whose password is reset")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	dir := *dataDir
	if dir == "" {
		var err error
		if dir, err = store.DefaultDataDir(); err != nil {
			fmt.Fprintf(stderr, "reset-password: %v\n", err)
			return 1
		}
	}
	file := filepath.Join(dir, databaseFile)
	if _, err := os.Stat(file); err != nil {
		fmt.Fprintf(stderr, "reset-password: there is no database at %s (%v): start caddy-view serve once to create it and its "+
			"first user, or point --data-dir at the directory serve uses\n", file, err)
		return 1
	}
	st, err := store.OpenSQLite(ctx, file)
	if err != nil {
		fmt.Fprintf(stderr, "reset-password: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close() }() // read back nothing here: a failed close has nothing to act on

	password, err := auth.New(st, auth.Options{}).ResetPassword(ctx, *username)
	switch {
	case errors.Is(err, auth.ErrUnknownUser):
		fmt.Fprintf(stderr, "reset-password: there is no user %q in %s\n", *username, file)
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "reset-password: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "new password for %s: %s\nThis is the only time it is shown. Every session of %s was ended.\n",
		*username, password, *username)
	return 0
}
