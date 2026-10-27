package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
