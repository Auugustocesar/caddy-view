package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// History is what the history routes need from the store: the three read methods.
type History interface {
	QueryLogs(ctx context.Context, q store.LogQuery) ([]store.LogEntry, error)
	QueryTraffic(ctx context.Context, q store.TrafficQuery) ([]store.TrafficBucket, error)
	QueryMetrics(ctx context.Context, q store.MetricQuery) ([]store.MetricSample, error)
}

const (
	// maxBuckets is the most buckets one /api/traffic answer may hold.
	maxBuckets = 3000
	// trafficChunk is how many buckets are asked of the store at once. A bucket has at most six rows (one per status
	// class), so a chunk always fits in the row limit of the store.
	trafficChunk = store.MaxTrafficLimit / 6

	// freshness is how recent a metric sample must be to describe the present.
	freshness = 2 * time.Minute

	inFlightMetric = "caddy_http_requests_in_flight"
	healthyMetric  = "caddy_reverse_proxy_upstreams_healthy"
)

// badRequest is a problem with a request parameter, which names the parameter.
type badRequest struct {
	param, why string
}

func (e *badRequest) Error() string { return fmt.Sprintf("invalid %s: %s", e.param, e.why) }

func badParam(param, format string, args ...any) error {
	return &badRequest{param: param, why: fmt.Sprintf(format, args...)}
}

// fail answers a failed history request: a bad parameter is the client's problem and says what is wrong, anything
// else is ours, so the cause goes to the log and the client gets a generic message.
func (a *api) fail(w http.ResponseWriter, r *http.Request, err error) {
	var bad *badRequest
	switch {
	case errors.As(err, &bad):
		writeError(w, http.StatusBadRequest, bad.Error())
	case errors.Is(err, store.ErrInvalidQuery):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		a.log.Error("history query failed", "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func (a *api) currentTime() time.Time {
	if a.opts.Now != nil {
		return a.opts.Now().UTC()
	}
	return time.Now().UTC()
}

// ---- parameters

// intParam reads an optional integer parameter that must not be negative. An empty or missing one is absent.
func intParam(q url.Values, name string) (int64, error) {
	raw := q.Get(name)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, badParam(name, "%q is not a whole number", raw)
	}
	if n < 0 {
		return 0, badParam(name, "%d is negative", n)
	}
	return n, nil
}

// timeParam reads an optional RFC 3339 time. An empty or missing one is the zero time.
func timeParam(q url.Values, name string) (time.Time, error) {
	raw := q.Get(name)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, badParam(name, "%q is not an RFC 3339 time such as 2026-10-27T12:00:00Z", raw)
	}
	return t.UTC(), nil
}

// timeRange reads from and to, and checks that from comes first when both are given.
func timeRange(q url.Values) (from, to time.Time, err error) {
	if from, err = timeParam(q, "from"); err != nil {
		return
	}
	if to, err = timeParam(q, "to"); err != nil {
		return
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		err = badParam("from", "%s is not before to (%s)", from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	return
}

// ---- /api/logs

type entryJSON struct {
	ID         int64     `json:"id"`
	Time       time.Time `json:"time"`
	Host       string    `json:"host"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMs float64   `json:"durationMs"`
	Bytes      int64     `json:"bytes"`
	ClientIP   string    `json:"clientIp"`
}

func (a *api) logs(w http.ResponseWriter, r *http.Request) {
	query, err := logQuery(r.URL.Query())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	entries, err := a.opts.History.QueryLogs(r.Context(), query)
	if err != nil {
		a.fail(w, r, err)
		return
	}

	out := make([]entryJSON, len(entries))
	for i, e := range entries {
		out[i] = entryJSON{
			ID: e.ID, Time: e.Time, Host: e.Host, Method: e.Method, Path: e.Path, Status: e.Status,
			DurationMs: float64(e.Duration.Microseconds()) / 1000, Bytes: e.Bytes, ClientIP: e.ClientIP,
		}
	}
	// A full page may be followed by more: its last ID is where the next page starts.
	var next *int64
	if len(entries) > 0 && len(entries) == effectiveLimit(query.Limit) {
		next = &entries[len(entries)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "next": next})
}

func effectiveLimit(limit int) int {
	switch {
	case limit <= 0:
		return store.DefaultLimit
	case limit > store.MaxLimit:
		return store.MaxLimit
	}
	return limit
}

func logQuery(params url.Values) (store.LogQuery, error) {
	q := store.LogQuery{Host: params.Get("host"), Text: params.Get("q")}
	limit, err := intParam(params, "limit")
	if err != nil {
		return q, err
	}
	before, err := intParam(params, "before")
	if err != nil {
		return q, err
	}
	minStatus, err := intParam(params, "minStatus")
	if err != nil {
		return q, err
	}
	maxStatus, err := intParam(params, "maxStatus")
	if err != nil {
		return q, err
	}
	q.Limit, q.BeforeID, q.MinStatus, q.MaxStatus = int(min(limit, math.MaxInt32)), before, int(min(minStatus, math.MaxInt32)), int(min(maxStatus, math.MaxInt32))
	q.From, q.To, err = timeRange(params)
	return q, err
}

// ---- traffic

// statusKeys name the status classes in the JSON, indexed by class; class 0 is everything else.
var statusKeys = [6]string{"other", "1xx", "2xx", "3xx", "4xx", "5xx"}

// totals adds up traffic buckets of any number of status classes and start times.
type totals struct {
	requests, bytes int64
	status          [6]int64
	durationSum     time.Duration
	durationMax     time.Duration
	latency         [store.LatencyBuckets]int64
}

func (t *totals) add(b store.TrafficBucket) {
	t.requests += b.Count
	t.bytes += b.Bytes
	t.status[b.Class] += b.Count
	t.durationSum += b.DurationSum
	t.durationMax = max(t.durationMax, b.DurationMax)
	for i, n := range b.Latency {
		t.latency[i] += n
	}
}

func (t *totals) statusJSON() map[string]int64 {
	out := make(map[string]int64, len(statusKeys))
	for class, key := range statusKeys {
		out[key] = t.status[class]
	}
	return out
}

func millis(d time.Duration) *float64 {
	ms := float64(d.Microseconds()) / 1000
	return &ms
}

// avgMs is the mean duration, or nil without requests.
func (t *totals) avgMs() *float64 {
	if t.requests == 0 {
		return nil
	}
	return millis(t.durationSum / time.Duration(t.requests))
}

// maxMs is the longest duration, or nil without requests.
func (t *totals) maxMs() *float64 {
	if t.requests == 0 {
		return nil
	}
	return millis(t.durationMax)
}

// p95Ms estimates the 95th percentile, or nil without requests.
func (t *totals) p95Ms() *float64 {
	if t.requests == 0 {
		return nil
	}
	return millis(store.LatencyQuantile(t.latency, 0.95))
}

// fetchTraffic reads the buckets that start in [from, to), a few thousand at a time so that a long range cannot
// exceed the row limit of the store.
func (a *api) fetchTraffic(ctx context.Context, res store.Resolution, width time.Duration, from, to time.Time, host string) ([]store.TrafficBucket, error) {
	var out []store.TrafficBucket
	for start := from; start.Before(to); start = start.Add(trafficChunk * width) {
		end := start.Add(trafficChunk * width)
		if end.After(to) {
			end = to
		}
		got, err := a.opts.History.QueryTraffic(ctx, store.TrafficQuery{Resolution: res, From: start, To: end, Host: host})
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

type bucketJSON struct {
	Start    time.Time        `json:"start"`
	Requests int64            `json:"requests"`
	Bytes    int64            `json:"bytes"`
	Status   map[string]int64 `json:"status"`
	AvgMs    *float64         `json:"avgMs"`
	P95Ms    *float64         `json:"p95Ms"`
	MaxMs    *float64         `json:"maxMs"`
}

func (a *api) traffic(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	name, res, width, span, err := resolution(params.Get("resolution"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	from, to, err := timeRange(params)
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
		a.fail(w, r, badParam("from", "%s is not before to (%s)", from.Format(time.RFC3339), to.Format(time.RFC3339)))
		return
	}
	first := from.Truncate(width)
	if n := int64((to.Sub(first) + width - 1) / width); n > maxBuckets {
		a.fail(w, r, badParam("from", "the range holds %d buckets of %s, at most %d are allowed", n, width, maxBuckets))
		return
	}

	rows, err := a.fetchTraffic(r.Context(), res, width, first, to, params.Get("host"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	byStart := make(map[time.Time]*totals)
	for _, row := range rows {
		t := byStart[row.Start]
		if t == nil {
			t = &totals{}
			byStart[row.Start] = t
		}
		t.add(row)
	}

	var buckets []bucketJSON
	for start := first; start.Before(to); start = start.Add(width) {
		t := byStart[start]
		if t == nil {
			t = &totals{}
		}
		buckets = append(buckets, bucketJSON{
			Start: start, Requests: t.requests, Bytes: t.bytes, Status: t.statusJSON(),
			AvgMs: t.avgMs(), P95Ms: t.p95Ms(), MaxMs: t.maxMs(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resolution": name, "bucketSeconds": int(width.Seconds()), "buckets": buckets,
	})
}

// resolution reads the resolution parameter: its canonical name, the store resolution, the bucket width and the
// default span.
func resolution(name string) (string, store.Resolution, time.Duration, time.Duration, error) {
	switch name {
	case "", "minute":
		return "minute", store.PerMinute, time.Minute, time.Hour, nil
	case "hour":
		return "hour", store.PerHour, time.Hour, 24 * time.Hour, nil
	}
	return "", 0, 0, 0, badParam("resolution", "%q is not minute or hour", name)
}

// ---- /api/summary

// windows are the summary windows by name. Up to sixMinuteWindows the minute rollups answer; longer ones use hours.
var windows = map[string]time.Duration{
	"5m": 5 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour,
}

const longestMinuteWindow = 6 * time.Hour

type upstreamsJSON struct {
	Healthy int `json:"healthy"`
	Total   int `json:"total"`
}

func (a *api) summary(w http.ResponseWriter, r *http.Request) {
	name := "1h"
	if r.URL.Query().Has("window") {
		name = r.URL.Query().Get("window")
	}
	window, ok := windows[name]
	if !ok {
		a.fail(w, r, badParam("window", "%q is not one of 5m, 1h, 6h, 24h, 7d", name))
		return
	}

	res, width := store.PerMinute, time.Minute
	if window > longestMinuteWindow {
		res, width = store.PerHour, time.Hour
	}
	now := a.currentTime()
	rows, err := a.fetchTraffic(r.Context(), res, width, now.Add(-window).Truncate(width), now, "")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var t totals
	for _, row := range rows {
		t.add(row)
	}

	inFlight, err := a.inFlight(r.Context(), now)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	upstreams, err := a.upstreams(r.Context(), now)
	if err != nil {
		a.fail(w, r, err)
		return
	}

	var errorRate *float64
	if t.requests > 0 {
		rate := float64(t.status[5]) / float64(t.requests)
		errorRate = &rate
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"window": name, "requests": t.requests, "requestsPerSecond": float64(t.requests) / window.Seconds(),
		"errors5xx": t.status[5], "errorRate": errorRate, "avgMs": t.avgMs(), "p95Ms": t.p95Ms(),
		"bytes": t.bytes, "status": t.statusJSON(), "inFlight": inFlight, "upstreams": upstreams,
	})
}

// newest returns the latest value of every series of a metric within the freshness window, or nil when there is none.
func (a *api) newest(ctx context.Context, name string, now time.Time) (map[string]float64, error) {
	samples, err := a.opts.History.QueryMetrics(ctx, store.MetricQuery{Name: name, From: now.Add(-freshness)})
	if err != nil {
		return nil, err
	}
	// Oldest first, so the last sample of a series is the one that stays.
	latest := make(map[string]float64)
	for _, s := range samples {
		latest[s.Labels] = s.Value
	}
	if len(latest) == 0 {
		return nil, nil
	}
	return latest, nil
}

// inFlight is the number of requests being served now, over every server, or nil without recent metrics.
func (a *api) inFlight(ctx context.Context, now time.Time) (*int64, error) {
	latest, err := a.newest(ctx, inFlightMetric, now)
	if err != nil || latest == nil {
		return nil, err
	}
	var sum float64
	for _, v := range latest {
		sum += v
	}
	n := int64(math.Round(sum))
	return &n, nil
}

// upstreams counts the healthy upstreams among those seen recently, or nil without recent metrics.
func (a *api) upstreams(ctx context.Context, now time.Time) (*upstreamsJSON, error) {
	latest, err := a.newest(ctx, healthyMetric, now)
	if err != nil || latest == nil {
		return nil, err
	}
	u := upstreamsJSON{Total: len(latest)}
	for _, v := range latest {
		if v >= 0.5 {
			u.Healthy++
		}
	}
	return &u, nil
}
