// Command caddy-view is a lightweight web UI for Caddy.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Auugustocesar/caddy-view/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
