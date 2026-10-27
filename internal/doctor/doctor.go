// Package doctor inspects a Caddy setup and reports what caddy-view needs but is missing.
package doctor

import (
	"fmt"
	"net"
	"path"
	"sort"
	"strings"
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
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CheckMetrics reports whether every HTTP server has metrics enabled.
func CheckMetrics(cfg Config) Result {
	const id = "metrics"
	const hint = "Add `metrics` to the servers global option: `{ servers { metrics } }`."
	servers := httpServers(cfg)
	if len(servers) == 0 {
		return Result{ID: id, Status: StatusWarn, Message: "no HTTP servers are configured, so there are no metrics to read", Hint: hint}
	}
	var missing []string
	for name, srv := range servers {
		if m, ok := srv.(map[string]any); !ok || m["metrics"] == nil {
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
		if m, ok := srv.(map[string]any); ok && m["logs"] != nil {
			enabled = true
			break
		}
	}
	if !enabled {
		return Result{ID: id, Status: StatusWarn, Message: "access logging is not enabled on any HTTP server", Hint: hint}
	}
	var fileLoggers, jsonFileLoggers int
	for _, l := range loggers(cfg) {
		if dig(l, "writer", "output") != "file" {
			continue
		}
		fileLoggers++
		if dig(l, "encoder", "format") == "json" {
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

// CheckImport reports whether the Caddyfile imports the file caddy-view manages.
func CheckImport(caddyfile, target string) Result {
	const id = "import"
	for _, line := range strings.Split(caddyfile, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "import" {
			continue
		}
		arg := strings.TrimPrefix(strings.Trim(fields[1], "\"`"), "./")
		if ok, err := path.Match(arg, target); err == nil && ok {
			return Result{ID: id, Status: StatusOK, Message: fmt.Sprintf("the Caddyfile imports %s", target)}
		}
	}
	return Result{
		ID:      id,
		Status:  StatusWarn,
		Message: fmt.Sprintf("the Caddyfile does not import %s", target),
		Hint:    "Add `import conf.d/*.caddy` to the Caddyfile so sites created in caddy-view are loaded.",
	}
}

func httpServers(cfg Config) map[string]any {
	servers, _ := dig(map[string]any(cfg), "apps", "http", "servers").(map[string]any)
	return servers
}

func loggers(cfg Config) []any {
	logs, _ := dig(map[string]any(cfg), "logging", "logs").(map[string]any)
	out := make([]any, 0, len(logs))
	for _, l := range logs {
		out = append(out, l)
	}
	return out
}

// dig walks nested JSON objects and returns nil when any step is missing.
func dig(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}
