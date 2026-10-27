package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// safeBuffer is an io.Writer that can be read while the command is still writing to it.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var listeningLine = regexp.MustCompile(`http://127\.0\.0\.1:\d+`)

// startServe runs `caddy-view serve` in the background on a free loopback port.
func startServe(t *testing.T, extraArgs ...string) (baseURL string, stop func() int, stdout, stderr *safeBuffer) {
	t.Helper()
	stdout, stderr = &safeBuffer{}, &safeBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	args := append([]string{"serve", "--listen", "127.0.0.1:0"}, withoutHistory(extraArgs)...)
	done := make(chan int, 1)
	go func() { done <- Run(ctx, args, stdout, stderr) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := listeningLine.FindString(stdout.String()); m != "" {
			stop = func() int {
				cancel()
				select {
				case code := <-done:
					return code
				case <-time.After(10 * time.Second):
					t.Fatal("serve did not stop after the context was cancelled")
					return -1
				}
			}
			return m, stop, stdout, stderr
		}
		select {
		case code := <-done:
			t.Fatalf("serve exited with %d before listening\nstdout: %s\nstderr: %s", code, stdout, stderr)
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("serve never reported its address\nstdout: %s\nstderr: %s", stdout, stderr)
	return
}

func TestUsageListsServe(t *testing.T) {
	_, stdout, _ := run(t, "help")
	if !strings.Contains(stdout, "serve") {
		t.Errorf("usage %q does not list the serve command", stdout)
	}
}

func TestServeRefusesNonLoopback(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:0", ":0", "192.168.50.10:8484"} {
		t.Run(listen, func(t *testing.T) {
			code, stdout, stderr := run(t, "serve", "--listen", listen)
			if code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if !strings.Contains(stderr, "loopback") {
				t.Errorf("stderr %q does not explain that only loopback is allowed", stderr)
			}
			if listeningLine.MatchString(stdout) || strings.Contains(stdout, "listening") {
				t.Errorf("stdout %q suggests the server started", stdout)
			}
		})
	}
}

func TestServeFlagErrors(t *testing.T) {
	t.Run("unknown flag", func(t *testing.T) {
		code, _, stderr := run(t, "serve", "--nope")
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if !strings.Contains(stderr, "nope") {
			t.Errorf("stderr %q does not name the unknown flag", stderr)
		}
	})
	t.Run("invalid admin address", func(t *testing.T) {
		code, _, stderr := run(t, "serve", "--admin", "ftp://example.com", "--listen", "127.0.0.1:0")
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if !strings.Contains(stderr, "ftp") {
			t.Errorf("stderr %q does not explain the address problem", stderr)
		}
	})
}

func TestServeServesTheAPIAndStopsCleanly(t *testing.T) {
	admin := fakeAdmin(t, goodConfig)
	base, stop, stdout, _ := startServe(t, "--admin", admin.URL)

	if !strings.Contains(stdout.String(), admin.URL) {
		t.Errorf("startup output %q does not mention the Admin API %s", stdout.String(), admin.URL)
	}

	resp, err := http.Get(base + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	var status struct {
		Caddy struct {
			Reachable bool   `json:"reachable"`
			Endpoint  string `json:"endpoint"`
		} `json:"caddy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.Caddy.Reachable || status.Caddy.Endpoint != admin.URL {
		t.Errorf("status = %+v, want reachable at %s", status.Caddy, admin.URL)
	}

	if code := stop(); code != 0 {
		t.Errorf("exit code after a clean shutdown = %d, want 0", code)
	}
}

func TestServeStartsEvenWhenCaddyIsDown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	down := l.Addr().String()
	l.Close()

	base, stop, _, _ := startServe(t, "--admin", down)
	defer stop()

	resp, err := http.Get(base + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	var status struct {
		Caddy struct {
			Reachable bool   `json:"reachable"`
			Error     string `json:"error"`
		} `json:"caddy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if resp.StatusCode != http.StatusOK || status.Caddy.Reachable || status.Caddy.Error == "" {
		t.Errorf("status %d, caddy %+v, want 200 with an unreachable Caddy and an error message", resp.StatusCode, status.Caddy)
	}
}

func TestServeReportsAddressInUse(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	code, _, stderr := run(t, "serve", "--listen", busy.Addr().String())
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, busy.Addr().String()) {
		t.Errorf("stderr %q does not name the address that is in use", stderr)
	}
}

const secretAdminConfig = `{"apps":{"http":{"servers":{"srv0":{"listen":[":443"]}}}},"provider":{"api_token":"supersecret123"},"public":"visible"}`

func getConfigBody(t *testing.T, base string) string {
	t.Helper()
	resp, err := http.Get(base + "/api/config")
	if err != nil {
		t.Fatalf("GET /api/config: %v", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestServeRedactsSecretsByDefault(t *testing.T) {
	admin := fakeAdmin(t, secretAdminConfig)
	base, stop, _, _ := startServe(t, "--admin", admin.URL)
	defer stop()

	body := getConfigBody(t, base)
	if strings.Contains(body, "supersecret123") {
		t.Errorf("the API leaks a secret by default: %s", body)
	}
	if !strings.Contains(body, "[redacted]") {
		t.Errorf("body = %s, want the secret redacted", body)
	}
}

func TestServeShowSecretsFlag(t *testing.T) {
	admin := fakeAdmin(t, secretAdminConfig)
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--show-secrets")
	defer stop()

	if body := getConfigBody(t, base); !strings.Contains(body, "supersecret123") {
		t.Errorf("--show-secrets did not return the raw configuration: %s", body)
	}
}

func TestServeRejectsAnInvalidLogLevel(t *testing.T) {
	code, _, stderr := run(t, "serve", "--listen", "127.0.0.1:0", "--log-level", "nope")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "invalid --log-level") || !strings.Contains(stderr, "nope") {
		t.Errorf("stderr %q does not name the flag and the rejected value", stderr)
	}
}

func TestServeLogsOperationalProblemsToStderr(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	down := l.Addr().String()
	l.Close()

	base, stop, _, stderr := startServe(t, "--admin", down)
	defer stop()

	resp, err := http.Get(base + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, want := range []string{"level=WARN", "caddy request failed"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr %q does not contain %q", stderr.String(), want)
		}
	}
}

func TestServeLogLevelDebugShowsRequests(t *testing.T) {
	admin := fakeAdmin(t, goodConfig)
	base, stop, _, stderr := startServe(t, "--admin", admin.URL, "--log-level", "debug")
	defer stop()

	resp, err := http.Get(base + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(stderr.String(), "msg=request") {
		t.Errorf("stderr %q does not show the request at debug level", stderr.String())
	}
}

func TestServeServesTheUI(t *testing.T) {
	admin := fakeAdmin(t, goodConfig)
	base, stop, _, _ := startServe(t, "--admin", admin.URL)
	defer stop()

	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(body.String(), `<div id="app">`) {
		t.Errorf("GET / = %d %q, want the UI page", resp.StatusCode, body.String())
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

// withoutHistory keeps tests from writing a database into the real user data directory: unless a test chooses where
// the data goes, the history is kept in memory.
func withoutHistory(args []string) []string {
	for _, a := range args {
		if a == "--no-persist" || a == "--data-dir" {
			return args
		}
	}
	return append([]string{"--no-persist"}, args...)
}

func TestServeStoresHistoryInTheDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	admin := fakeAdmin(t, goodConfig)
	_, stop, _, _ := startServe(t, "--admin", admin.URL, "--data-dir", dir)
	defer stop()

	info, err := os.Stat(filepath.Join(dir, "caddy-view.db"))
	if err != nil {
		t.Fatalf("the database was not created in the data directory: %v", err)
	}
	if info.IsDir() {
		t.Error("caddy-view.db is a directory")
	}
}

func TestServeNoPersistWritesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	admin := fakeAdmin(t, goodConfig)
	_, stop, _, _ := startServe(t, "--admin", admin.URL, "--data-dir", dir, "--no-persist")
	defer stop()

	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("--no-persist still created %s (stat error: %v)", dir, err)
	}
}

func TestServeCannotUseADataDirThatIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := run(t, "serve", "--listen", "127.0.0.1:0", "--data-dir", file)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, file) {
		t.Errorf("stderr %q does not name the data directory %s", stderr, file)
	}
	if strings.Contains(stdout, "listening") {
		t.Errorf("stdout %q suggests the server started without its store", stdout)
	}
}

func TestServeReopensTheSameHistory(t *testing.T) {
	dir := t.TempDir()
	admin := fakeAdmin(t, goodConfig)
	for range 2 {
		_, stop, _, _ := startServe(t, "--admin", admin.URL, "--data-dir", dir)
		if code := stop(); code != 0 {
			t.Fatalf("exit code = %d, want 0 (a second start must accept the existing database)", code)
		}
	}
}

const accessLine = `{"level":"info","ts":1761566400.123,"logger":"http.log.access.log0","msg":"handled request",` +
	`"request":{"remote_ip":"10.0.0.7","client_ip":"10.0.0.7","method":"GET","host":"app.example.com","uri":"/ingested?token=secret"},` +
	`"duration":0.01,"size":10,"status":200}` + "\n"

func TestServeIngestsTheAccessLogIntoTheHistory(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(logFile, []byte(accessLine), 0o600); err != nil {
		t.Fatal(err)
	}
	admin := fakeAdmin(t, goodConfig)
	_, stop, _, _ := startServe(t, "--admin", admin.URL, "--data-dir", dir, "--access-log", logFile)

	// A second handle on the same database is what the API will be: it sees what ingestion committed.
	reader, err := store.OpenSQLite(context.Background(), filepath.Join(dir, "caddy-view.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	var got []store.LogEntry
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for len(got) == 0 {
		select {
		case <-tick.C:
			got, err = reader.QueryLogs(context.Background(), store.LogQuery{})
			if err != nil {
				t.Fatal(err)
			}
		case <-deadline:
			t.Fatal("the access log was not ingested")
		}
	}
	if got[0].Host != "app.example.com" || got[0].Path != "/ingested" {
		t.Errorf("stored entry = %+v, want host app.example.com and the path without its query string", got[0])
	}
	if code := stop(); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestServeKeepsTheQueryStringOnRequest(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(logFile, []byte(accessLine), 0o600); err != nil {
		t.Fatal(err)
	}
	admin := fakeAdmin(t, goodConfig)
	_, stop, _, _ := startServe(t, "--admin", admin.URL, "--data-dir", dir, "--access-log", logFile, "--store-query")
	defer stop()

	reader, err := store.OpenSQLite(context.Background(), filepath.Join(dir, "caddy-view.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			got, _ := reader.QueryLogs(context.Background(), store.LogQuery{})
			if len(got) == 0 {
				continue
			}
			if got[0].Path != "/ingested?token=secret" {
				t.Errorf("path = %q, want the query string kept", got[0].Path)
			}
			return
		case <-deadline:
			t.Fatal("the access log was not ingested")
		}
	}
}

func TestServeScrapesTheMetricsIntoTheHistory(t *testing.T) {
	dir := t.TempDir()
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config/":
			io.WriteString(w, goodConfig)
		case "/metrics":
			io.WriteString(w, "# TYPE caddy_http_requests_in_flight gauge\n"+`caddy_http_requests_in_flight{handler="subroute",server="srv0"} 3`+"\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(admin.Close)
	_, stop, _, _ := startServe(t, "--admin", admin.URL, "--data-dir", dir)

	reader, err := store.OpenSQLite(context.Background(), filepath.Join(dir, "caddy-view.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	var got []store.MetricSample
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for len(got) == 0 {
		select {
		case <-tick.C:
			got, err = reader.QueryMetrics(context.Background(), store.MetricQuery{Name: "caddy_http_requests_in_flight"})
			if err != nil {
				t.Fatal(err)
			}
		case <-deadline:
			t.Fatal("the metrics were not scraped")
		}
	}
	if got[0].Labels != `{server="srv0"}` || got[0].Value != 3 {
		t.Errorf("stored sample = %+v, want value 3 for server srv0", got[0])
	}
	if code := stop(); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestServeRejectsBadRetentionFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--retain-logs", "forever"},
		{"--retain-logs", "-1d"},
		{"--retain-rollups", "1.5d"},
		{"--max-db-size", "lots"},
		{"--max-db-size", "-1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, stdout, stderr := run(t, append([]string{"serve", "--listen", "127.0.0.1:0", "--no-persist"}, args...)...)
			if code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if !strings.Contains(stderr, args[0]) {
				t.Errorf("stderr %q does not name the flag %s", stderr, args[0])
			}
			if strings.Contains(stdout, "listening") {
				t.Errorf("serve started listening despite a bad flag: %s", stdout)
			}
		})
	}
}

func TestServePrunesDataPastItsRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "caddy-view.db")
	ctx := context.Background()

	// A database from an earlier run, holding one very old entry and one recent one.
	seed, err := store.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	err = seed.AppendLog(ctx, store.LogBatch{Entries: []store.LogEntry{
		{Time: old, Host: "old.example.com", Path: "/old", Status: 200},
		{Time: time.Now(), Host: "new.example.com", Path: "/new", Status: 200},
	}})
	if err != nil {
		t.Fatal(err)
	}
	seed.Close()

	admin := fakeAdmin(t, goodConfig)
	_, stop, _, _ := startServe(t, "--admin", admin.URL, "--data-dir", dir, "--retain-logs", "14d")
	defer stop()

	reader, err := store.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			got, err := reader.QueryLogs(ctx, store.LogQuery{})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 1 && got[0].Path == "/new" {
				return
			}
		case <-deadline:
			t.Fatal("the entry past the retention was not deleted, or the recent one was")
		}
	}
}
