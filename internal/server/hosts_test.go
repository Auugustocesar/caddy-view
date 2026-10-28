package server

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

type hostAnswer struct {
	Host      string           `json:"host"`
	Requests  int64            `json:"requests"`
	Bytes     int64            `json:"bytes"`
	Errors5xx int64            `json:"errors5xx"`
	Status    map[string]int64 `json:"status"`
	AvgMs     *float64         `json:"avgMs"`
	P95Ms     *float64         `json:"p95Ms"`
	MaxMs     *float64         `json:"maxMs"`
}

func hosts(t *testing.T, h http.Handler, params url.Values) []hostAnswer {
	t.Helper()
	code, body := get(t, h, "/api/hosts", params)
	if code != http.StatusOK {
		t.Fatalf("GET /api/hosts?%s = %d: %s", params.Encode(), code, body)
	}
	var out struct {
		Hosts []hostAnswer `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, body)
	}
	if out.Hosts == nil {
		t.Fatalf("hosts is null in %s, want a list", body)
	}
	return out.Hosts
}

func TestHostsTotalsEachHostOfTheLastHour(t *testing.T) {
	m := store.NewMemory()
	seedBusyMinute(t, m)
	got := hosts(t, historyHandler(t, m), nil)

	if len(got) != 2 {
		t.Fatalf("got %d hosts, want 2: %+v", len(got), got)
	}
	a, b := got[0], got[1]
	if a.Host != "a.example.com" || a.Requests != 3 || a.Bytes != 151 || a.Errors5xx != 0 {
		t.Errorf("host a = %+v, want 3 requests, 151 bytes, no 5xx", a)
	}
	if want := map[string]int64{"1xx": 0, "2xx": 2, "3xx": 0, "4xx": 1, "5xx": 0, "other": 0}; len(a.Status) != len(want) || a.Status["2xx"] != 2 || a.Status["4xx"] != 1 {
		t.Errorf("host a status = %v, want %v", a.Status, want)
	}
	// The durations are kept in microseconds, so the average is exact to a microsecond.
	if a.AvgMs == nil || math.Abs(*a.AvgMs-55.0/3) > 0.001 || a.MaxMs == nil || !near(*a.MaxMs, 30) || a.P95Ms == nil || !near(*a.P95Ms, 50) {
		t.Errorf("host a latency = avg %v, p95 %v, max %v, want 18.33, 50 (the bound that holds 30 ms) and 30", a.AvgMs, a.P95Ms, a.MaxMs)
	}
	if b.Host != "b.example.com" || b.Requests != 1 || b.Bytes != 7 || b.Errors5xx != 1 || b.Status["5xx"] != 1 {
		t.Errorf("host b = %+v, want one 5xx request of 7 bytes", b)
	}
	if b.AvgMs == nil || !near(*b.AvgMs, 3000) || b.P95Ms == nil || !near(*b.P95Ms, 5000) {
		t.Errorf("host b latency = avg %v, p95 %v, want 3000 and 5000", b.AvgMs, b.P95Ms)
	}
}

func TestHostsByHourDefaultsToTheLastDay(t *testing.T) {
	m := store.NewMemory()
	seedBusyMinute(t, m)
	// A request 5 hours ago is outside the default hour, and inside the default day.
	seed(t, m, req("c.example.com", 200, clock.Add(-5*time.Hour), time.Millisecond, 9))
	h := historyHandler(t, m)

	if got := hosts(t, h, nil); len(got) != 2 {
		t.Errorf("the default (minutes, the last hour) gave %+v, want the two recent hosts", got)
	}
	got := hosts(t, h, url.Values{"resolution": {"hour"}})
	if len(got) != 3 {
		t.Fatalf("hours gave %+v, want the three hosts of the last day", got)
	}
}

func TestHostsFollowsTheRangeItIsGiven(t *testing.T) {
	m := store.NewMemory()
	seedBusyMinute(t, m)
	h := historyHandler(t, m)

	before := url.Values{"from": {rfc(clock.Add(-2 * time.Hour))}, "to": {rfc(clock.Add(-time.Hour))}}
	if got := hosts(t, h, before); len(got) != 0 {
		t.Errorf("a range before the traffic gave %+v, want an empty list", got)
	}
	around := url.Values{"from": {rfc(clock.Add(-31 * time.Minute))}, "to": {rfc(clock.Add(-29 * time.Minute))}}
	if got := hosts(t, h, around); len(got) != 2 {
		t.Errorf("a range around the traffic gave %+v, want both hosts", got)
	}
}

func TestHostsCanBeLimited(t *testing.T) {
	m := store.NewMemory()
	seedBusyMinute(t, m)
	got := hosts(t, historyHandler(t, m), url.Values{"limit": {"1"}})
	if len(got) != 1 || got[0].Host != "a.example.com" {
		t.Errorf("limit 1 gave %+v, want only the busiest host", got)
	}
}

func TestHostsRefusesWhatItCannotRead(t *testing.T) {
	h := historyHandler(t, store.NewMemory())
	tests := []struct {
		name   string
		params url.Values
		names  string
	}{
		{"unknown resolution", url.Values{"resolution": {"day"}}, "resolution"},
		{"limit that is not a number", url.Values{"limit": {"many"}}, "limit"},
		{"negative limit", url.Values{"limit": {"-1"}}, "limit"},
		{"from that is not a time", url.Values{"from": {"yesterday"}}, "from"},
		{"to that is not a time", url.Values{"to": {"2026-13-45"}}, "to"},
		{"from after to", url.Values{"from": {rfc(clock)}, "to": {rfc(clock.Add(-time.Hour))}}, "from"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := get(t, h, "/api/hosts", tt.params)
			if code != http.StatusBadRequest || !strings.Contains(body, tt.names) {
				t.Errorf("GET = %d %s, want 400 naming %q", code, body, tt.names)
			}
		})
	}
}

func TestHostsHidesTheCauseOfAStoreFailure(t *testing.T) {
	code, body := get(t, historyHandler(t, failingHistory{}), "/api/hosts", nil)
	if code != http.StatusInternalServerError || strings.Contains(body, errStore.Error()) {
		t.Errorf("GET = %d %s, want 500 without the cause", code, body)
	}
}

func TestHostsIsAbsentWithoutAHistory(t *testing.T) {
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint})
	if code, _ := get(t, h, "/api/hosts", nil); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

func TestHostsOnlyAnswersGET(t *testing.T) {
	rec := postJSON(t, historyHandler(t, store.NewMemory()), "/api/hosts", `{}`)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET" {
		t.Errorf("POST = %d, Allow %q, want 405 and GET", rec.Code, rec.Header().Get("Allow"))
	}
}
