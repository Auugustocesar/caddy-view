package server

import (
	"math"
	"net/http"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

type hostJSON struct {
	Host      string           `json:"host"`
	Requests  int64            `json:"requests"`
	Bytes     int64            `json:"bytes"`
	Errors5xx int64            `json:"errors5xx"`
	Status    map[string]int64 `json:"status"`
	AvgMs     *float64         `json:"avgMs"`
	P95Ms     *float64         `json:"p95Ms"`
	MaxMs     *float64         `json:"maxMs"`
}

// hosts answers the traffic of each host over a range, the busiest first. The range and its defaults are those of
// /api/traffic: the last hour by the minute, or the last day by the hour.
func (a *api) hosts(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	_, res, width, span, err := resolution(params.Get("resolution"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	from, to, err := timeRange(params)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	limit, err := intParam(params, "limit")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if to.IsZero() {
		to = a.currentTime()
	}
	if from.IsZero() {
		from = to.Add(-span)
	}
	if !from.Before(to) {
		a.fail(w, r, badParam("from", "%s is not before to (%s)", from.Format("2006-01-02T15:04:05Z07:00"), to.Format("2006-01-02T15:04:05Z07:00")))
		return
	}

	// The bucket that holds the start counts whole, as in /api/traffic.
	found, err := a.opts.History.QueryHosts(r.Context(), store.HostQuery{
		Resolution: res, From: from.Truncate(width), To: to, Limit: int(min(limit, math.MaxInt32)),
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]hostJSON, len(found))
	for i, h := range found {
		t := totals{requests: h.Requests, bytes: h.Bytes, status: h.Status, durationSum: h.DurationSum, durationMax: h.DurationMax, latency: h.Latency}
		out[i] = hostJSON{
			Host: h.Host, Requests: h.Requests, Bytes: h.Bytes, Errors5xx: h.Status[5], Status: t.statusJSON(),
			AvgMs: t.avgMs(), P95Ms: t.p95Ms(), MaxMs: t.maxMs(),
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": out})
}
