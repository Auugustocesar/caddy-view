// Package cli implements the caddy-view command line.
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
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/doctor"
	"github.com/Auugustocesar/caddy-view/internal/server"
	"github.com/Auugustocesar/caddy-view/internal/web"
)

const usage = `Usage: caddy-view <command> [flags]

Commands:
  serve    Run the read-only web UI and API
  doctor   Check that Caddy is set up for caddy-view
  help     Show this help
`

const defaultAdmin = "http://localhost:2019"

// Run executes the command line and returns the process exit code:
// 0 success, 1 a check failed or the server could not run, 2 usage error.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "doctor":
		return runDoctor(ctx, args[1:], stdout, stderr)
	case "serve":
		return runServe(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	admin := fs.String("admin", defaultAdmin, "Caddy Admin API address (unix:///path or http://host:port)")
	caddyfile := fs.String("caddyfile", "", "path to the Caddyfile, enables the Caddyfile checks")
	managed := fs.String("managed-file", doctor.DefaultManagedFile, "file managed by caddy-view, relative to the Caddyfile directory")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	client, err := caddy.New(*admin)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --admin: %v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	results := doctor.Run(ctx, doctor.Options{Admin: *admin, CaddyfilePath: *caddyfile, ManagedFile: *managed}, client)
	doctor.Format(stdout, results)
	return doctor.ExitCode(results)
}

func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	admin := fs.String("admin", defaultAdmin, "Caddy Admin API address (unix:///path or http://host:port)")
	listen := fs.String("listen", "127.0.0.1:8484", "address to listen on; loopback only until the UI has authentication")
	showSecrets := fs.Bool("show-secrets", false, "do not redact passwords, tokens and other secrets in /api/config")
	logLevel := fs.String("log-level", "info", "log verbosity: debug, info, warn or error")
	accessLog := fs.String("access-log", "", "JSON access log file to stream; default: found from the Caddy configuration")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if err := server.ValidateListen(*listen); err != nil {
		fmt.Fprintf(stderr, "invalid --listen: %v\n", err)
		return 2
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		fmt.Fprintf(stderr, "invalid --log-level %q: use debug, info, warn or error\n", *logLevel)
		return 2
	}
	if info, err := os.Stat(*accessLog); *accessLog != "" && err == nil && info.IsDir() {
		fmt.Fprintf(stderr, "invalid --access-log: %s is a directory, not a file\n", *accessLog)
		return 2
	}
	client, err := caddy.New(*admin)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --admin: %v\n", err)
		return 2
	}

	if *showSecrets {
		fmt.Fprintln(stderr, "warning: --show-secrets is on, /api/config returns secrets in clear text")
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	handler := server.New(client, server.Options{
		Endpoint:      client.Endpoint(),
		ShowSecrets:   *showSecrets,
		Logger:        logger,
		AccessLogPath: *accessLog,
		UI:            web.Handler(),
	})
	onListen := func(addr net.Addr) {
		fmt.Fprintf(stdout, "caddy-view listening on http://%s (Admin API: %s)\n", addr, client.Endpoint())
	}
	if err := server.Serve(ctx, *listen, handler, onListen); err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "caddy-view stopped")
	return 0
}
