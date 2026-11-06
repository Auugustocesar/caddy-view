package server

import (
	"context"
	"net/http"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/attention"
	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// attention answers what needs a look over a window: the hosts with more errors than in the period before, the hosts
// far slower than the others, and the upstreams that do not answer. The rules are in package attention.
func (a *api) attention(w http.ResponseWriter, r *http.Request) {
	name := "1h"
	if r.URL.Query().Has("window") {
		name = r.URL.Query().Get("window")
	}
	window, ok := windows[name]
	if !ok {
		a.fail(w, r, badParam("window", "%q is not one of 5m, 1h, 6h, 24h, 7d", name))
		return
	}
	res, width := windowResolution(window)
	now := a.currentTime()

	current, err := a.hostStats(r.Context(), res, width, now.Add(-window), now)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	previous, err := a.hostStats(r.Context(), res, width, now.Add(-2*window), now.Add(-window).Truncate(width))
	if err != nil {
		a.fail(w, r, err)
		return
	}

	in := attention.Input{Hosts: current, Previous: previous}
	if a.opts.UpstreamHealth != nil {
		// Caddy being unreachable only leaves out what depends on it; the traffic items are still worth showing.
		if cfg, err := a.caddy.Config(r.Context()); err != nil {
			a.log.Warn("attention without upstream state, Caddy could not be read", "error", err)
		} else {
			in.Sites = a.siteStates(cfg)
			proxies, withChecks := caddy.CountHealthChecks(cfg)
			in.CaddyHasNoHealthChecks = proxies > 0 && withChecks == 0
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"window": name, "items": attention.Evaluate(in)})
}

// hostStats totals the traffic of each host in [from, to), the busiest first.
func (a *api) hostStats(ctx context.Context, res store.Resolution, width time.Duration, from, to time.Time) ([]attention.HostStats, error) {
	found, err := a.opts.History.QueryHosts(ctx, store.HostQuery{
		Resolution: res, From: from.Truncate(width), To: to, Limit: store.MaxHostLimit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]attention.HostStats, len(found))
	for i, h := range found {
		t := totals{requests: h.Requests, status: h.Status, latency: h.Latency}
		out[i] = attention.HostStats{Host: h.Host, Requests: h.Requests, Errors5xx: h.Status[5], P95Ms: t.p95Ms()}
	}
	return out, nil
}

// siteStates pairs the sites of the configuration with the result of the upstream check.
func (a *api) siteStates(cfg map[string]any) []attention.SiteState {
	var out []attention.SiteState
	for _, s := range caddy.ExtractSites(cfg) {
		site := attention.SiteState{Host: s.Host}
		for _, addr := range s.Upstreams {
			state := attention.Unknown
			if res, ok := a.opts.UpstreamHealth.Result(addr); ok {
				state = attention.State(res.State)
			}
			site.Upstreams = append(site.Upstreams, attention.UpstreamState{Address: addr, State: state})
		}
		out = append(out, site)
	}
	return out
}
