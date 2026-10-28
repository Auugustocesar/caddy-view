// Package doctor inspects a Caddy setup and reports what caddy-view needs but is missing.
package doctor

import (
	"fmt"
	"net"
	"path"
	"path/filepath"
	"sort"
	"strings"

	cf "github.com/Auugustocesar/caddy-view/internal/caddyfile"
	"github.com/Auugustocesar/caddy-view/internal/cfgpath"
	"github.com/Auugustocesar/caddy-view/internal/netutil"
)

// Status is the outcome of a single check.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Result is the outcome of a single check.
type Result struct {
	ID      string
	Status  Status
	Message string
	// Hint tells the user how to fix a warning or failure.
	Hint string
}

// Config is Caddy's running configuration as returned by GET /config/.
type Config map[string]any

// CheckAdminTransport reports how caddy-view reaches the Admin API.
func CheckAdminTransport(addr string) Result {
	const id = "admin-transport"
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return Result{
			ID:      id,
			Status:  StatusFail,
			Message: "no Admin API address configured",
			Hint:    "Pass --admin unix:///run/caddy/admin.sock or the address of the Admin API.",
		}
	}
	if strings.HasPrefix(addr, "unix") {
		return Result{ID: id, Status: StatusOK, Message: "Admin API is reached through a unix socket"}
	}
	if isLoopback(addr) {
		return Result{ID: id, Status: StatusOK, Message: "Admin API is reached over loopback TCP"}
	}
	return Result{
		ID:      id,
		Status:  StatusWarn,
		Message: fmt.Sprintf("Admin API at %s is reachable beyond loopback and has no authentication", addr),
		Hint:    "Prefer a unix socket: set `admin unix//run/caddy/admin.sock` in the Caddyfile global options.",
	}
}

func isLoopback(addr string) bool {
	addr = strings.TrimPrefix(strings.TrimPrefix(addr, "http://"), "https://")
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	return netutil.IsLoopbackHost(host)
}

// CheckMetrics reports whether metrics are enabled. Caddy 2.10 and later adapt the global `metrics` option (and the
// deprecated `servers { metrics }`) to a setting of the whole HTTP app; older versions, and the JSON config, set it
// per server. Either counts.
func CheckMetrics(cfg Config) Result {
	const id = "metrics"
	const hint = "Add `metrics` to the global options: `{ metrics }`."
	servers := httpServers(cfg)
	if len(servers) == 0 {
		return Result{ID: id, Status: StatusWarn, Message: "no HTTP servers are configured, so there are no metrics to read", Hint: hint}
	}
	appWide := cfgpath.Get(map[string]any(cfg), "apps", "http", "metrics") != nil
	var missing []string
	for name, srv := range servers {
		if !appWide && cfgpath.Get(srv, "metrics") == nil {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return Result{ID: id, Status: StatusOK, Message: "metrics are enabled on every HTTP server"}
	}
	sort.Strings(missing)
	return Result{
		ID:      id,
		Status:  StatusWarn,
		Message: fmt.Sprintf("metrics are not enabled on: %s", strings.Join(missing, ", ")),
		Hint:    hint,
	}
}

// CheckAccessLog reports whether access logs are written to a JSON file.
//
// v1 simplification: any logger that writes JSON to a file counts; it is not mapped to a specific server.
func CheckAccessLog(cfg Config) Result {
	const id = "access-log"
	const hint = "Add `log { output file /var/log/caddy/access.log; format json }` to your sites."
	var enabled bool
	for _, srv := range httpServers(cfg) {
		if cfgpath.Get(srv, "logs") != nil {
			enabled = true
			break
		}
	}
	if !enabled {
		return Result{ID: id, Status: StatusWarn, Message: "access logging is not enabled on any HTTP server", Hint: hint}
	}
	var fileLoggers, jsonFileLoggers int
	for _, l := range loggers(cfg) {
		if cfgpath.Get(l, "writer", "output") != "file" {
			continue
		}
		fileLoggers++
		if cfgpath.Get(l, "encoder", "format") == "json" {
			jsonFileLoggers++
		}
	}
	switch {
	case jsonFileLoggers > 0:
		return Result{ID: id, Status: StatusOK, Message: "access logs are written to a JSON file"}
	case fileLoggers > 0:
		return Result{ID: id, Status: StatusWarn, Message: "access logs are written to a file but not as JSON", Hint: hint}
	default:
		return Result{ID: id, Status: StatusWarn, Message: "access logs are not written to a file, so there is nothing to read", Hint: hint}
	}
}

// CheckRelativeImports warns about the imports of files by a relative path, which caddy-view cannot apply faithfully:
// Caddy resolves them against its working directory when it is given text through the Admin API.
func CheckRelativeImports(text, caddyfilePath string) Result {
	const id = "relative-import"
	imports := cf.RelativeImports(text)
	if len(imports) == 0 {
		return Result{ID: id, Status: StatusOK, Message: "the Caddyfile imports no file by a relative path"}
	}
	first := imports[0]
	message := fmt.Sprintf("line %d imports %s by a relative path", first.Line, first.Pattern)
	if len(imports) > 1 {
		message += fmt.Sprintf(" (and %d more)", len(imports)-1)
	}
	full := path.Join(filepath.ToSlash(filepath.Dir(caddyfilePath)), first.Pattern)
	return Result{
		ID:      id,
		Status:  StatusWarn,
		Message: message,
		Hint: fmt.Sprintf("Write the path in full, such as `import %s`. caddy-view sends changes to Caddy as text, and Caddy "+
			"resolves a relative path from its own working directory, so an apply would drop what is imported.", full),
	}
}

// CheckImport reports whether the Caddyfile, which lives in dir, imports the file caddy-view manages. The import may
// be relative to dir or an absolute path.
func CheckImport(caddyfile, dir, target string) Result {
	const id = "import"
	absolute := path.Join(filepath.ToSlash(dir), target)
	for _, line := range strings.Split(caddyfile, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "import" {
			continue
		}
		arg := strings.TrimPrefix(strings.Trim(fields[1], "\"`"), "./")
		if ok, err := path.Match(arg, target); err == nil && ok {
			return Result{ID: id, Status: StatusOK, Message: fmt.Sprintf("the Caddyfile imports %s", target)}
		}
		if ok, err := path.Match(arg, absolute); err == nil && ok {
			return Result{ID: id, Status: StatusOK, Message: fmt.Sprintf("the Caddyfile imports %s", target)}
		}
	}
	return Result{
		ID:      id,
		Status:  StatusWarn,
		Message: fmt.Sprintf("the Caddyfile does not import %s", target),
		Hint: fmt.Sprintf("Add `import %s/*.caddy` to the Caddyfile so sites created in caddy-view are loaded. "+
			"Write the path in full: caddy-view cannot apply a relative import faithfully.",
			path.Join(filepath.ToSlash(dir), path.Dir(target))),
	}
}

func httpServers(cfg Config) map[string]any {
	return cfgpath.Map(cfgpath.Get(map[string]any(cfg), "apps", "http", "servers"))
}

func loggers(cfg Config) []any {
	logs := cfgpath.Map(cfgpath.Get(map[string]any(cfg), "logging", "logs"))
	out := make([]any, 0, len(logs))
	for _, l := range logs {
		out = append(out, l)
	}
	return out
}
