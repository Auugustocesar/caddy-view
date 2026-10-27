// Command caddy-view is a lightweight web UI for Caddy.
package main

import (
	"context"
	"os"

	"github.com/Auugustocesar/caddy-view/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
