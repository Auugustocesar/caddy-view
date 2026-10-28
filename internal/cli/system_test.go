package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// systemOf asks /api/system of a running serve.
func systemOf(t *testing.T, base string) map[string]any {
	t.Helper()
	status, got := api{t, base}.call(http.MethodGet, "/api/system", "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/system = %d %v, want 200", status, got)
	}
	return got
}

func sub(t *testing.T, m map[string]any, name string) map[string]any {
	t.Helper()
	s, ok := m[name].(map[string]any)
	if !ok {
		t.Fatalf("no %q section in %v", name, m)
	}
	return s
}

// eventually asks until check is happy; the parts of serve that fill /api/system work in the background.
func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !check() {
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestServeSystemInMemory(t *testing.T) {
	admin := newEditAdmin(t) // /metrics is a 404 there: Caddy without `metrics`; its access log file does not exist
	base, stop, _, _ := startServe(t, "--admin", admin.URL)
	defer stop()

	got := systemOf(t, base)
	if c := sub(t, got, "caddy"); c["reachable"] != true {
		t.Errorf("caddy = %v, want reachable", c)
	}
	if d := sub(t, got, "database"); d["kind"] != "memory" || d["file"] != nil {
		t.Errorf("database = %v, want memory with no file", d)
	}
	if e := sub(t, got, "editing"); e["enabled"] != false {
		t.Errorf("editing = %v, want disabled", e)
	}
	r := sub(t, got, "retention")
	want := map[string]any{"rawSeconds": float64(14 * 86400), "rollupsSeconds": float64(90 * 86400), "maxBytes": float64(1 << 30)}
	for k, v := range want {
		if r[k] != v {
			t.Errorf("retention[%q] = %v, want the default %v", k, r[k], v)
		}
	}

	eventually(t, "the scraper to learn that Caddy has no metrics", func() bool {
		return sub(t, systemOf(t, base), "scraper")["state"] == "disabled"
	})
	eventually(t, "ingestion to find the access log in Caddy's configuration", func() bool {
		i := sub(t, systemOf(t, base), "ingestion")
		return i["file"] == "/var/log/caddy/access.log" && i["state"] == "waiting" // the file is not there
	})
}

func TestServeSystemShowsTheRetentionFlags(t *testing.T) {
	admin := newEditAdmin(t)
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--retain-logs", "36h", "--retain-rollups", "10d", "--max-db-size", "512MiB")
	defer stop()
	r := sub(t, systemOf(t, base), "retention")
	want := map[string]any{"rawSeconds": float64(36 * 3600), "rollupsSeconds": float64(10 * 86400), "maxBytes": float64(512 << 20)}
	for k, v := range want {
		if r[k] != v {
			t.Errorf("retention[%q] = %v, want %v", k, r[k], v)
		}
	}
}

func TestServeSystemShowsTheDatabaseFile(t *testing.T) {
	admin := newEditAdmin(t)
	dataDir := t.TempDir()
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--data-dir", dataDir)
	defer stop()

	d := sub(t, systemOf(t, base), "database")
	if d["kind"] != "sqlite" || d["schemaVersion"] != float64(store.LatestSchemaVersion) {
		t.Errorf("database = %v, want sqlite at schema version %d", d, store.LatestSchemaVersion)
	}
	if file, _ := d["file"].(string); filepath.Base(file) != "caddy-view.db" || !strings.HasPrefix(file, filepath.Clean(dataDir)) {
		t.Errorf("database file = %q, want caddy-view.db in %s", d["file"], dataDir)
	}
	if size, _ := d["sizeBytes"].(float64); size <= 0 {
		t.Errorf("database size = %v, want the pages the migrations created", d["sizeBytes"])
	}
}

func TestServeSystemShowsIngestionOfTheAccessLog(t *testing.T) {
	admin := newEditAdmin(t)
	logFile := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(logFile, []byte(accessLine()), 0o600); err != nil {
		t.Fatal(err)
	}
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--access-log", logFile)
	defer stop()

	eventually(t, "the access log to be ingested", func() bool {
		i := sub(t, systemOf(t, base), "ingestion")
		return i["state"] == "following" && i["newestEntry"] != nil
	})
	i := sub(t, systemOf(t, base), "ingestion")
	info, err := os.Stat(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if i["file"] != logFile || i["offset"] != float64(info.Size()) || i["skippedLines"] != float64(0) {
		t.Errorf("ingestion = %v, want %s read up to its end (%d bytes), nothing skipped", i, logFile, info.Size())
	}
}

func TestServeSystemShowsEditing(t *testing.T) {
	admin := newEditAdmin(t)
	file := writeCaddyfile(t, siteOne)
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir())
	defer stop()

	e := sub(t, systemOf(t, base), "editing")
	if e["enabled"] != true || e["caddyfile"] != file {
		t.Fatalf("editing = %v, want enabled on %s", e, file)
	}
	// The fake Caddy runs its own configuration, not this file, and nothing was applied yet.
	d := sub(t, e, "drift")
	if d["known"] != false || d["drifted"] != false || d["running"] != "differs" {
		t.Errorf("drift = %v, want unknown history, not drifted, and Caddy running something else", d)
	}
}
