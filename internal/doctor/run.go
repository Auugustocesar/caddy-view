package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
)

// DefaultManagedFile is the file caddy-view writes, relative to the Caddyfile directory.
const DefaultManagedFile = "conf.d/caddy-view-sites.caddy"

// Fetcher returns the running Caddy configuration.
type Fetcher interface {
	Config(ctx context.Context) (map[string]any, error)
}

// Options selects what Run inspects.
type Options struct {
	// Admin is the Admin API address, as given to the user.
	Admin string
	// CaddyfilePath enables the Caddyfile checks when set.
	CaddyfilePath string
	// ManagedFile is the managed file path relative to the Caddyfile directory. Defaults to DefaultManagedFile.
	ManagedFile string
}

// Run executes every check that applies and returns the results in a stable order.
func Run(ctx context.Context, opts Options, f Fetcher) []Result {
	results := []Result{CheckAdminTransport(opts.Admin)}

	cfg, err := f.Config(ctx)
	switch {
	case err == nil:
		results = append(results,
			Result{ID: "admin-reachable", Status: StatusOK, Message: "the Admin API returned the running configuration"},
			CheckMetrics(Config(cfg)),
			CheckAccessLog(Config(cfg)),
		)
	case errors.Is(err, caddy.ErrUnreachable):
		results = append(results, Result{
			ID:      "admin-reachable",
			Status:  StatusFail,
			Message: fmt.Sprintf("cannot reach the Admin API at %s", opts.Admin),
			Hint:    "Check that Caddy is running and that --admin points to its admin address or socket.",
		})
	default:
		results = append(results, Result{
			ID:      "admin-reachable",
			Status:  StatusFail,
			Message: fmt.Sprintf("the Admin API request failed: %v", err),
			Hint:    "Check the Caddy logs and that --admin points to the Caddy Admin API.",
		})
	}

	if opts.CaddyfilePath != "" {
		results = append(results, checkCaddyfile(opts)...)
	}
	return results
}

func checkCaddyfile(opts Options) []Result {
	data, err := os.ReadFile(opts.CaddyfilePath)
	if err != nil {
		return []Result{{
			ID:      "caddyfile",
			Status:  StatusFail,
			Message: fmt.Sprintf("cannot read %s: %v", opts.CaddyfilePath, err),
			Hint:    "Check the path, and that the file is mounted and readable by the user running caddy-view.",
		}}
	}
	managed := opts.ManagedFile
	if managed == "" {
		managed = DefaultManagedFile
	}
	return []Result{
		{ID: "caddyfile", Status: StatusOK, Message: fmt.Sprintf("read %s", opts.CaddyfilePath)},
		CheckImport(string(data), managed),
	}
}

// Format writes results as a human-readable report followed by a summary line.
func Format(w io.Writer, results []Result) {
	var ok, warn, fail int
	for _, r := range results {
		var label string
		switch r.Status {
		case StatusOK:
			label, ok = "[ ok ]", ok+1
		case StatusWarn:
			label, warn = "[warn]", warn+1
		default:
			label, fail = "[FAIL]", fail+1
		}
		fmt.Fprintf(w, "%s %s: %s\n", label, r.ID, r.Message)
		if r.Hint != "" {
			fmt.Fprintf(w, "       hint: %s\n", r.Hint)
		}
	}
	fmt.Fprintf(w, "\n%d ok, %s, %s\n", ok, plural(warn, "warning"), plural(fail, "failure"))
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ExitCode is 1 when any check failed and 0 otherwise. Warnings do not fail the run.
func ExitCode(results []Result) int {
	for _, r := range results {
		if r.Status == StatusFail {
			return 1
		}
	}
	return 0
}
