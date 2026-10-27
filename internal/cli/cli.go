// Package cli implements the caddy-view command line.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/doctor"
)

const usage = `Usage: caddy-view <command> [flags]

Commands:
  doctor   Check that Caddy is set up for caddy-view
  help     Show this help
`

// Run executes the command line and returns the process exit code:
// 0 success, 1 a check failed, 2 usage error.
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
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	admin := fs.String("admin", "http://localhost:2019", "Caddy Admin API address (unix:///path or http://host:port)")
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
