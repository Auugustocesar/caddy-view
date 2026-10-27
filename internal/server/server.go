// Package server is the read-only HTTP API of caddy-view.
package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/netutil"
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
}

type api struct {
	caddy Caddy
	opts  Options
}

// New returns the read-only API handler. It only serves GET, and only to loopback Host names.
func New(c Caddy, opts Options) http.Handler {
	a := &api{caddy: c, opts: opts}
	routes := map[string]http.HandlerFunc{
		"/api/status": a.status,
		"/api/config": a.config,
		"/api/sites":  a.sites,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")

		if !loopbackHost(r.Host) {
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
	})
}

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
		s.Reachable = false
		s.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{"caddy": s})
}

func (a *api) config(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.caddy.Config(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
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
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	state := map[string]caddy.Upstream{}
	ups, upsErr := a.caddy.Upstreams(r.Context())
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
