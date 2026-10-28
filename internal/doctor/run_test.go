package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
)

type fakeFetcher struct {
	cfg map[string]any
	err error
}

func (f fakeFetcher) Config(context.Context) (map[string]any, error) { return f.cfg, f.err }

const goodConfig = `{
	"apps":{"http":{"servers":{"srv0":{"metrics":{},"logs":{}}}}},
	"logging":{"logs":{"log0":{"writer":{"output":"file","filename":"/var/log/caddy/access.log"},"encoder":{"format":"json"}}}}
}`

func goodFetcher(t *testing.T) fakeFetcher {
	t.Helper()
	return fakeFetcher{cfg: mustConfig(t, goodConfig)}
}

func writeCaddyfile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func ids(results []Result) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.ID)
	}
	return out
}

func byID(t *testing.T, results []Result, id string) Result {
	t.Helper()
	for _, r := range results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no result with ID %q in %v", id, ids(results))
	return Result{}
}

func TestRunAllGood(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	// The import is absolute: a relative one is warned about, because caddy-view cannot apply it faithfully.
	if err := os.WriteFile(path, []byte("import "+filepath.ToSlash(dir)+"/conf.d/*.caddy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{Admin: "unix:///run/caddy/admin.sock", CaddyfilePath: path}
	results := Run(context.Background(), opts, goodFetcher(t))

	want := []string{"admin-transport", "admin-reachable", "metrics", "access-log", "caddyfile", "import", "relative-import"}
	if got := ids(results); !reflect.DeepEqual(got, want) {
		t.Fatalf("result IDs = %v, want %v", got, want)
	}
	for _, r := range results {
		if r.Status != StatusOK {
			t.Errorf("%s: status = %q (%s), want ok", r.ID, r.Status, r.Message)
		}
	}
}

func TestRunWithoutCaddyfilePathSkipsCaddyfileChecks(t *testing.T) {
	results := Run(context.Background(), Options{Admin: "localhost:2019"}, goodFetcher(t))
	want := []string{"admin-transport", "admin-reachable", "metrics", "access-log"}
	if got := ids(results); !reflect.DeepEqual(got, want) {
		t.Fatalf("result IDs = %v, want %v", got, want)
	}
}

func TestRunUnreachableSkipsConfigChecks(t *testing.T) {
	fetcher := fakeFetcher{err: fmt.Errorf("%w: connection refused", caddy.ErrUnreachable)}
	opts := Options{
		Admin:         "localhost:2019",
		CaddyfilePath: writeCaddyfile(t, "import conf.d/*.caddy\n"),
	}
	results := Run(context.Background(), opts, fetcher)

	want := []string{"admin-transport", "admin-reachable", "caddyfile", "import", "relative-import"}
	if got := ids(results); !reflect.DeepEqual(got, want) {
		t.Fatalf("result IDs = %v, want %v (config checks must not run without a config)", got, want)
	}
	r := byID(t, results, "admin-reachable")
	assertStatus(t, r, StatusFail)
	if byID(t, results, "import").Status != StatusOK {
		t.Error("Caddyfile checks should still run when the Admin API is down")
	}
}

func TestRunOtherFetchErrorIsAFailureWithTheCause(t *testing.T) {
	results := Run(context.Background(), Options{Admin: "localhost:2019"}, fakeFetcher{err: errors.New("boom")})
	r := byID(t, results, "admin-reachable")
	assertStatus(t, r, StatusFail)
	if !bytes.Contains([]byte(r.Message), []byte("boom")) {
		t.Errorf("message %q does not carry the cause", r.Message)
	}
}

func TestRunMissingCaddyfile(t *testing.T) {
	opts := Options{
		Admin:         "localhost:2019",
		CaddyfilePath: filepath.Join(t.TempDir(), "does-not-exist"),
	}
	results := Run(context.Background(), opts, goodFetcher(t))
	assertStatus(t, byID(t, results, "caddyfile"), StatusFail)
	for _, r := range results {
		if r.ID == "import" {
			t.Error("import check must not run when the Caddyfile cannot be read")
		}
	}
}

func TestRunManagedFile(t *testing.T) {
	t.Run("custom managed file", func(t *testing.T) {
		opts := Options{
			Admin:         "localhost:2019",
			CaddyfilePath: writeCaddyfile(t, "import sites/*.caddy\n"),
			ManagedFile:   "sites/caddy-view.caddy",
		}
		assertStatus(t, byID(t, Run(context.Background(), opts, goodFetcher(t)), "import"), StatusOK)
	})
	t.Run("default managed file", func(t *testing.T) {
		opts := Options{
			Admin:         "localhost:2019",
			CaddyfilePath: writeCaddyfile(t, "import "+DefaultManagedFile+"\n"),
		}
		assertStatus(t, byID(t, Run(context.Background(), opts, goodFetcher(t)), "import"), StatusOK)
	})
	t.Run("default does not match a different folder", func(t *testing.T) {
		opts := Options{
			Admin:         "localhost:2019",
			CaddyfilePath: writeCaddyfile(t, "import sites/*.caddy\n"),
		}
		assertStatus(t, byID(t, Run(context.Background(), opts, goodFetcher(t)), "import"), StatusWarn)
	})
}

func TestRunWarnsAboutARelativeImport(t *testing.T) {
	opts := Options{Admin: "localhost:2019", CaddyfilePath: writeCaddyfile(t, "import conf.d/*.caddy\n")}
	results := Run(context.Background(), opts, goodFetcher(t))
	// The managed file is imported, which is what the import check looks at; the way it is written is the other check.
	assertStatus(t, byID(t, results, "import"), StatusOK)
	assertStatus(t, byID(t, results, "relative-import"), StatusWarn)
}

func TestRunWarningsComeFromTheChecks(t *testing.T) {
	cfg := mustConfig(t, `{"apps":{"http":{"servers":{"srv0":{}}}}}`)
	results := Run(context.Background(), Options{Admin: "localhost:2019"}, fakeFetcher{cfg: cfg})
	assertStatus(t, byID(t, results, "metrics"), StatusWarn)
	assertStatus(t, byID(t, results, "access-log"), StatusWarn)
}

func TestExitCode(t *testing.T) {
	ok := Result{ID: "a", Status: StatusOK}
	warn := Result{ID: "b", Status: StatusWarn}
	fail := Result{ID: "c", Status: StatusFail}
	tests := []struct {
		name    string
		results []Result
		want    int
	}{
		{"no results", nil, 0},
		{"all ok", []Result{ok, ok}, 0},
		{"warnings do not fail the run", []Result{ok, warn}, 0},
		{"one failure", []Result{ok, warn, fail}, 1},
		{"only failures", []Result{fail}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.results); got != tt.want {
				t.Errorf("ExitCode = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFormat(t *testing.T) {
	t.Run("mixed results", func(t *testing.T) {
		results := []Result{
			{ID: "metrics", Status: StatusOK, Message: "metrics are enabled"},
			{ID: "import", Status: StatusWarn, Message: "no import", Hint: "add it"},
			{ID: "admin-reachable", Status: StatusFail, Message: "unreachable", Hint: "start caddy"},
		}
		var buf bytes.Buffer
		Format(&buf, results)
		want := "[ ok ] metrics: metrics are enabled\n" +
			"[warn] import: no import\n" +
			"       hint: add it\n" +
			"[FAIL] admin-reachable: unreachable\n" +
			"       hint: start caddy\n" +
			"\n" +
			"1 ok, 1 warning, 1 failure\n"
		if got := buf.String(); got != want {
			t.Errorf("Format output:\n%q\nwant:\n%q", got, want)
		}
	})
	t.Run("plural summary", func(t *testing.T) {
		results := []Result{
			{ID: "a", Status: StatusOK, Message: "m"},
			{ID: "b", Status: StatusOK, Message: "m"},
			{ID: "c", Status: StatusWarn, Message: "m", Hint: "h"},
			{ID: "d", Status: StatusWarn, Message: "m", Hint: "h"},
		}
		var buf bytes.Buffer
		Format(&buf, results)
		if want := "2 ok, 2 warnings, 0 failures\n"; !bytes.HasSuffix(buf.Bytes(), []byte(want)) {
			t.Errorf("Format output %q does not end with %q", buf.String(), want)
		}
	})
}
