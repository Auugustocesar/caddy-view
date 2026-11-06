package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/cfgpath"
)

// socketTimeout is how long CheckAdminSocket waits for a socket to accept a connection.
const socketTimeout = 2 * time.Second

// CheckAdminSocket reports whether the unix socket of the Admin API exists and may be used by this user. It returns a
// zero Result, which Run leaves out, for an address that is not a unix socket.
func CheckAdminSocket(addr string) Result {
	const id = "admin-socket"
	path, ok := caddy.UnixPath(addr)
	if !ok || path == "" {
		return Result{}
	}
	conn, err := net.DialTimeout("unix", path, socketTimeout)
	if err == nil {
		_ = conn.Close() // the connection was only a probe
		return Result{ID: id, Status: StatusOK, Message: fmt.Sprintf("the Admin API socket %s accepts connections", path)}
	}
	r := Result{ID: id, Status: StatusFail}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.Message = fmt.Sprintf("there is no socket at %s", path)
		r.Hint = "Check that Caddy is running, that its global options say `admin unix//run/caddy/admin.sock` (with your path), " +
			"and that the directory is a volume shared with caddy-view."
	case errors.Is(err, fs.ErrPermission):
		r.Message = fmt.Sprintf("permission denied to use the socket %s", path)
		r.Hint = "Give the user running caddy-view write access to the socket: its group or mode, or `admin unix//path|0660` in Caddy."
	case errors.Is(err, syscall.ECONNREFUSED):
		r.Message = fmt.Sprintf("the socket %s exists but nothing is listening on it", path)
		r.Hint = "It is probably left over from a Caddy that stopped. Start Caddy, or remove the file."
	default:
		r.Message = fmt.Sprintf("cannot connect to the socket %s: %v", path, err)
		r.Hint = "Check that Caddy is running and that the path is the one in its `admin` option."
	}
	return r
}

// CheckAccessLogReadable reports whether caddy-view can read the access log file.
func CheckAccessLogReadable(path string) Result {
	const id = "access-log-readable"
	const hint = "Make the file readable by the user running caddy-view, and in Docker mount its directory into caddy-view at " +
		"the same path; or pass the path where caddy-view sees it with --access-log."
	f, err := os.Open(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Result{ID: id, Status: StatusWarn, Message: fmt.Sprintf("the access log %s does not exist here (yet)", path),
			Hint: "Caddy creates it with the first request it logs. " + hint}
	case errors.Is(err, fs.ErrPermission):
		return Result{ID: id, Status: StatusFail, Message: fmt.Sprintf("permission denied reading the access log %s", path), Hint: hint}
	case err != nil:
		return Result{ID: id, Status: StatusFail, Message: fmt.Sprintf("cannot read the access log %s: %v", path, err), Hint: hint}
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.IsDir() {
		return Result{ID: id, Status: StatusWarn, Message: fmt.Sprintf("%s is a directory, not a log file", path), Hint: hint}
	}
	return Result{ID: id, Status: StatusOK, Message: fmt.Sprintf("the access log %s is readable", path)}
}

// CheckCaddyfileWritable reports whether caddy-view could write the Caddyfile: the file itself, and its directory, since
// a change is written to a new file that replaces the old one. It is a warning, not a failure: a read-only caddy-view
// does not need it.
func CheckCaddyfileWritable(path string) Result {
	const id = "caddyfile-writable"
	const hint = "Needed only for --allow-edit. Give the user running caddy-view write access, and in Docker mount the " +
		"directory of the Caddyfile, not the file alone."
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return Result{ID: id, Status: StatusWarn, Message: fmt.Sprintf("caddy-view cannot write the Caddyfile %s: %v", path, err), Hint: hint}
	}
	f.Close()
	dir := filepath.Dir(path)
	probe, err := os.CreateTemp(dir, ".caddy-view-doctor-*")
	if err != nil {
		return Result{ID: id, Status: StatusWarn, Message: fmt.Sprintf("caddy-view cannot create files in the directory %s: %v", dir, err), Hint: hint}
	}
	probe.Close()
	_ = os.Remove(probe.Name()) // a leftover empty file would be noise, and a failure to remove it changes nothing here
	return Result{ID: id, Status: StatusOK, Message: fmt.Sprintf("caddy-view can write the Caddyfile and its directory %s", dir)}
}

// accessLogFile returns the file of the first JSON file logger of the configuration, by logger name, or "" when there
// is none. It is the file serve follows when it is not given one.
func accessLogFile(cfg Config) string {
	logs := cfgpath.Map(cfgpath.Get(map[string]any(cfg), "logging", "logs"))
	names := make([]string, 0, len(logs))
	for name := range logs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		l := logs[name]
		if cfgpath.Get(l, "writer", "output") != "file" || cfgpath.Get(l, "encoder", "format") != "json" {
			continue
		}
		if file, ok := cfgpath.Get(l, "writer", "filename").(string); ok && file != "" {
			return file
		}
	}
	return ""
}
