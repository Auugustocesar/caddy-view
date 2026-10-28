// Package server is the read-only HTTP API of caddy-view.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/netutil"
	"github.com/Auugustocesar/caddy-view/internal/redact"
)

// Caddy is what the API needs from the Admin API client.
type Caddy interface {
	Config(ctx context.Context) (map[string]any, error)
	Upstreams(ctx context.Context) ([]caddy.Upstream, error)
}

// Options configures the handler.
type Options struct {
	// Endpoint is the Admin API address shown in /api/status.
	Endpoint string
	// ShowSecrets turns off the redaction of secrets in /api/config.
	ShowSecrets bool
	// Logger receives operational logs. A nil Logger discards them.
	Logger *slog.Logger
	// UI serves every path outside /api/. When nil, those paths are a 404.
	UI http.Handler
	// AccessLogPath is the file followed by /api/logs/stream. When empty, the file is discovered from the
	// running Caddy configuration.
	AccessLogPath string
	// History serves /api/logs, /api/traffic and /api/summary. When nil, those routes are a 404.
	History History
	// Sites serves the managed sites routes, which change Caddy. When nil, those routes are a 404.
	Sites SiteManager
	// Editor serves the Caddyfile and revision routes, which change Caddy. When nil, those routes are a 404.
	Editor Editor
	// Ingest, Scrape and Database feed /api/system; each one that is nil leaves its section out.
	Ingest   IngestStatus
	Scrape   ScrapeStatus
	Database Database
	// Retention is reported by /api/system when not nil.
	Retention *Retention
	// CaddyfilePath is the file reported by /api/system under editing.
	CaddyfilePath string
	// Now gives the current time for the history routes. Zero means time.Now.
	Now func() time.Time
	// MaxStreams caps the concurrent log streams. Zero means 8.
	MaxStreams int
	// HeartbeatInterval is how often an idle log stream sends a ping comment. Zero means 15 seconds.
	HeartbeatInterval time.Duration
	// LogPollInterval is how often the log file is checked for new lines. Zero means the tailer's default.
	LogPollInterval time.Duration
}

type api struct {
	caddy Caddy
	opts  Options
	log   *slog.Logger
	// streams holds one token per open log stream.
	streams chan struct{}
}

var errNoAccessLog = errors.New("no access log found: configure a JSON file logger in Caddy or pass --access-log")

// New returns the API handler, only for loopback Host names. It is read-only: it serves GET, and POST only on the
// Caddyfile and revision routes that exist when Options.Editor is set.
func New(c Caddy, opts Options) http.Handler {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	maxStreams := opts.MaxStreams
	if maxStreams <= 0 {
		maxStreams = defaultMaxStreams
	}
	a := &api{caddy: c, opts: opts, log: log, streams: make(chan struct{}, maxStreams)}
	routes := map[string]route{
		"/api/status":      onGet(a.status),
		"/api/config":      onGet(a.config),
		"/api/sites":       onGet(a.sites),
		"/api/logs/stream": onGet(a.logStream),
		"/api/system":      onGet(a.system),
	}
	if opts.History != nil {
		routes["/api/logs"] = onGet(a.logs)
		routes["/api/traffic"] = onGet(a.traffic)
		routes["/api/summary"] = onGet(a.summary)
	}
	if opts.Sites != nil {
		routes["/api/managed-sites"] = onGet(a.sitesList).with(http.MethodPost, a.sitesCreate)
		routes["/api/managed-sites/preview"] = onPost(a.sitesPreview)
		routes["/api/managed-sites/import"] = onPost(a.sitesImport)
	}
	if opts.Editor != nil {
		routes["/api/caddyfile"] = onGet(a.caddyfile)
		routes["/api/caddyfile/validate"] = onPost(a.validate)
		routes["/api/caddyfile/format"] = onPost(a.format)
		routes["/api/caddyfile/apply"] = onPost(a.apply)
		routes["/api/revisions"] = onGet(a.revisions)
	}
	return a.logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			a.log.Warn("request rejected: host not allowed", "host", r.Host, "method", r.Method, "path", r.URL.Path)
			setAPIHeaders(w)
			writeError(w, http.StatusForbidden, "host not allowed")
			return
		}
		if !isAPIPath(r.URL.Path) && opts.UI != nil {
			opts.UI.ServeHTTP(w, r)
			return
		}

		setAPIHeaders(w)
		rt, ok := routes[r.URL.Path]
		if !ok && opts.Editor != nil {
			rt, ok = a.revisionRoute(r.URL.Path)
		}
		if !ok && opts.Sites != nil {
			rt, ok = a.managedRoute(r.URL.Path)
		}
		if !ok {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		handler, allowed := rt.handlers[r.Method]
		if !allowed {
			w.Header().Set("Allow", rt.allow())
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if r.Method != http.MethodGet && !a.sameSiteJSON(w, r) {
			return
		}
		handler(w, r)
	}))
}

// route is what a path answers: a handler for each method it allows, in the order they are named in Allow.
type route struct {
	handlers map[string]http.HandlerFunc
	order    []string
}

func newRoute(method string, h http.HandlerFunc) route {
	return route{handlers: map[string]http.HandlerFunc{method: h}, order: []string{method}}
}

func onGet(h http.HandlerFunc) route  { return newRoute(http.MethodGet, h) }
func onPost(h http.HandlerFunc) route { return newRoute(http.MethodPost, h) }

// with returns the route that also answers method.
func (r route) with(method string, h http.HandlerFunc) route {
	handlers := maps.Clone(r.handlers)
	handlers[method] = h
	return route{handlers: handlers, order: append(slices.Clone(r.order), method)}
}

// wrap returns the route with every handler passed through f.
func (r route) wrap(f func(http.HandlerFunc) http.HandlerFunc) route {
	handlers := make(map[string]http.HandlerFunc, len(r.handlers))
	for method, h := range r.handlers {
		handlers[method] = f(h)
	}
	return route{handlers: handlers, order: r.order}
}

// allow is the value of the Allow header.
func (r route) allow() string { return strings.Join(r.order, ", ") }

// isAPIPath reports whether a path belongs to the API, which never falls through to the UI.
func isAPIPath(p string) bool { return p == "/api" || strings.HasPrefix(p, "/api/") }

func setAPIHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
}

// logRequests logs every request at debug level, so normal operation stays quiet.
func (a *api) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		a.log.Debug("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start))
	})
}

// statusRecorder remembers the status code of a response.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer, for example to flush a stream.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// loopbackHost reports whether a Host header names this machine. Anything else is refused, which blocks DNS
// rebinding: a page on another origin reaching the API through a hostname it controls.
func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return netutil.IsLoopbackHost(host)
}

func (a *api) status(w http.ResponseWriter, r *http.Request) {
	type caddyStatus struct {
		Reachable bool   `json:"reachable"`
		Endpoint  string `json:"endpoint"`
		Error     string `json:"error,omitempty"`
	}
	s := caddyStatus{Reachable: true, Endpoint: a.opts.Endpoint}
	if _, err := a.caddy.Config(r.Context()); err != nil {
		// Debug, not warn: the UI polls this route, and an unreachable Caddy would flood the log.
		a.log.Debug("caddy is not reachable", "error", err)
		s.Reachable = false
		s.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{"caddy": s})
}

func (a *api) config(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.caddy.Config(r.Context())
	if err != nil {
		a.badGateway(w, r, err)
		return
	}
	if !a.opts.ShowSecrets {
		cfg = redact.Config(cfg)
	}
	writeJSON(w, http.StatusOK, cfg)
}

type upstreamJSON struct {
	Address     string `json:"address"`
	NumRequests int    `json:"numRequests"`
	Fails       int    `json:"fails"`
}

type siteJSON struct {
	Server    string         `json:"server"`
	Host      string         `json:"host"`
	Upstreams []upstreamJSON `json:"upstreams"`
}

func (a *api) sites(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.caddy.Config(r.Context())
	if err != nil {
		a.badGateway(w, r, err)
		return
	}

	state := map[string]caddy.Upstream{}
	ups, upsErr := a.caddy.Upstreams(r.Context())
	if upsErr != nil {
		a.log.Warn("upstream state unavailable, returning sites without it", "error", upsErr)
	}
	for _, u := range ups {
		state[u.Address] = u
	}

	extracted := caddy.ExtractSites(cfg)
	sites := make([]siteJSON, 0, len(extracted))
	for _, s := range extracted {
		site := siteJSON{Server: s.Server, Host: s.Host, Upstreams: make([]upstreamJSON, 0, len(s.Upstreams))}
		for _, addr := range s.Upstreams {
			u := state[addr]
			site.Upstreams = append(site.Upstreams, upstreamJSON{Address: addr, NumRequests: u.NumRequests, Fails: u.Fails})
		}
		sites = append(sites, site)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": sites, "upstreamsAvailable": upsErr == nil})
}

// badGateway logs why Caddy could not be read, so the cause is visible to the operator and not only to the
// client, and answers 502.
func (a *api) badGateway(w http.ResponseWriter, r *http.Request, err error) {
	a.log.Warn("caddy request failed", "path", r.URL.Path, "error", err)
	writeError(w, http.StatusBadGateway, err.Error())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
