package doctor

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// shortDir makes a temporary directory with a short path, since a unix socket path is limited to about 100 bytes and the
// name of t.TempDir() contains the name of the test.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// listenUnix serves a unix socket at path; keep leaves the socket file behind when the listener closes.
func listenUnix(t *testing.T, path string, keep bool) *net.UnixListener {
	t.Helper()
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Skipf("unix sockets are not available here: %v", err)
	}
	l.SetUnlinkOnClose(!keep)
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return l
}

// skipIfPermissionsAreNotEnforced skips a test that depends on being refused access: root, and Windows, are not.
func skipIfPermissionsAreNotEnforced(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions are not enforced for this user or system")
	}
}

func TestCheckAdminSocket(t *testing.T) {
	t.Run("a TCP address has no socket to check", func(t *testing.T) {
		for _, addr := range []string{"localhost:2019", "http://localhost:2019", ""} {
			if r := CheckAdminSocket(addr); r.ID != "" {
				t.Errorf("CheckAdminSocket(%q) = %+v, want no result", addr, r)
			}
		}
	})
	t.Run("a socket that answers", func(t *testing.T) {
		path := filepath.Join(shortDir(t), "a.sock")
		listenUnix(t, path, false)
		for _, addr := range []string{"unix://" + path, "unix/" + path} {
			r := CheckAdminSocket(addr)
			if r.ID != "admin-socket" || r.Status != StatusOK {
				t.Errorf("CheckAdminSocket(%q) = %+v, want ok", addr, r)
			}
		}
	})
	t.Run("no socket file", func(t *testing.T) {
		path := filepath.Join(shortDir(t), "missing.sock")
		r := CheckAdminSocket("unix://" + path)
		if r.Status != StatusFail || !strings.Contains(r.Message, path) || r.Hint == "" {
			t.Errorf("CheckAdminSocket = %+v, want a failure that names the path and has a hint", r)
		}
	})
	t.Run("a socket file nobody listens on", func(t *testing.T) {
		path := filepath.Join(shortDir(t), "stale.sock")
		l := listenUnix(t, path, true)
		l.Close()
		r := CheckAdminSocket("unix://" + path)
		if r.Status != StatusFail || !strings.Contains(r.Message, path) {
			t.Errorf("CheckAdminSocket = %+v, want a failure: nothing is listening", r)
		}
	})
	t.Run("a socket this user may not use", func(t *testing.T) {
		skipIfPermissionsAreNotEnforced(t)
		path := filepath.Join(shortDir(t), "locked.sock")
		listenUnix(t, path, false)
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatal(err)
		}
		r := CheckAdminSocket("unix://" + path)
		if r.Status != StatusFail || !strings.Contains(strings.ToLower(r.Message+r.Hint), "permission") {
			t.Errorf("CheckAdminSocket = %+v, want a failure that says it is a permission problem", r)
		}
	})
}

func TestCheckAccessLogReadable(t *testing.T) {
	t.Run("readable", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "access.log")
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if r := CheckAccessLogReadable(p); r.ID != "access-log-readable" || r.Status != StatusOK {
			t.Errorf("CheckAccessLogReadable = %+v, want ok", r)
		}
	})
	t.Run("not there yet", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "access.log")
		r := CheckAccessLogReadable(p)
		if r.Status != StatusWarn || !strings.Contains(r.Message, p) || !strings.Contains(r.Hint, "--access-log") {
			t.Errorf("CheckAccessLogReadable = %+v, want a warning that names the file and mentions --access-log", r)
		}
	})
	t.Run("a directory is not a log", func(t *testing.T) {
		r := CheckAccessLogReadable(t.TempDir())
		if r.Status != StatusWarn {
			t.Errorf("CheckAccessLogReadable(dir) = %+v, want a warning", r)
		}
	})
	t.Run("a mode caddy-view cannot read", func(t *testing.T) {
		skipIfPermissionsAreNotEnforced(t)
		p := filepath.Join(t.TempDir(), "access.log")
		if err := os.WriteFile(p, []byte("{}\n"), 0o000); err != nil {
			t.Fatal(err)
		}
		r := CheckAccessLogReadable(p)
		if r.Status != StatusFail || !strings.Contains(strings.ToLower(r.Message+r.Hint), "permission") {
			t.Errorf("CheckAccessLogReadable = %+v, want a failure that says it is a permission problem", r)
		}
	})
}

func TestCheckCaddyfileWritable(t *testing.T) {
	write := func(t *testing.T, dir string) string {
		t.Helper()
		p := filepath.Join(dir, "Caddyfile")
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Run("file and directory writable", func(t *testing.T) {
		dir := t.TempDir()
		if r := CheckCaddyfileWritable(write(t, dir)); r.ID != "caddyfile-writable" || r.Status != StatusOK {
			t.Errorf("CheckCaddyfileWritable = %+v, want ok", r)
		}
		left, _ := os.ReadDir(dir)
		if len(left) != 1 {
			t.Errorf("the directory holds %d entries after the check, want only the Caddyfile: the check must clean up", len(left))
		}
	})
	t.Run("a Caddyfile that may not be written", func(t *testing.T) {
		skipIfPermissionsAreNotEnforced(t)
		p := write(t, t.TempDir())
		if err := os.Chmod(p, 0o400); err != nil {
			t.Fatal(err)
		}
		r := CheckCaddyfileWritable(p)
		if r.Status != StatusWarn || !strings.Contains(r.Message, "Caddyfile") {
			t.Errorf("CheckCaddyfileWritable = %+v, want a warning", r)
		}
	})
	t.Run("a directory that may not be written, which the atomic write needs", func(t *testing.T) {
		skipIfPermissionsAreNotEnforced(t)
		dir := t.TempDir()
		p := write(t, dir)
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o700) })
		r := CheckCaddyfileWritable(p)
		if r.Status != StatusWarn || !strings.Contains(r.Message, "directory") {
			t.Errorf("CheckCaddyfileWritable = %+v, want a warning that names the directory", r)
		}
	})
}

func TestAccessLogFileOfAConfig(t *testing.T) {
	cfg := mustConfig(t, `{"logging":{"logs":{
		"stdout":{"writer":{"output":"stdout"},"encoder":{"format":"json"}},
		"text":{"writer":{"output":"file","filename":"/var/log/text.log"},"encoder":{"format":"console"}},
		"b":{"writer":{"output":"file","filename":"/var/log/b.log"},"encoder":{"format":"json"}},
		"a":{"writer":{"output":"file","filename":"/var/log/a.log"},"encoder":{"format":"json"}}}}}`)
	if got := accessLogFile(cfg); got != "/var/log/a.log" {
		t.Errorf("accessLogFile = %q, want the first JSON file logger by name, /var/log/a.log", got)
	}
	if got := accessLogFile(mustConfig(t, `{}`)); got != "" {
		t.Errorf("accessLogFile of a config without loggers = %q, want empty", got)
	}
}
