package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// clock is the time of every history test: 2026-10-27 12:00:00 UTC, on a minute and an hour boundary.
var clock = time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

var errStore = errors.New("the disk is on fire")

func historyHandler(t *testing.T, h History) http.Handler {
	t.Helper()
	return New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{
		Endpoint: testEndpoint, History: h, Now: func() time.Time { return clock },
	})
}

func req(host string, status int, at time.Time, d time.Duration, bytes int64) store.LogEntry {
	return store.LogEntry{Time: at, Host: host, Method: "GET", Path: "/", Status: status, Duration: d, Bytes: bytes}
}

func at(path string, e store.LogEntry) store.LogEntry {
	e.Path = path
	return e
}

func seed(t *testing.T, m *store.Memory, entries ...store.LogEntry) {
	t.Helper()
	if err := m.AppendLog(context.Background(), store.LogBatch{Entries: entries}); err != nil {
		t.Fatal(err)
	}
}

func seedMetrics(t *testing.T, m *store.Memory, samples ...store.MetricSample) {
	t.Helper()
	if err := m.AppendMetrics(context.Background(), samples); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, h http.Handler, path string, params url.Values) (int, string) {
	t.Helper()
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	rec := do(t, h, http.MethodGet, path)
	return rec.Code, rec.Body.String()
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// failingHistory answers every query with an error.
type failingHistory struct{}

func (failingHistory) QueryLogs(context.Context, store.LogQuery) ([]store.LogEntry, error) {
	return nil, errStore
}

func (failingHistory) QueryTraffic(context.Context, store.TrafficQuery) ([]store.TrafficBucket, error) {
	return nil, errStore
}

func (failingHistory) QueryHosts(context.Context, store.HostQuery) ([]store.HostTotals, error) {
	return nil, errStore
}

func (failingHistory) QueryMetrics(context.Context, store.MetricQuery) ([]store.MetricSample, error) {
	return nil, errStore
}

// errorBody decodes an error answer.
func errorBody(t *testing.T, body string) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil || e.Error == "" {
		t.Fatalf("body %q is not an error object: %v", body, err)
	}
	return e.Error
}

var historyPaths = []string{"/api/logs", "/api/traffic", "/api/summary"}

func TestHistoryRoutesAreAbsentWithoutHistory(t *testing.T) {
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint})
	for _, p := range historyPaths {
		if code, _ := get(t, h, p, nil); code != http.StatusNotFound {
			t.Errorf("GET %s without a history = %d, want 404", p, code)
		}
	}
}

func TestHistoryRoutesOnlyAnswerGET(t *testing.T) {
	h := historyHandler(t, store.NewMemory())
	for _, p := range historyPaths {
		if rec := do(t, h, http.MethodPost, p); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", p, rec.Code)
		}
	}
}

func TestAStoreFailureIsAnInternalErrorThatHidesTheCause(t *testing.T) {
	h := historyHandler(t, failingHistory{})
	for _, p := range historyPaths {
		code, body := get(t, h, p, nil)
		if code != http.StatusInternalServerError {
			t.Errorf("GET %s with a failing store = %d, want 500", p, code)
		}
		if msg := errorBody(t, body); strings.Contains(msg, errStore.Error()) {
			t.Errorf("GET %s leaked the cause to the client: %q", p, msg)
		}
	}
}

// ---- /api/logs

type logsAnswer struct {
	Entries []struct {
		ID         int64   `json:"id"`
		Time       string  `json:"time"`
		Host       string  `json:"host"`
		Method     string  `json:"method"`
		Path       string  `json:"path"`
		Status     int     `json:"status"`
		DurationMs float64 `json:"durationMs"`
		Bytes      int64   `json:"bytes"`
		ClientIP   string  `json:"clientIp"`
	} `json:"entries"`
	Next *int64 `json:"next"`
}

func logs(t *testing.T, h http.Handler, params url.Values) logsAnswer {
	t.Helper()
	code, body := get(t, h, "/api/logs", params)
	if code != http.StatusOK {
		t.Fatalf("GET /api/logs?%s = %d: %s", params.Encode(), code, body)
	}
	var out logsAnswer
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, body)
	}
	return out
}

func (a logsAnswer) paths() []string {
	out := make([]string, len(a.Entries))
	for i, e := range a.Entries {
		out[i] = e.Path
	}
	return out
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLogsReturnsTheNewestFirstWithTheirFields(t *testing.T) {
	m := store.NewMemory()
	seed(t, m,
		at("/old", req("a.example.com", 200, clock.Add(-2*time.Minute), time.Millisecond, 1)),
		store.LogEntry{
			Time: clock.Add(-time.Minute + 123*time.Millisecond), Host: "h.example.com", Method: "POST", Path: "/v1/items",
			Status: 502, Duration: 12345 * time.Microsecond, Bytes: 4096, ClientIP: "10.0.0.7",
		},
	)
	got := logs(t, historyHandler(t, m), nil)

	if want := []string{"/v1/items", "/old"}; !sameList(got.paths(), want) {
		t.Fatalf("paths = %v, want %v", got.paths(), want)
	}
	e := got.Entries[0]
	if e.ID <= 0 || e.Time != "2026-10-27T11:59:00.123Z" || e.Host != "h.example.com" || e.Method != "POST" ||
		e.Status != 502 || !near(e.DurationMs, 12.345) || e.Bytes != 4096 || e.ClientIP != "10.0.0.7" {
		t.Errorf("entry = %+v", e)
	}
	if got.Next != nil {
		t.Errorf("next = %d, want null: the page is not full", *got.Next)
	}
}

func TestLogsPagesBackwardsWithNext(t *testing.T) {
	m := store.NewMemory()
	for i := 1; i <= 5; i++ {
		seed(t, m, at(fmt.Sprintf("/%d", i), req("h", 200, clock, time.Millisecond, 1)))
	}
	h := historyHandler(t, m)

	first := logs(t, h, url.Values{"limit": {"2"}})
	if want := []string{"/5", "/4"}; !sameList(first.paths(), want) {
		t.Fatalf("first page = %v, want %v", first.paths(), want)
	}
	if first.Next == nil || *first.Next != first.Entries[1].ID {
		t.Fatalf("next = %v, want the ID of the last entry %d", first.Next, first.Entries[1].ID)
	}

	second := logs(t, h, url.Values{"limit": {"2"}, "before": {fmt.Sprint(*first.Next)}})
	if want := []string{"/3", "/2"}; !sameList(second.paths(), want) {
		t.Fatalf("second page = %v, want %v", second.paths(), want)
	}

	third := logs(t, h, url.Values{"limit": {"2"}, "before": {fmt.Sprint(*second.Next)}})
	if want := []string{"/1"}; !sameList(third.paths(), want) || third.Next != nil {
		t.Errorf("last page = %v with next %v, want [/1] and no next", third.paths(), third.Next)
	}
}

func TestLogsFilters(t *testing.T) {
	m := store.NewMemory()
	seed(t, m,
		at("/index", req("a.example.com", 200, clock.Add(-3*time.Minute), time.Millisecond, 1)),
		at("/Login", req("a.example.com", 302, clock.Add(-2*time.Minute), time.Millisecond, 1)),
		at("/login", req("b.example.com", 404, clock.Add(-time.Minute), time.Millisecond, 1)),
		at("/api/100%", req("b.example.com", 500, clock, time.Millisecond, 1)),
	)
	h := historyHandler(t, m)

	tests := []struct {
		name   string
		params url.Values
		want   []string
	}{
		{"host", url.Values{"host": {"b.example.com"}}, []string{"/api/100%", "/login"}},
		{"minimum status", url.Values{"minStatus": {"400"}}, []string{"/api/100%", "/login"}},
		{"maximum status", url.Values{"maxStatus": {"302"}}, []string{"/Login", "/index"}},
		{"status range", url.Values{"minStatus": {"302"}, "maxStatus": {"404"}}, []string{"/login", "/Login"}},
		{"text ignores case", url.Values{"q": {"LOGIN"}}, []string{"/login", "/Login"}},
		{"text is literal", url.Values{"q": {"100%"}}, []string{"/api/100%"}},
		{"time range", url.Values{"from": {rfc(clock.Add(-2 * time.Minute))}, "to": {rfc(clock.Add(-time.Minute))}}, []string{"/Login"}},
		{"filters combine", url.Values{"host": {"b.example.com"}, "q": {"login"}}, []string{"/login"}},
		{"nothing matches", url.Values{"q": {"nothing like this"}}, []string{}},
	}
	for _, tt := range tests {
		if got := logs(t, h, tt.params).paths(); !sameList(got, tt.want) {
			t.Errorf("%s: paths = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestBadParametersAreAnErrorThatNamesThem(t *testing.T) {
	h := historyHandler(t, store.NewMemory())
	tests := []struct {
		path   string
		params url.Values
		name   string // the parameter the message must name
	}{
		{"/api/logs", url.Values{"limit": {"abc"}}, "limit"},
		{"/api/logs", url.Values{"limit": {"-1"}}, "limit"},
		{"/api/logs", url.Values{"before": {"abc"}}, "before"},
		{"/api/logs", url.Values{"before": {"-5"}}, "before"},
		{"/api/logs", url.Values{"minStatus": {"abc"}}, "minStatus"},
		{"/api/logs", url.Values{"maxStatus": {"1.5"}}, "maxStatus"},
		{"/api/logs", url.Values{"from": {"yesterday"}}, "from"},
		{"/api/logs", url.Values{"to": {"2026-13-45T00:00:00Z"}}, "to"},
		{"/api/logs", url.Values{"from": {rfc(clock)}, "to": {rfc(clock.Add(-time.Hour))}}, "from"},
		{"/api/traffic", url.Values{"resolution": {"week"}}, "resolution"},
		{"/api/traffic", url.Values{"from": {"bad"}}, "from"},
		{"/api/traffic", url.Values{"to": {"bad"}}, "to"},
		{"/api/traffic", url.Values{"from": {rfc(clock)}, "to": {rfc(clock)}}, "from"},
		{"/api/traffic", url.Values{"from": {rfc(clock)}, "to": {rfc(clock.Add(-time.Minute))}}, "from"},
		{"/api/traffic", url.Values{"from": {rfc(clock.Add(-3001 * time.Minute))}, "to": {rfc(clock)}}, "from"},
		{"/api/traffic", url.Values{"resolution": {"hour"}, "from": {rfc(clock.Add(-3001 * time.Hour))}, "to": {rfc(clock)}}, "from"},
		{"/api/summary", url.Values{"window": {"2w"}}, "window"},
		{"/api/summary", url.Values{"window": {""}}, "window"},
	}
	for _, tt := range tests {
		t.Run(tt.path+"?"+tt.params.Encode(), func(t *testing.T) {
			code, body := get(t, h, tt.path, tt.params)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", code, body)
			}
			if msg := errorBody(t, body); !strings.Contains(msg, tt.name) {
				t.Errorf("error %q does not name the parameter %q", msg, tt.name)
			}
		})
	}
}

// ---- /api/traffic

type trafficAnswer struct {
	Resolution    string `json:"resolution"`
	BucketSeconds int    `json:"bucketSeconds"`
	Buckets       []struct {
		Start    string           `json:"start"`
		Requests int64            `json:"requests"`
		Bytes    int64            `json:"bytes"`
		Status   map[string]int64 `json:"status"`
		AvgMs    *float64         `json:"avgMs"`
		P95Ms    *float64         `json:"p95Ms"`
		MaxMs    *float64         `json:"maxMs"`
	} `json:"buckets"`
}

func traffic(t *testing.T, h http.Handler, params url.Values) trafficAnswer {
	t.Helper()
	code, body := get(t, h, "/api/traffic", params)
	if code != http.StatusOK {
		t.Fatalf("GET /api/traffic?%s = %d: %s", params.Encode(), code, body)
	}
	var out trafficAnswer
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, body)
	}
	return out
}

// seedBusyMinute stores four requests in the minute starting 11:30, from two hosts and three status classes.
func seedBusyMinute(t *testing.T, m *store.Memory) {
	t.Helper()
	busy := clock.Add(-30*time.Minute + 10*time.Second)
	seed(t, m,
		req("a.example.com", 200, busy, 20*time.Millisecond, 100),
		req("a.example.com", 200, busy.Add(time.Second), 30*time.Millisecond, 50),
		req("b.example.com", 502, busy.Add(2*time.Second), 3*time.Second, 7),
		req("a.example.com", 404, busy.Add(3*time.Second), 5*time.Millisecond, 1),
	)
}

func TestTrafficFillsTheLastHourByMinute(t *testing.T) {
	m := store.NewMemory()
	seedBusyMinute(t, m)
	got := traffic(t, historyHandler(t, m), nil)

	if got.Resolution != "minute" || got.BucketSeconds != 60 {
		t.Errorf("resolution %q with %d seconds per bucket, want minute and 60", got.Resolution, got.BucketSeconds)
	}
	if len(got.Buckets) != 60 {
		t.Fatalf("got %d buckets, want 60 (every minute of the last hour)", len(got.Buckets))
	}
	if first := got.Buckets[0]; first.Start != "2026-10-27T11:00:00Z" || first.Requests != 0 || first.AvgMs != nil || first.P95Ms != nil || first.MaxMs != nil {
		t.Errorf("an empty bucket = %+v, want zero requests and null latencies", first)
	}

	busy := got.Buckets[30]
	if busy.Start != "2026-10-27T11:30:00Z" || busy.Requests != 4 || busy.Bytes != 158 {
		t.Fatalf("the busy bucket = %+v", busy)
	}
	wantStatus := map[string]int64{"1xx": 0, "2xx": 2, "3xx": 0, "4xx": 1, "5xx": 1, "other": 0}
	for class, n := range wantStatus {
		if busy.Status[class] != n {
			t.Errorf("status %s = %d, want %d (all: %v)", class, busy.Status[class], n, busy.Status)
		}
	}
	if len(busy.Status) != len(wantStatus) {
		t.Errorf("status has keys %v, want exactly %v", busy.Status, wantStatus)
	}
	if busy.AvgMs == nil || !near(*busy.AvgMs, 763.75) {
		t.Errorf("avgMs = %v, want 763.75", busy.AvgMs)
	}
	if busy.MaxMs == nil || !near(*busy.MaxMs, 3000) {
		t.Errorf("maxMs = %v, want 3000", busy.MaxMs)
	}
	if busy.P95Ms == nil || !near(*busy.P95Ms, 5000) {
		t.Errorf("p95Ms = %v, want 5000: the bound of the bucket that holds 3 s", busy.P95Ms)
	}
}

func TestTrafficByHourDefaultsToTheLastDay(t *testing.T) {
	m := store.NewMemory()
	seedBusyMinute(t, m)
	got := traffic(t, historyHandler(t, m), url.Values{"resolution": {"hour"}})

	if got.Resolution != "hour" || got.BucketSeconds != 3600 || len(got.Buckets) != 24 {
		t.Fatalf("resolution %q, %d seconds, %d buckets, want hour, 3600 and 24", got.Resolution, got.BucketSeconds, len(got.Buckets))
	}
	if got.Buckets[0].Start != "2026-10-26T12:00:00Z" {
		t.Errorf("first bucket starts %s, want 2026-10-26T12:00:00Z", got.Buckets[0].Start)
	}
	if last := got.Buckets[23]; last.Start != "2026-10-27T11:00:00Z" || last.Requests != 4 {
		t.Errorf("last bucket = %+v, want the 11:00 hour with 4 requests", last)
	}
}

func TestTrafficCanBeLimitedToOneHost(t *testing.T) {
	m := store.NewMemory()
	seedBusyMinute(t, m)
	got := traffic(t, historyHandler(t, m), url.Values{"host": {"b.example.com"}})
	if busy := got.Buckets[30]; busy.Requests != 1 || busy.Status["5xx"] != 1 {
		t.Errorf("host b bucket = %+v, want one 5xx request", busy)
	}
}

func TestTrafficRanges(t *testing.T) {
	m := store.NewMemory()
	seedBusyMinute(t, m)
	h := historyHandler(t, m)

	tests := []struct {
		name       string
		params     url.Values
		wantCount  int
		wantFirst  string
		busyAtLast bool
	}{
		{"an explicit range", url.Values{"from": {"2026-10-27T11:29:00Z"}, "to": {"2026-10-27T11:32:00Z"}}, 3, "2026-10-27T11:29:00Z", false},
		{"a start inside a minute is aligned down", url.Values{"from": {"2026-10-27T11:29:30Z"}, "to": {"2026-10-27T11:31:30Z"}}, 3, "2026-10-27T11:29:00Z", false},
		{"only to: the default span before it", url.Values{"to": {"2026-10-27T11:31:00Z"}}, 60, "2026-10-27T10:31:00Z", true},
		{"only from: up to now", url.Values{"from": {"2026-10-27T11:58:00Z"}}, 2, "2026-10-27T11:58:00Z", false},
		{"the longest range", url.Values{"from": {rfc(clock.Add(-3000 * time.Minute))}, "to": {rfc(clock)}}, 3000, "2026-10-25T10:00:00Z", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := traffic(t, h, tt.params)
			if len(got.Buckets) != tt.wantCount || got.Buckets[0].Start != tt.wantFirst {
				t.Fatalf("got %d buckets from %s, want %d from %s", len(got.Buckets), got.Buckets[0].Start, tt.wantCount, tt.wantFirst)
			}
			if tt.busyAtLast && got.Buckets[len(got.Buckets)-1].Requests != 4 {
				t.Errorf("the last bucket has %d requests, want the busy minute with 4", got.Buckets[len(got.Buckets)-1].Requests)
			}
		})
	}
}

// ---- /api/summary

type summaryAnswer struct {
	Requests          int64            `json:"requests"`
	RequestsPerSecond float64          `json:"requestsPerSecond"`
	Errors5xx         int64            `json:"errors5xx"`
	ErrorRate         *float64         `json:"errorRate"`
	AvgMs             *float64         `json:"avgMs"`
	P95Ms             *float64         `json:"p95Ms"`
	Bytes             int64            `json:"bytes"`
	Status            map[string]int64 `json:"status"`
	InFlight          *int64           `json:"inFlight"`
	Upstreams         *struct {
		Healthy int `json:"healthy"`
		Total   int `json:"total"`
	} `json:"upstreams"`
}

func summary(t *testing.T, h http.Handler, params url.Values) summaryAnswer {
	t.Helper()
	code, body := get(t, h, "/api/summary", params)
	if code != http.StatusOK {
		t.Fatalf("GET /api/summary?%s = %d: %s", params.Encode(), code, body)
	}
	var out summaryAnswer
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, body)
	}
	return out
}

func TestSummaryOfTheLastHour(t *testing.T) {
	m := store.NewMemory()
	seedBusyMinute(t, m)
	got := summary(t, historyHandler(t, m), nil)

	if got.Requests != 4 || got.Errors5xx != 1 || got.Bytes != 158 {
		t.Errorf("requests %d, 5xx %d, bytes %d, want 4, 1 and 158", got.Requests, got.Errors5xx, got.Bytes)
	}
	if !near(got.RequestsPerSecond, 4.0/3600) {
		t.Errorf("requestsPerSecond = %v, want 4/3600: the whole window counts, not only the busy minute", got.RequestsPerSecond)
	}
	if got.ErrorRate == nil || !near(*got.ErrorRate, 0.25) {
		t.Errorf("errorRate = %v, want 0.25", got.ErrorRate)
	}
	if got.AvgMs == nil || !near(*got.AvgMs, 763.75) || got.P95Ms == nil || !near(*got.P95Ms, 5000) {
		t.Errorf("avgMs %v and p95Ms %v, want 763.75 and 5000", got.AvgMs, got.P95Ms)
	}
	if got.Status["2xx"] != 2 || got.Status["4xx"] != 1 || got.Status["5xx"] != 1 || got.Status["3xx"] != 0 {
		t.Errorf("status = %v", got.Status)
	}
	if got.InFlight != nil || got.Upstreams != nil {
		t.Errorf("inFlight %v and upstreams %v, want null without metrics", got.InFlight, got.Upstreams)
	}
}

func TestSummaryWithoutTraffic(t *testing.T) {
	got := summary(t, historyHandler(t, store.NewMemory()), nil)
	if got.Requests != 0 || got.RequestsPerSecond != 0 || got.Errors5xx != 0 || got.Bytes != 0 {
		t.Errorf("summary = %+v, want zeros", got)
	}
	if got.ErrorRate != nil || got.AvgMs != nil || got.P95Ms != nil {
		t.Errorf("errorRate %v, avgMs %v, p95Ms %v, want null without requests", got.ErrorRate, got.AvgMs, got.P95Ms)
	}
}

func TestSummaryWindows(t *testing.T) {
	m := store.NewMemory()
	seedBusyMinute(t, m) // 30 minutes ago
	seed(t, m,
		req("a.example.com", 200, clock.Add(-90*time.Second), time.Millisecond, 10), // 90 seconds ago
		req("a.example.com", 200, clock.Add(-72*time.Hour), 10*time.Millisecond, 5), // three days ago
	)
	h := historyHandler(t, m)

	for window, want := range map[string]int64{"5m": 1, "1h": 5, "6h": 5, "24h": 5, "7d": 6} {
		if got := summary(t, h, url.Values{"window": {window}}); got.Requests != want {
			t.Errorf("window %s counts %d requests, want %d", window, got.Requests, want)
		}
	}
	if got := summary(t, h, url.Values{"window": {"5m"}}); !near(got.RequestsPerSecond, 1.0/300) {
		t.Errorf("5m requestsPerSecond = %v, want 1/300", got.RequestsPerSecond)
	}
}

func TestSummaryTakesInFlightAndUpstreamsFromTheNewestSamples(t *testing.T) {
	m := store.NewMemory()
	sample := func(name, labels string, ago time.Duration, v float64) store.MetricSample {
		return store.MetricSample{Name: name, Labels: labels, Time: clock.Add(-ago), Value: v, Raw: v}
	}
	seedMetrics(t, m,
		sample("caddy_http_requests_in_flight", `{server="srv0"}`, 70*time.Second, 5), // replaced by a newer one
		sample("caddy_http_requests_in_flight", `{server="srv0"}`, 10*time.Second, 2),
		sample("caddy_http_requests_in_flight", `{server="srv1"}`, 5*time.Second, 1),
		sample("caddy_http_requests_in_flight", `{server="srv2"}`, 5*time.Minute, 9), // too old to count
		sample("caddy_reverse_proxy_upstreams_healthy", `{upstream="a:1"}`, 90*time.Second, 0),
		sample("caddy_reverse_proxy_upstreams_healthy", `{upstream="a:1"}`, 20*time.Second, 1),
		sample("caddy_reverse_proxy_upstreams_healthy", `{upstream="b:2"}`, 20*time.Second, 1),
		sample("caddy_reverse_proxy_upstreams_healthy", `{upstream="c:3"}`, 20*time.Second, 0),
	)
	got := summary(t, historyHandler(t, m), nil)

	if got.InFlight == nil || *got.InFlight != 3 {
		t.Errorf("inFlight = %v, want 3 (srv0 now 2, srv1 1, srv2 too old)", got.InFlight)
	}
	if got.Upstreams == nil || got.Upstreams.Healthy != 2 || got.Upstreams.Total != 3 {
		t.Errorf("upstreams = %+v, want 2 healthy of 3", got.Upstreams)
	}
}

func TestSummaryIgnoresMetricsThatAreTooOld(t *testing.T) {
	m := store.NewMemory()
	seedMetrics(t, m, store.MetricSample{
		Name: "caddy_http_requests_in_flight", Labels: `{server="srv0"}`, Time: clock.Add(-10 * time.Minute), Value: 4, Raw: 4,
	})
	if got := summary(t, historyHandler(t, m), nil); got.InFlight != nil {
		t.Errorf("inFlight = %v, want null: the only sample is ten minutes old", *got.InFlight)
	}
}
