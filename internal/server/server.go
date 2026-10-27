// Package server is the read-only HTTP API of caddy-view.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
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
	// AccessLogPath is the file followed by /api/logs/stream. When empty, the file is discovered from the
	// running Caddy configuration.
	AccessLogPath string
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

// New returns the read-only API handler. It only serves GET, and only to loopback Host names.
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
	routes := map[string]http.HandlerFunc{
		"/api/status":      a.status,
		"/api/config":      a.config,
		"/api/sites":       a.sites,
		"/api/logs/stream": a.logStream,
	}
	return a.logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")

		if !loopbackHost(r.Host) {
			a.log.Warn("request rejected: host not allowed", "host", r.Host, "method", r.Method, "path", r.URL.Path)
			writeError(w, http.StatusForbidden, "host not allowed")
			return
		}
		handler, ok := routes[r.URL.Path]
		if !ok {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if r.Method != http.MethodGet {
			h.Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handler(w, r)
	}))
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
