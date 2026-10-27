package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodConfig = `{
	"apps":{"http":{"servers":{"srv0":{"metrics":{},"logs":{}}}}},
	"logging":{"logs":{"log0":{"writer":{"output":"file","filename":"/var/log/caddy/access.log"},"encoder":{"format":"json"}}}}
}`

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func fakeAdmin(t *testing.T, config string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, config)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUsage(t *testing.T) {
	t.Run("no arguments is a usage error", func(t *testing.T) {
		code, _, stderr := run(t)
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if !strings.Contains(stderr, "doctor") {
			t.Errorf("usage on stderr %q does not list the doctor command", stderr)
		}
	})
	t.Run("help prints usage and succeeds", func(t *testing.T) {
		for _, arg := range []string{"help", "-h", "--help"} {
			code, stdout, _ := run(t, arg)
			if code != 0 {
				t.Errorf("%s: exit code = %d, want 0", arg, code)
			}
			if !strings.Contains(stdout, "doctor") {
				t.Errorf("%s: usage on stdout %q does not list the doctor command", arg, stdout)
			}
		}
	})
	t.Run("unknown command", func(t *testing.T) {
		code, _, stderr := run(t, "frobnicate")
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if !strings.Contains(stderr, "frobnicate") {
			t.Errorf("stderr %q does not name the unknown command", stderr)
		}
	})
}

func TestDoctorFlagErrors(t *testing.T) {
	t.Run("unknown flag", func(t *testing.T) {
		code, _, stderr := run(t, "doctor", "--nope")
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if stderr == "" {
			t.Error("nothing written to stderr")
		}
	})
	t.Run("invalid admin address", func(t *testing.T) {
		code, _, stderr := run(t, "doctor", "--admin", "ftp://example.com")
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if !strings.Contains(stderr, "ftp") {
			t.Errorf("stderr %q does not explain the address problem", stderr)
		}
	})
}

func TestDoctorHealthySetup(t *testing.T) {
	srv := fakeAdmin(t, goodConfig)
	caddyfile := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(caddyfile, []byte("import conf.d/*.caddy\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := run(t, "doctor", "--admin", srv.URL, "--caddyfile", caddyfile)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"[ ok ] admin-reachable", "[ ok ] metrics", "[ ok ] access-log", "[ ok ] import", "6 ok, 0 warnings, 0 failures"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
}

func TestDoctorWarningsStillExitZero(t *testing.T) {
	srv := fakeAdmin(t, `{"apps":{"http":{"servers":{"srv0":{}}}}}`)
	code, stdout, _ := run(t, "doctor", "--admin", srv.URL)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (warnings only)", code)
	}
	if !strings.Contains(stdout, "[warn] metrics") || !strings.Contains(stdout, "hint:") {
		t.Errorf("stdout does not show the metrics warning with a hint:\n%s", stdout)
	}
}

func TestDoctorUnreachableAdmin(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	code, stdout, _ := run(t, "doctor", "--admin", addr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout, "[FAIL] admin-reachable") {
		t.Errorf("stdout does not report the unreachable Admin API:\n%s", stdout)
	}
}

func TestDoctorCustomManagedFile(t *testing.T) {
	srv := fakeAdmin(t, goodConfig)
	caddyfile := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(caddyfile, []byte("import sites/*.caddy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := run(t, "doctor", "--admin", srv.URL, "--caddyfile", caddyfile, "--managed-file", "sites/caddy-view.caddy")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "[ ok ] import") {
		t.Errorf("custom managed file not honored:\n%s", stdout)
	}
}
