package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/upstreamcheck"
)

// fakeHealth answers from a table.
type fakeHealth map[string]upstreamcheck.Result

func (f fakeHealth) Result(address string) (upstreamcheck.Result, bool) {
	r, ok := f[address]
	return r, ok
}

type checkedSiteBody struct {
	Host      string `json:"host"`
	Upstreams []struct {
		Address    string   `json:"address"`
		State      string   `json:"state"`
		HTTPStatus *int     `json:"httpStatus"`
		LatencyMs  *float64 `json:"latencyMs"`
		CheckedAt  *string  `json:"checkedAt"`
	} `json:"upstreams"`
}

type checkedSitesResponse struct {
	Sites         []checkedSiteBody `json:"sites"`
	UpstreamCheck bool              `json:"upstreamCheck"`
}

func healthHandler(f *fakeCaddy, h UpstreamHealth) http.Handler {
	return New(f, Options{Endpoint: testEndpoint, UpstreamHealth: h})
}

func TestSitesCarryTheUpstreamCheck(t *testing.T) {
	checkedAt := time.Date(2026, 11, 6, 14, 2, 11, 0, time.UTC)
	f := &fakeCaddy{cfg: parse(t, twoSitesConfig)}

	t.Run("each upstream has its state, latency and time of the check", func(t *testing.T) {
		h := fakeHealth{
			"10.0.0.5:8080": {State: upstreamcheck.Answering, Latency: 12500 * time.Microsecond, CheckedAt: checkedAt},
			"10.0.0.6:9090": {State: upstreamcheck.Down, CheckedAt: checkedAt},
		}
		var got checkedSitesResponse
		decode(t, do(t, healthHandler(f, h), http.MethodGet, "/api/sites"), &got)

		if !got.UpstreamCheck {
			t.Error("upstreamCheck = false, want true when the check runs")
		}
		up := got.Sites[0].Upstreams[0]
		if up.State != "answering" {
			t.Errorf("state = %q, want answering", up.State)
		}
		if up.LatencyMs == nil || *up.LatencyMs != 12.5 {
			t.Errorf("latencyMs = %v, want 12.5", up.LatencyMs)
		}
		if up.CheckedAt == nil || *up.CheckedAt != "2026-11-06T14:02:11Z" {
			t.Errorf("checkedAt = %v, want 2026-11-06T14:02:11Z", up.CheckedAt)
		}
		down := got.Sites[1].Upstreams[0]
		if down.State != "down" {
			t.Errorf("state = %q, want down", down.State)
		}
		if down.LatencyMs != nil {
			t.Errorf("latencyMs = %v, want none for an upstream that did not answer", *down.LatencyMs)
		}
	})

	t.Run("an upstream that has not been checked yet is unknown", func(t *testing.T) {
		h := fakeHealth{"10.0.0.5:8080": {State: upstreamcheck.Answering, CheckedAt: checkedAt}}
		var got checkedSitesResponse
		decode(t, do(t, healthHandler(f, h), http.MethodGet, "/api/sites"), &got)

		up := got.Sites[1].Upstreams[0]
		if up.State != "unknown" {
			t.Errorf("state = %q, want unknown", up.State)
		}
		if up.CheckedAt != nil || up.LatencyMs != nil {
			t.Errorf("checkedAt = %v, latencyMs = %v, want neither before a check", up.CheckedAt, up.LatencyMs)
		}
	})

	t.Run("an upstream the check could not reach is unknown, with the time it was tried", func(t *testing.T) {
		h := fakeHealth{"10.0.0.5:8080": {State: upstreamcheck.Unknown, CheckedAt: checkedAt}}
		var got checkedSitesResponse
		decode(t, do(t, healthHandler(f, h), http.MethodGet, "/api/sites"), &got)

		up := got.Sites[0].Upstreams[0]
		if up.State != "unknown" || up.CheckedAt == nil {
			t.Errorf("upstream = %+v, want unknown with a checkedAt", up)
		}
	})

	t.Run("with the check off every upstream is unknown and the response says so", func(t *testing.T) {
		var got checkedSitesResponse
		decode(t, do(t, healthHandler(f, nil), http.MethodGet, "/api/sites"), &got)

		if got.UpstreamCheck {
			t.Error("upstreamCheck = true, want false when the check is off")
		}
		for _, s := range got.Sites {
			for _, u := range s.Upstreams {
				if u.State != "unknown" {
					t.Errorf("%s %s: state = %q, want unknown", s.Host, u.Address, u.State)
				}
			}
		}
	})
}

func TestSitesCarryTheStatusTheCheckSaw(t *testing.T) {
	f := &fakeCaddy{cfg: parse(t, twoSitesConfig)}
	h := fakeHealth{
		"10.0.0.5:8080": {State: upstreamcheck.Answering, Status: 404},
		"10.0.0.6:9090": {State: upstreamcheck.Down},
	}
	var got checkedSitesResponse
	decode(t, do(t, healthHandler(f, h), http.MethodGet, "/api/sites"), &got)

	if s := got.Sites[0].Upstreams[0].HTTPStatus; s == nil || *s != 404 {
		t.Errorf("httpStatus = %v, want 404: it is what a native health check of Caddy would judge", s)
	}
	if s := got.Sites[1].Upstreams[0].HTTPStatus; s != nil {
		t.Errorf("httpStatus = %v, want none for an upstream that did not answer", *s)
	}
}
