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
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/configmgr"
	"github.com/Auugustocesar/caddy-view/internal/doctor"
	"github.com/Auugustocesar/caddy-view/internal/ingest"
	"github.com/Auugustocesar/caddy-view/internal/instancelock"
	"github.com/Auugustocesar/caddy-view/internal/retain"
	"github.com/Auugustocesar/caddy-view/internal/scrape"
	"github.com/Auugustocesar/caddy-view/internal/server"
	"github.com/Auugustocesar/caddy-view/internal/store"
	"github.com/Auugustocesar/caddy-view/internal/web"
)

const usage = `Usage: caddy-view <command> [flags]

Commands:
  serve    Run the read-only web UI and API
  doctor   Check that Caddy is set up for caddy-view
  help     Show this help
`

const (
	defaultAdmin = "http://localhost:2019"
	databaseFile = "caddy-view.db"
)

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

// history is the store behind the UI: ingestion and the metrics scraper write to it, retention prunes it, the API
// reads from it and the Caddyfile manager keeps its revisions in it.
type history interface {
	ingest.Store
	scrape.Store
	retain.Store
	server.History
	configmgr.RevisionStore
	Revisions(ctx context.Context, q store.RevisionQuery) ([]store.Revision, error)
	io.Closer
}

// checkEditFlags refuses an --allow-edit that could not be done safely. The messages name the flags.
func checkEditFlags(allowEdit bool, caddyfile string, noPersist bool) error {
	if !allowEdit {
		return nil
	}
	switch {
	case caddyfile == "":
		return errors.New("invalid --allow-edit: it needs --caddyfile, the file to edit")
	case noPersist:
		return errors.New("invalid --allow-edit: it cannot be used with --no-persist, because the revisions are the " +
			"rollback history and must survive a restart")
	}
	if _, err := os.ReadFile(caddyfile); err != nil {
		return fmt.Errorf("invalid --caddyfile: %w", err)
	}
	return nil
}

// editor serves the Caddyfile routes: the manager changes the file, the history keeps and lists the revisions.
type editor struct {
	*configmgr.Manager
	revisions interface {
		Revision(ctx context.Context, number int64) (store.Revision, error)
		Revisions(ctx context.Context, q store.RevisionQuery) ([]store.Revision, error)
	}
}

func (e editor) Revision(ctx context.Context, number int64) (store.Revision, error) {
	return e.revisions.Revision(ctx, number)
}

func (e editor) Revisions(ctx context.Context, q store.RevisionQuery) ([]store.Revision, error) {
	return e.revisions.Revisions(ctx, q)
}

// newEditor builds the editor for --allow-edit and warns about what that allows and about a Caddyfile that was
// changed behind caddy-view's back.
func newEditor(ctx context.Context, client *caddy.Client, hist history, path string, logger *slog.Logger) editor {
	mgr := configmgr.New(client, hist, configmgr.Options{Path: path, Logger: logger})
	logger.Warn("--allow-edit is on: whoever can reach this address can change Caddy's configuration", "caddyfile", path)
	// First resolve what a crash left unfinished, so that what follows compares against the truth.
	if _, err := mgr.Recover(ctx); err != nil {
		logger.Warn("cannot resolve the revisions a crash left unfinished", "caddyfile", path, "error", err)
	}
	if drift, err := mgr.Reconcile(ctx); err != nil {
		logger.Warn("cannot compare the Caddyfile with the revision history", "caddyfile", path, "error", err)
	} else {
		if drift.Drifted {
			logger.Warn("the Caddyfile differs from the newest applied revision, so it was edited outside caddy-view",
				"caddyfile", path, "revision", drift.Revision)
		}
		if drift.Running == configmgr.RunDiffers {
			logger.Warn("Caddy is running something other than the Caddyfile on disk: it was reloaded from elsewhere, "+
				"or the file was changed without a reload", "caddyfile", path)
		}
	}
	return editor{Manager: mgr, revisions: hist}
}

// caddyfileLock takes the lock that lets one caddy-view at a time edit a Caddyfile. The file is next to the Caddyfile,
// since that directory is the one an apply already has to write in.
func caddyfileLock(ctx context.Context, caddyfile string) (*instancelock.Lock, error) {
	path := filepath.Join(filepath.Dir(caddyfile), ".caddy-view.lock")
	l, err := instancelock.Acquire(ctx, path)
	switch {
	case errors.Is(err, instancelock.ErrBusy):
		return nil, fmt.Errorf("another caddy-view is already editing the Caddyfile %s (lock %s)", caddyfile, path)
	case err != nil:
		return nil, fmt.Errorf("cannot lock the Caddyfile %s for editing: %w", caddyfile, err)
	}
	return l, nil
}

// openHistory opens the store that keeps the history: in memory with --no-persist, otherwise the SQLite database in
// the data directory. The directory is only touched when persisting.
func openHistory(ctx context.Context, dataDir string, noPersist bool) (h history, file string, err error) {
	if noPersist {
		return store.NewMemory(), "", nil
	}
	if dataDir == "" {
		dir, err := store.DefaultDataDir()
		if err != nil {
			return nil, "", err
		}
		dataDir = dir
	}
	file = filepath.Join(dataDir, databaseFile)
	s, err := store.OpenSQLite(ctx, file)
	if err != nil {
		return nil, "", fmt.Errorf("open history in %s: %w", dataDir, err)
	}
	return s, file, nil
}

func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	// The background workers log to stderr while this function writes to it too.
	stderr = &syncWriter{w: stderr}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	admin := fs.String("admin", defaultAdmin, "Caddy Admin API address (unix:///path or http://host:port)")
	listen := fs.String("listen", "127.0.0.1:8484", "address to listen on; loopback only until the UI has authentication")
	showSecrets := fs.Bool("show-secrets", false, "do not redact passwords, tokens and other secrets in /api/config")
	logLevel := fs.String("log-level", "info", "log verbosity: debug, info, warn or error")
	accessLog := fs.String("access-log", "", "JSON access log file to stream; default: found from the Caddy configuration")
	dataDir := fs.String("data-dir", "", "directory for the history database; default: the user data directory")
	noPersist := fs.Bool("no-persist", false, "keep the history in memory only, nothing is written to disk")
	retainLogs := fs.String("retain-logs", "14d", "how long log entries, metric samples and minute rollups are kept, such as 36h or 14d; 0 keeps them forever")
	retainRollups := fs.String("retain-rollups", "90d", "how long hour rollups are kept, such as 90d; 0 keeps them forever")
	maxDBSize := fs.String("max-db-size", "1GiB", "most the history may hold, such as 512MiB or 1GiB; above it the oldest raw data is deleted; 0 turns the cap off")
	storeQuery := fs.Bool("store-query", false, "keep query strings in the stored request paths (they often carry tokens)")
	caddyfile := fs.String("caddyfile", "", "path to the Caddyfile caddy-view may edit; used with --allow-edit")
	allowEdit := fs.Bool("allow-edit", false, "let the UI and API read and change the Caddyfile; needs --caddyfile and the history database")
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

	policy, err := retentionPolicy(*retainLogs, *retainRollups, *maxDBSize)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := checkEditFlags(*allowEdit, *caddyfile, *noPersist); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *allowEdit {
		lock, err := caddyfileLock(ctx, *caddyfile)
		if err != nil {
			fmt.Fprintf(stderr, "serve: %v\n", err)
			return 1
		}
		defer lock.Release() // the error of giving up a lock that the system frees at exit anyway is not worth a message
	}

	hist, dbFile, err := openHistory(ctx, *dataDir, *noPersist)
	if err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 1
	}
	defer hist.Close()

	if *showSecrets {
		fmt.Fprintln(stderr, "warning: --show-secrets is on, /api/config returns secrets in clear text")
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	opts := server.Options{
		Endpoint:      client.Endpoint(),
		ShowSecrets:   *showSecrets,
		Logger:        logger,
		AccessLogPath: *accessLog,
		UI:            web.Handler(),
		History:       hist,
		Database:      databaseInfo{store: hist, file: dbFile},
		Retention:     &server.Retention{Raw: policy.Raw, Rollups: policy.Rollups, MaxBytes: policy.MaxBytes},
	}
	ingestion := &ingestState{path: *accessLog}
	scraper := scrape.New(client, hist, scrape.Options{Logger: logger})
	opts.Ingest, opts.Scrape = ingestion, scraper
	if *allowEdit {
		opts.Editor = newEditor(ctx, client, hist, *caddyfile, logger)
		opts.CaddyfilePath = *caddyfile
	}
	handler := server.New(client, opts)
	// Registered after the store's Close, so it runs first: ingestion must stop before the store goes away.
	stopIngest := startIngest(ctx, ingestion, hist, client, *accessLog, *storeQuery, logger)
	defer stopIngest()
	stopScrape := startScrape(ctx, scraper)
	defer stopScrape()
	stopRetain := background(ctx, func(ctx context.Context) {
		// Run only returns nil, after the context is cancelled.
		_ = retain.New(hist, retain.Options{Policy: policy, Logger: logger}).Run(ctx)
	})
	defer stopRetain()

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

// discoverEvery is how often serve looks again for an access log while Caddy has none configured or cannot be reached.
const discoverEvery = 30 * time.Second

// background runs fn in its own goroutine and returns a function that cancels its context and waits for it to end.
func background(ctx context.Context, fn func(ctx context.Context)) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// startIngest copies the access log into the history in the background and returns a function that stops it and
// waits for it to finish. The file is the one given with --access-log, otherwise the first JSON file log of the
// running Caddy configuration, which is looked up again until one appears.
func startIngest(ctx context.Context, state *ingestState, h ingest.Store, client *caddy.Client, accessLog string, keepQuery bool, logger *slog.Logger) (stop func()) {
	return background(ctx, func(ctx context.Context) {
		path := accessLog
		for path == "" {
			path = discoverAccessLog(ctx, client, logger)
			if path != "" {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(discoverEvery):
			}
		}
		logger.Info("ingesting the access log into the history", "path", path)
		ingester := ingest.New(h, ingest.Options{Path: path, KeepQuery: keepQuery, Logger: logger})
		state.found(ingester)
		// Run only returns nil, after the context is cancelled.
		_ = ingester.Run(ctx)
	})
}

// ingestState is what /api/system reports about ingestion. The ingester only exists once the access log is known,
// which can take a while when Caddy has none configured yet, so until then it reports that it is waiting.
type ingestState struct {
	path     string // the --access-log given, or empty while it is still to be found
	ingester atomic.Pointer[ingest.Ingester]
}

func (s *ingestState) found(i *ingest.Ingester) { s.ingester.Store(i) }

func (s *ingestState) Status() ingest.Status {
	if i := s.ingester.Load(); i != nil {
		return i.Status()
	}
	return ingest.Status{State: ingest.StateWaiting, Path: s.path}
}

// databaseInfo tells /api/system about the history store.
type databaseInfo struct {
	store history
	file  string // empty for the memory store
}

func (d databaseInfo) Info(ctx context.Context) (server.DatabaseInfo, error) {
	size, err := d.store.Size(ctx)
	if err != nil {
		return server.DatabaseInfo{}, fmt.Errorf("size of the history: %w", err)
	}
	info := server.DatabaseInfo{Kind: "memory", File: d.file, SizeBytes: size}
	if d.file == "" {
		return info, nil
	}
	info.Kind = "sqlite"
	if v, ok := d.store.(interface {
		SchemaVersion(ctx context.Context) (int, error)
	}); ok {
		if info.SchemaVersion, err = v.SchemaVersion(ctx); err != nil {
			return server.DatabaseInfo{}, fmt.Errorf("schema version of the history: %w", err)
		}
	}
	return info, nil
}

// startScrape copies Caddy's metrics into the history in the background and returns a function that stops it and
// waits for it to finish.
func startScrape(ctx context.Context, scraper *scrape.Scraper) (stop func()) {
	return background(ctx, func(ctx context.Context) {
		// Run only returns nil, after the context is cancelled.
		_ = scraper.Run(ctx)
	})
}

// discoverAccessLog returns the first JSON file log of the running configuration, or "" when there is none.
func discoverAccessLog(ctx context.Context, client *caddy.Client, logger *slog.Logger) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cfg, err := client.Config(ctx)
	if err != nil {
		logger.Warn("cannot read the Caddy configuration to find the access log, will retry", "error", err)
		return ""
	}
	files := caddy.AccessLogFiles(cfg)
	if len(files) == 0 {
		logger.Info("no JSON access log file is configured in Caddy, so no history is collected; see caddy-view doctor")
		return ""
	}
	return files[0]
}

// retentionPolicy reads the retention flags. The error names the flag that is wrong.
func retentionPolicy(retainLogs, retainRollups, maxDBSize string) (retain.Policy, error) {
	var p retain.Policy
	var err error
	if p.Raw, err = parseDays(retainLogs); err != nil {
		return retain.Policy{}, fmt.Errorf("invalid --retain-logs: %w", err)
	}
	if p.Rollups, err = parseDays(retainRollups); err != nil {
		return retain.Policy{}, fmt.Errorf("invalid --retain-rollups: %w", err)
	}
	if p.MaxBytes, err = parseSize(maxDBSize); err != nil {
		return retain.Policy{}, fmt.Errorf("invalid --max-db-size: %w", err)
	}
	return p, nil
}

// syncWriter makes a writer safe to share between goroutines, so that the background workers' logs and serve's own
// messages cannot interleave or race on a writer, such as a buffer, that is not safe for it.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
