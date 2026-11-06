package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/store"
	"github.com/Auugustocesar/caddy-view/internal/upstreamcheck"
)

type attentionItem struct {
	Kind         string   `json:"kind"`
	Severity     string   `json:"severity"`
	Host         string   `json:"host"`
	Down         []string `json:"down"`
	Total        int      `json:"total"`
	Errors5xx    int64    `json:"errors5xx"`
	Rate         *float64 `json:"rate"`
	PreviousRate *float64 `json:"previousRate"`
	P95Ms        *float64 `json:"p95Ms"`
	MedianP95Ms  *float64 `json:"medianP95Ms"`
}

type attentionAnswer struct {
	Window string          `json:"window"`
	Items  []attentionItem `json:"items"`
}

func attentionHandler(t *testing.T, m *store.Memory, c Caddy, health UpstreamHealth) http.Handler {
	t.Helper()
	return New(c, Options{Endpoint: testEndpoint, History: m, UpstreamHealth: health, Now: func() time.Time { return clock }})
}

func getAttention(t *testing.T, h http.Handler, params url.Values) attentionAnswer {
	t.Helper()
	code, body := get(t, h, "/api/attention", params)
	if code != http.StatusOK {
		t.Fatalf("GET /api/attention?%s = %d: %s", params.Encode(), code, body)
	}
	var out attentionAnswer
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, body)
	}
	return out
}

// seedHost adds n requests of a host, errors of them 502, spread over the minute that starts at the given time.
func seedHost(t *testing.T, m *store.Memory, host string, start time.Time, n, errors int, d time.Duration) {
	t.Helper()
	var entries []store.LogEntry
	for i := 0; i < n; i++ {
		status := 200
		if i < errors {
			status = 502
		}
		entries = append(entries, req(host, status, start.Add(time.Duration(i)*time.Millisecond), d, 10))
	}
	seed(t, m, entries...)
}

var (
	thisPeriod = clock.Add(-30 * time.Minute)
	lastPeriod = clock.Add(-90 * time.Minute)
)

func TestAttentionErrorsAreComparedWithThePreviousPeriod(t *testing.T) {
	m := store.NewMemory()
	seedHost(t, m, "api.example.com", thisPeriod, 100, 8, 20*time.Millisecond) // 8%
	seedHost(t, m, "api.example.com", lastPeriod, 100, 1, 20*time.Millisecond) // 1% before
	seedHost(t, m, "ok.example.com", thisPeriod, 100, 0, 20*time.Millisecond)
	h := attentionHandler(t, m, &fakeCaddy{cfg: parse(t, `{}`)}, nil)

	got := getAttention(t, h, nil)

	if got.Window != "1h" {
		t.Errorf("window = %q, want the default 1h", got.Window)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %+v, want one", got.Items)
	}
	it := got.Items[0]
	if it.Kind != "errors_up" || it.Host != "api.example.com" || it.Severity != "bad" || it.Errors5xx != 8 {
		t.Errorf("item = %+v, want errors_up of api.example.com with 8 errors", it)
	}
	if it.Rate == nil || !near(*it.Rate, 0.08) || it.PreviousRate == nil || !near(*it.PreviousRate, 0.01) {
		t.Errorf("rates = %v and %v, want 0.08 and 0.01", it.Rate, it.PreviousRate)
	}
}

func TestAttentionTheSamePeriodTwiceIsNotATrend(t *testing.T) {
	m := store.NewMemory()
	seedHost(t, m, "api.example.com", thisPeriod, 100, 8, 20*time.Millisecond)
	seedHost(t, m, "api.example.com", lastPeriod, 100, 8, 20*time.Millisecond)
	h := attentionHandler(t, m, &fakeCaddy{cfg: parse(t, `{}`)}, nil)

	if got := getAttention(t, h, nil); len(got.Items) != 0 {
		t.Errorf("items = %+v, want none: the errors are as they were", got.Items)
	}
}

func TestAttentionSlowHost(t *testing.T) {
	m := store.NewMemory()
	for _, host := range []string{"a.example.com", "b.example.com"} {
		seedHost(t, m, host, thisPeriod, 100, 0, 20*time.Millisecond)
	}
	seedHost(t, m, "slow.example.com", thisPeriod, 100, 0, 2*time.Second)
	h := attentionHandler(t, m, &fakeCaddy{cfg: parse(t, `{}`)}, nil)

	got := getAttention(t, h, nil)
	if len(got.Items) != 1 || got.Items[0].Kind != "host_slow" || got.Items[0].Host != "slow.example.com" {
		t.Fatalf("items = %+v, want one host_slow for slow.example.com", got.Items)
	}
	if it := got.Items[0]; it.P95Ms == nil || it.MedianP95Ms == nil || *it.P95Ms <= *it.MedianP95Ms {
		t.Errorf("numbers = p95 %v, median %v; want both, p95 above the median", it.P95Ms, it.MedianP95Ms)
	}
}

func TestAttentionUpstreamsThatAreDown(t *testing.T) {
	m := store.NewMemory()
	health := fakeHealth{
		"10.0.0.5:8080": {State: upstreamcheck.Down},
		"10.0.0.6:9090": {State: upstreamcheck.Answering},
	}
	t.Run("a site with its only upstream down, and Caddy without health checks", func(t *testing.T) {
		h := attentionHandler(t, m, &fakeCaddy{cfg: parse(t, twoSitesConfig)}, health)
		got := getAttention(t, h, nil)

		if len(got.Items) != 2 {
			t.Fatalf("items = %+v, want upstream_down and no_health_checks", got.Items)
		}
		down := got.Items[0]
		if down.Kind != "upstream_down" || down.Host != "a.example.com" || down.Severity != "bad" || down.Total != 1 ||
			len(down.Down) != 1 || down.Down[0] != "10.0.0.5:8080" {
			t.Errorf("first item = %+v, want a.example.com down, 1 of 1", down)
		}
		if got.Items[1].Kind != "no_health_checks" || got.Items[1].Severity != "info" {
			t.Errorf("second item = %+v, want no_health_checks as info", got.Items[1])
		}
	})
	t.Run("Caddy with health checks is not told off", func(t *testing.T) {
		cfg := `{"apps":{"http":{"servers":{"srv0":{"routes":[
			{"match":[{"host":["a.example.com"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.5:8080"}],"health_checks":{"active":{"uri":"/"}}}]}
		]}}}}}`
		got := getAttention(t, attentionHandler(t, m, &fakeCaddy{cfg: parse(t, cfg)}, health), nil)
		if len(got.Items) != 1 || got.Items[0].Kind != "upstream_down" {
			t.Errorf("items = %+v, want only upstream_down", got.Items)
		}
	})
	t.Run("with the check off there is nothing to say about upstreams", func(t *testing.T) {
		got := getAttention(t, attentionHandler(t, m, &fakeCaddy{cfg: parse(t, twoSitesConfig)}, nil), nil)
		if len(got.Items) != 0 {
			t.Errorf("items = %+v, want none", got.Items)
		}
	})
	t.Run("when Caddy cannot be read the traffic items are still answered", func(t *testing.T) {
		mm := store.NewMemory()
		seedHost(t, mm, "api.example.com", thisPeriod, 100, 9, 20*time.Millisecond)
		c := &fakeCaddy{cfgErr: fmt.Errorf("%w: refused", caddy.ErrUnreachable)}
		got := getAttention(t, attentionHandler(t, mm, c, health), nil)
		if len(got.Items) != 1 || got.Items[0].Kind != "errors_up" {
			t.Errorf("items = %+v, want the errors_up item alone", got.Items)
		}
	})
}

func TestAttentionNoItemsIsAnEmptyListNotNull(t *testing.T) {
	h := attentionHandler(t, store.NewMemory(), &fakeCaddy{cfg: parse(t, `{}`)}, nil)
	_, body := get(t, h, "/api/attention", nil)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["items"]) != "[]" {
		t.Errorf("items = %s, want []", raw["items"])
	}
}

func TestAttentionWindows(t *testing.T) {
	m := store.NewMemory()
	// 5 hours ago errors, 11 hours ago almost none: visible in 6h and 24h, not in 1h.
	seedHost(t, m, "api.example.com", clock.Add(-5*time.Hour), 100, 9, 20*time.Millisecond)
	h := attentionHandler(t, m, &fakeCaddy{cfg: parse(t, `{}`)}, nil)

	if got := getAttention(t, h, url.Values{"window": {"1h"}}); len(got.Items) != 0 {
		t.Errorf("1h items = %+v, want none", got.Items)
	}
	got := getAttention(t, h, url.Values{"window": {"6h"}})
	if got.Window != "6h" || len(got.Items) != 1 {
		t.Errorf("6h = %+v, want the errors", got)
	}
}

func TestAttentionRefusesAnUnknownWindow(t *testing.T) {
	h := attentionHandler(t, store.NewMemory(), &fakeCaddy{cfg: parse(t, `{}`)}, nil)
	if code, _ := get(t, h, "/api/attention", url.Values{"window": {"2y"}}); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestAttentionStoreFailureIs500(t *testing.T) {
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint, History: failingHistory{}, Now: func() time.Time { return clock }})
	if code, _ := get(t, h, "/api/attention", nil); code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", code)
	}
}
