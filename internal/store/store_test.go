package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Auugustocesar/caddy-view/internal/store"
	"github.com/Auugustocesar/caddy-view/internal/store/storetest"
)

var base = time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

func TestMemoryStoreMeetsTheContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Store { return store.NewMemory() })
}

func TestSQLiteStoreMeetsTheContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Store {
		s, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "caddy-view.db"))
		if err != nil {
			t.Fatalf("OpenSQLite: %v", err)
		}
		return s
	})
}

func TestMemoryStoreMeetsTheMetricsContract(t *testing.T) {
	storetest.RunMetrics(t, func(t *testing.T) storetest.MetricStore { return store.NewMemory() })
}

func TestSQLiteStoreMeetsTheMetricsContract(t *testing.T) {
	storetest.RunMetrics(t, func(t *testing.T) storetest.MetricStore {
		s, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "caddy-view.db"))
		if err != nil {
			t.Fatalf("OpenSQLite: %v", err)
		}
		return s
	})
}

func TestSQLiteKeepsMetricsAcrossAReopen(t *testing.T) {
	path := dbPath(t)
	ctx := context.Background()
	first := open(t, path)
	err := first.AppendMetrics(ctx, []store.MetricSample{{Name: "caddy_http_requests_total", Labels: `{server="srv0"}`, Time: base, Value: 3, Raw: 30}})
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	last, err := open(t, path).LastMetrics(ctx)
	if err != nil || last[`caddy_http_requests_total{server="srv0"}`].Raw != 30 {
		t.Errorf("LastMetrics after a reopen = %v, %v, want the stored raw value 30", last, err)
	}
}

func TestMemoryStoreMeetsTheRollupContract(t *testing.T) {
	storetest.RunRollups(t, func(t *testing.T) storetest.RollupStore { return store.NewMemory() })
}

func TestSQLiteStoreMeetsTheRollupContract(t *testing.T) {
	storetest.RunRollups(t, func(t *testing.T) storetest.RollupStore { return openForContract(t) })
}

func TestMemoryStoreMeetsTheHostContract(t *testing.T) {
	storetest.RunHosts(t, func(t *testing.T) storetest.HostStore { return store.NewMemory() })
}

func TestSQLiteStoreMeetsTheHostContract(t *testing.T) {
	storetest.RunHosts(t, func(t *testing.T) storetest.HostStore { return openForContract(t) })
}

func TestMemoryStoreMeetsTheRetentionContract(t *testing.T) {
	storetest.RunPrune(t, func(t *testing.T) storetest.PruneStore { return store.NewMemory() })
}

func TestSQLiteStoreMeetsTheRetentionContract(t *testing.T) {
	storetest.RunPrune(t, func(t *testing.T) storetest.PruneStore { return openForContract(t) })
}

func openForContract(t *testing.T) *store.SQLite {
	t.Helper()
	s, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "caddy-view.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	return s
}

func TestSQLiteKeepsRollupsAcrossAReopen(t *testing.T) {
	path := dbPath(t)
	ctx := context.Background()
	first := open(t, path)
	err := first.AppendLog(ctx, store.LogBatch{Entries: []store.LogEntry{{Time: base, Host: "h", Status: 200, Duration: time.Millisecond, Bytes: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	got, err := open(t, path).QueryTraffic(ctx, store.TrafficQuery{Resolution: store.PerHour})
	if err != nil || len(got) != 1 || got[0].Count != 1 || got[0].Bytes != 5 {
		t.Errorf("rollups after a reopen = %+v, %v, want the stored request", got, err)
	}
}

func TestLatencyUpperBounds(t *testing.T) {
	bounds := store.LatencyUpperBounds()
	if len(bounds) != store.LatencyBuckets-1 {
		t.Fatalf("got %d bounds, want %d: the last bucket is open", len(bounds), store.LatencyBuckets-1)
	}
	for i := 1; i < len(bounds); i++ {
		if bounds[i] <= bounds[i-1] {
			t.Errorf("bounds %v are not strictly increasing", bounds)
		}
	}
	bounds[0] = time.Hour // the caller must not be able to change the real bounds
	if again := store.LatencyUpperBounds(); again[0] == time.Hour {
		t.Error("changing the returned slice changed the bounds")
	}
}

func TestLatencyQuantile(t *testing.T) {
	bounds := store.LatencyUpperBounds()
	hist := func(pairs ...int) (h [store.LatencyBuckets]int64) {
		for i := 0; i < len(pairs); i += 2 {
			h[pairs[i]] = int64(pairs[i+1])
		}
		return h
	}
	tests := []struct {
		name string
		h    [store.LatencyBuckets]int64
		q    float64
		want time.Duration
	}{
		{"an empty histogram", hist(), 0.95, 0},
		{"everything in the first bucket", hist(0, 50), 0.95, bounds[0]},
		{"the median of two buckets", hist(1, 90, 4, 10), 0.5, bounds[1]},
		{"p90 is still in the big bucket", hist(1, 90, 4, 10), 0.9, bounds[1]},
		{"p95 reaches the small bucket", hist(1, 90, 4, 10), 0.95, bounds[4]},
		{"the open bucket gives the last bound", hist(store.LatencyBuckets-1, 3), 0.95, bounds[len(bounds)-1]},
		{"a quantile of zero is the first request", hist(2, 1, 5, 1), 0, bounds[2]},
		{"a quantile above one is the slowest", hist(2, 1, 5, 1), 7, bounds[5]},
		{"a negative quantile is the first request", hist(2, 1, 5, 1), -1, bounds[2]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := store.LatencyQuantile(tt.h, tt.q); got != tt.want {
				t.Errorf("LatencyQuantile = %v, want %v", got, tt.want)
			}
		})
	}
}

func dbPath(t *testing.T) string {
	t.Helper()
	// The directory does not exist yet: OpenSQLite creates it.
	return filepath.Join(t.TempDir(), "data", "caddy-view.db")
}

func open(t *testing.T, path string) *store.SQLite {
	t.Helper()
	s, err := store.OpenSQLite(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenSQLite(%s): %v", path, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSQLiteKeepsDataAcrossAReopen(t *testing.T) {
	path := dbPath(t)
	ctx := context.Background()
	cursor := store.Cursor{Path: "/var/log/caddy/access.log", FileID: "7:42", Fingerprint: "abc", Offset: 777, Gaps: 1}

	first := open(t, path)
	err := first.AppendLog(ctx, store.LogBatch{
		Entries: []store.LogEntry{{Time: base, Host: "app.example.com", Method: "GET", Path: "/kept", Status: 200}},
		Cursor:  cursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := open(t, path)
	got, err := second.QueryLogs(ctx, store.LogQuery{})
	if err != nil || len(got) != 1 || got[0].Path != "/kept" {
		t.Fatalf("entries after a reopen = %+v, %v, want the stored one", got, err)
	}
	c, ok, err := second.LoadCursor(ctx, cursor.Path)
	if err != nil || !ok || c != cursor {
		t.Errorf("cursor after a reopen = %+v (found %v, err %v), want %+v", c, ok, err, cursor)
	}

	// IDs keep growing after a restart, so paging by ID stays valid.
	if err := second.AppendLog(ctx, store.LogBatch{Entries: []store.LogEntry{{Time: base, Host: "h", Path: "/next", Status: 200}}}); err != nil {
		t.Fatal(err)
	}
	all, _ := second.QueryLogs(ctx, store.LogQuery{})
	if len(all) != 2 || all[0].ID <= all[1].ID {
		t.Errorf("entries = %+v, want the new one first with a higher ID", all)
	}
}

func TestSQLiteCreatesPrivateFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not have Unix permission bits")
	}
	path := dbPath(t)
	open(t, path)

	for _, tt := range []struct {
		path string
		want os.FileMode
	}{{filepath.Dir(path), 0o700}, {path, 0o600}} {
		info, err := os.Stat(tt.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != tt.want {
			t.Errorf("%s has mode %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestSQLiteSchemaVersion(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	got, err := s.SchemaVersion(context.Background())
	if err != nil || got != store.LatestSchemaVersion {
		t.Fatalf("SchemaVersion = %d, %v, want %d", got, err, store.LatestSchemaVersion)
	}
	if store.LatestSchemaVersion < 1 {
		t.Fatalf("LatestSchemaVersion = %d, want at least the first migration", store.LatestSchemaVersion)
	}

	// Opening again must not apply anything twice.
	s.Close()
	again := open(t, path)
	if got, _ := again.SchemaVersion(context.Background()); got != store.LatestSchemaVersion {
		t.Errorf("SchemaVersion after a reopen = %d, want %d", got, store.LatestSchemaVersion)
	}
}

func TestSQLiteUsesWAL(t *testing.T) {
	path := dbPath(t)
	open(t, path).Close()

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var mode string
	if err := raw.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q, %v, want wal", mode, err)
	}
}

func TestSQLiteRefusesADatabaseFromANewerVersion(t *testing.T) {
	path := dbPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 9999"); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	_, err = store.OpenSQLite(context.Background(), path)
	if !errors.Is(err, store.ErrSchemaTooNew) {
		t.Errorf("OpenSQLite of a newer database: error = %v, want ErrSchemaTooNew", err)
	}
}

func TestSQLiteRejectsAFileThatIsNotADatabase(t *testing.T) {
	path := dbPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("this is not a database, just some text that is long enough to look like a page"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenSQLite(context.Background(), path); err == nil {
		t.Error("OpenSQLite of a text file succeeded, want an error")
	}
}

func TestSQLiteOpenHonoursACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.OpenSQLite(ctx, dbPath(t)); !errors.Is(err, context.Canceled) {
		t.Errorf("OpenSQLite with a cancelled context: error = %v, want context.Canceled", err)
	}
}

func TestSQLiteHandlesAreSafeToShareBetweenStores(t *testing.T) {
	// Ingestion writes while the API reads, both from the same process and the same file.
	path := dbPath(t)
	writer := open(t, path)
	reader := open(t, path)
	ctx := context.Background()

	if err := writer.AppendLog(ctx, store.LogBatch{Entries: []store.LogEntry{{Time: base, Host: "h", Path: "/shared", Status: 200}}}); err != nil {
		t.Fatal(err)
	}
	got, err := reader.QueryLogs(ctx, store.LogQuery{})
	if err != nil || len(got) != 1 {
		t.Errorf("second handle sees %+v, %v, want the entry written by the first", got, err)
	}
}

func TestMemoryStoreMeetsTheRevisionContract(t *testing.T) {
	storetest.RunRevisions(t, func(t *testing.T) storetest.RevisionStore { return store.NewMemory() })
}

func TestSQLiteStoreMeetsTheRevisionContract(t *testing.T) {
	storetest.RunRevisions(t, func(t *testing.T) storetest.RevisionStore { return openForContract(t) })
}

func TestSQLiteKeepsRevisionsAcrossAReopen(t *testing.T) {
	path := dbPath(t)
	ctx := context.Background()
	first := open(t, path)
	added, err := first.AddRevision(ctx, store.Revision{
		Time: base, Trigger: store.TriggerManualEdit, Status: store.StatusPending, Content: "a", SHA256: strings.Repeat("ab", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.UpdateRevision(ctx, added.Number, store.StatusApplied, ""); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second := open(t, path)
	got, ok, err := second.LatestApplied(ctx)
	if err != nil || !ok || got.Number != added.Number || got.Content != "a" || !got.Time.Equal(base) {
		t.Fatalf("LatestApplied after a reopen = %+v, %v, %v, want the stored revision", got, ok, err)
	}
	next, err := second.AddRevision(ctx, store.Revision{
		Time: base, Trigger: store.TriggerManualEdit, Status: store.StatusPending, Content: "b", SHA256: strings.Repeat("ab", 32),
	})
	if err != nil || next.Number != added.Number+1 {
		t.Errorf("next number after a reopen = %d, %v, want %d", next.Number, err, added.Number+1)
	}
}

func TestSQLiteAddsRevisionsToAVersion3Database(t *testing.T) {
	path := dbPath(t)
	ctx := context.Background()
	open(t, path).Close()

	// Turn the database back into what the previous release left: no revisions, schema version 3.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"config_revisions", "sites"} { // sites came with a later migration, which runs again
		if _, err := raw.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.ExecContext(ctx, "PRAGMA user_version = 3"); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s := open(t, path)
	if v, err := s.SchemaVersion(ctx); err != nil || v != store.LatestSchemaVersion {
		t.Fatalf("SchemaVersion = %d, %v, want %d", v, err, store.LatestSchemaVersion)
	}
	if _, err := s.AddRevision(ctx, store.Revision{
		Time: base, Trigger: store.TriggerManualEdit, Status: store.StatusPending, Content: "a", SHA256: strings.Repeat("ab", 32),
	}); err != nil {
		t.Errorf("AddRevision after the upgrade: %v", err)
	}
}

func TestMemoryStoreMeetsTheSitesContract(t *testing.T) {
	storetest.RunSites(t, func(t *testing.T) storetest.SiteStore { return store.NewMemory() })
}

func TestSQLiteStoreMeetsTheSitesContract(t *testing.T) {
	storetest.RunSites(t, func(t *testing.T) storetest.SiteStore { return openForContract(t) })
}

func TestSQLiteKeepsSitesAcrossAReopen(t *testing.T) {
	path := dbPath(t)
	ctx := context.Background()
	first := open(t, path)
	site := store.Site{
		ID: "a", Hostname: "a.example.com", Kind: store.SiteProxy, Upstreams: []string{"10.0.0.1:8080", "https://10.0.0.2"},
		SkipTLSVerify: true, TLS: store.TLSInternal, AccessLog: true, Enabled: true, CreatedAt: base, UpdatedAt: base,
	}
	if err := first.ReplaceSites(ctx, []store.Site{site}); err != nil {
		t.Fatal(err)
	}
	first.Close()

	got, err := open(t, path).Sites(ctx)
	if err != nil || len(got) != 1 || got[0].Hostname != "a.example.com" || len(got[0].Upstreams) != 2 || !got[0].SkipTLSVerify {
		t.Errorf("Sites after a reopen = %+v, %v, want the stored site", got, err)
	}
}

func TestSQLiteAddsSitesToAVersion4Database(t *testing.T) {
	path := dbPath(t)
	ctx := context.Background()
	open(t, path).Close()

	// Turn the database back into what the previous release left: no sites table, no managed columns, version 4.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "DROP TABLE IF EXISTS sites"); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"sites_content", "sites_sha256", "sites_snapshot"} {
		var has int
		if err := raw.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('config_revisions') WHERE name = ?", column).Scan(&has); err != nil {
			t.Fatal(err)
		}
		if has > 0 {
			if _, err := raw.ExecContext(ctx, "ALTER TABLE config_revisions DROP COLUMN "+column); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := raw.ExecContext(ctx, "PRAGMA user_version = 4"); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s := open(t, path)
	if v, err := s.SchemaVersion(ctx); err != nil || v != store.LatestSchemaVersion || v < 5 {
		t.Fatalf("SchemaVersion = %d, %v, want the latest, which is at least 5", v, err)
	}
	if err := s.ReplaceSites(ctx, []store.Site{{ID: "a", Hostname: "a.example.com", Kind: store.SiteProxy, Upstreams: []string{"10.0.0.1"}, TLS: store.TLSAutomatic, Enabled: true, CreatedAt: base, UpdatedAt: base}}); err != nil {
		t.Errorf("ReplaceSites after the upgrade: %v", err)
	}
}

func TestAnUpgradeFromTheSchemaWithoutPasswordsKeepsTheSites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caddy-view.db")
	ctx := context.Background()
	first := open(t, path)
	site := store.Site{ID: "a", Hostname: "a.example.com", Kind: store.SiteProxy, Upstreams: []string{"10.0.0.1"}, TLS: store.TLSAutomatic, Enabled: true, CreatedAt: base, UpdatedAt: base}
	if err := first.ReplaceSites(ctx, []store.Site{site}); err != nil {
		t.Fatal(err)
	}
	first.Close()

	// Turn the database back into what the previous release left: a sites table without the password columns.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"basic_auth_user", "basic_auth_hash"} {
		var has int
		if err := raw.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('sites') WHERE name = ?", column).Scan(&has); err != nil {
			t.Fatal(err)
		}
		if has > 0 {
			if _, err := raw.ExecContext(ctx, "ALTER TABLE sites DROP COLUMN "+column); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := raw.ExecContext(ctx, "PRAGMA user_version = 5"); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s := open(t, path)
	if v, err := s.SchemaVersion(ctx); err != nil || v != store.LatestSchemaVersion || v < 6 {
		t.Fatalf("SchemaVersion = %d, %v, want the latest, which is at least 6", v, err)
	}
	got, err := s.Sites(ctx)
	if err != nil || len(got) != 1 || got[0].Hostname != "a.example.com" || got[0].BasicAuthUser != "" || got[0].BasicAuthHash != "" {
		t.Fatalf("Sites after the upgrade = %+v, %v, want the site, without a password", got, err)
	}
	protected := site
	protected.BasicAuthUser, protected.BasicAuthHash = "bob", "$2a$04$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234"
	if err := s.ReplaceSites(ctx, []store.Site{protected}); err != nil {
		t.Errorf("ReplaceSites with a password after the upgrade: %v", err)
	}
}
