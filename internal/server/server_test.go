package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
)

const testEndpoint = "unix:///run/caddy/admin.sock"

type fakeCaddy struct {
	cfg    map[string]any
	cfgErr error
	ups    []caddy.Upstream
	upsErr error
	calls  atomic.Int32
}

func (f *fakeCaddy) Config(context.Context) (map[string]any, error) {
	f.calls.Add(1)
	return f.cfg, f.cfgErr
}

func (f *fakeCaddy) Upstreams(context.Context) ([]caddy.Upstream, error) {
	f.calls.Add(1)
	return f.ups, f.upsErr
}

func parse(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("invalid test JSON: %v", err)
	}
	return m
}

const twoSitesConfig = `{"apps":{"http":{"servers":{"srv0":{"routes":[
	{"match":[{"host":["a.example.com"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.5:8080"}]}]},
	{"match":[{"host":["b.example.com"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.6:9090"}]}]}
]}}}}}`

func newHandler(f *fakeCaddy) http.Handler {
	return New(f, Options{Endpoint: testEndpoint})
}

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://localhost:8484"+path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("response is not valid JSON: %v\nbody: %s", err, rec.Body.String())
	}
}

func TestStatus(t *testing.T) {
	t.Run("reachable", func(t *testing.T) {
		rec := do(t, newHandler(&fakeCaddy{cfg: parse(t, `{}`)}), http.MethodGet, "/api/status")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got struct {
			Caddy struct {
				Reachable bool   `json:"reachable"`
				Endpoint  string `json:"endpoint"`
				Error     string `json:"error"`
			} `json:"caddy"`
		}
		decode(t, rec, &got)
		if !got.Caddy.Reachable || got.Caddy.Endpoint != testEndpoint || got.Caddy.Error != "" {
			t.Errorf("caddy = %+v, want reachable at %s with no error", got.Caddy, testEndpoint)
		}
	})
	t.Run("unreachable is reported, not an HTTP error", func(t *testing.T) {
		f := &fakeCaddy{cfgErr: fmt.Errorf("%w: connection refused", caddy.ErrUnreachable)}
		rec := do(t, newHandler(f), http.MethodGet, "/api/status")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got struct {
			Caddy struct {
				Reachable bool   `json:"reachable"`
				Error     string `json:"error"`
			} `json:"caddy"`
		}
		decode(t, rec, &got)
		if got.Caddy.Reachable || got.Caddy.Error == "" {
			t.Errorf("caddy = %+v, want unreachable with an error message", got.Caddy)
		}
	})
	t.Run("any other failure carries the cause", func(t *testing.T) {
		rec := do(t, newHandler(&fakeCaddy{cfgErr: errors.New("boom")}), http.MethodGet, "/api/status")
		var got struct {
			Caddy struct {
				Reachable bool   `json:"reachable"`
				Error     string `json:"error"`
			} `json:"caddy"`
		}
		decode(t, rec, &got)
		if got.Caddy.Reachable || !strings.Contains(got.Caddy.Error, "boom") {
			t.Errorf("caddy = %+v, want unreachable with the cause", got.Caddy)
		}
	})
}

func TestConfig(t *testing.T) {
	t.Run("returns the running configuration", func(t *testing.T) {
		cfg := parse(t, `{"apps":{"http":{"servers":{"srv0":{"listen":[":443"]}}}}}`)
		rec := do(t, newHandler(&fakeCaddy{cfg: cfg}), http.MethodGet, "/api/config")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got map[string]any
		decode(t, rec, &got)
		if !reflect.DeepEqual(got, cfg) {
			t.Errorf("config = %v, want %v", got, cfg)
		}
	})
	t.Run("unreadable config is a bad gateway with a message", func(t *testing.T) {
		for name, err := range map[string]error{
			"unreachable": fmt.Errorf("%w: refused", caddy.ErrUnreachable),
			"api error":   &caddy.APIError{Status: 500, Message: "boom"},
		} {
			rec := do(t, newHandler(&fakeCaddy{cfgErr: err}), http.MethodGet, "/api/config")
			if rec.Code != http.StatusBadGateway {
				t.Errorf("%s: status = %d, want 502", name, rec.Code)
			}
			var body struct {
				Error string `json:"error"`
			}
			decode(t, rec, &body)
			if body.Error == "" {
				t.Errorf("%s: response has no error message", name)
			}
		}
	})
}

type siteBody struct {
	Server    string `json:"server"`
	Host      string `json:"host"`
	Upstreams []struct {
		Address     string `json:"address"`
		NumRequests int    `json:"numRequests"`
		Fails       int    `json:"fails"`
	} `json:"upstreams"`
}

type sitesResponse struct {
	Sites              []siteBody `json:"sites"`
	UpstreamsAvailable bool       `json:"upstreamsAvailable"`
}

func TestSites(t *testing.T) {
	t.Run("sites carry the live state of their upstreams", func(t *testing.T) {
		f := &fakeCaddy{
			cfg: parse(t, twoSitesConfig),
			ups: []caddy.Upstream{
				{Address: "10.0.0.5:8080", NumRequests: 3, Fails: 1},
				{Address: "10.0.0.6:9090", NumRequests: 0, Fails: 0},
			},
		}
		rec := do(t, newHandler(f), http.MethodGet, "/api/sites")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got sitesResponse
		decode(t, rec, &got)
		if !got.UpstreamsAvailable {
			t.Error("upstreamsAvailable = false, want true")
		}
		if len(got.Sites) != 2 {
			t.Fatalf("got %d sites, want 2: %+v", len(got.Sites), got.Sites)
		}
		a := got.Sites[0]
		if a.Server != "srv0" || a.Host != "a.example.com" || len(a.Upstreams) != 1 {
			t.Fatalf("first site = %+v", a)
		}
		if u := a.Upstreams[0]; u.Address != "10.0.0.5:8080" || u.NumRequests != 3 || u.Fails != 1 {
			t.Errorf("first site upstream = %+v, want 10.0.0.5:8080 with 3 requests and 1 fail", u)
		}
		if got.Sites[1].Host != "b.example.com" {
			t.Errorf("second site host = %q, want b.example.com", got.Sites[1].Host)
		}
	})
	t.Run("an upstream missing from Caddy's list has zero counters", func(t *testing.T) {
		f := &fakeCaddy{cfg: parse(t, twoSitesConfig), ups: []caddy.Upstream{{Address: "10.0.0.6:9090", NumRequests: 7}}}
		var got sitesResponse
		decode(t, do(t, newHandler(f), http.MethodGet, "/api/sites"), &got)
		u := got.Sites[0].Upstreams[0]
		if u.Address != "10.0.0.5:8080" || u.NumRequests != 0 || u.Fails != 0 {
			t.Errorf("upstream = %+v, want address kept and zero counters", u)
		}
	})
	t.Run("sites are still returned when upstream state is unavailable", func(t *testing.T) {
		f := &fakeCaddy{cfg: parse(t, twoSitesConfig), upsErr: errors.New("boom")}
		rec := do(t, newHandler(f), http.MethodGet, "/api/sites")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got sitesResponse
		decode(t, rec, &got)
		if got.UpstreamsAvailable {
			t.Error("upstreamsAvailable = true, want false")
		}
		if len(got.Sites) != 2 || got.Sites[0].Upstreams[0].Address != "10.0.0.5:8080" {
			t.Errorf("sites = %+v, want both sites with their addresses", got.Sites)
		}
	})
	t.Run("no sites is an empty list, not null", func(t *testing.T) {
		rec := do(t, newHandler(&fakeCaddy{cfg: parse(t, `{}`)}), http.MethodGet, "/api/sites")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"sites":[]`) {
			t.Errorf("body = %s, want \"sites\":[]", rec.Body.String())
		}
	})
	t.Run("a site with no upstreams has an empty list, not null", func(t *testing.T) {
		cfg := parse(t, `{"apps":{"http":{"servers":{"srv0":{"routes":[{"match":[{"host":["s.example.com"]}],"handle":[]}]}}}}}`)
		rec := do(t, newHandler(&fakeCaddy{cfg: cfg}), http.MethodGet, "/api/sites")
		if !strings.Contains(rec.Body.String(), `"upstreams":[]`) {
			t.Errorf("body = %s, want \"upstreams\":[]", rec.Body.String())
		}
	})
	t.Run("unreadable config is a bad gateway", func(t *testing.T) {
		rec := do(t, newHandler(&fakeCaddy{cfgErr: fmt.Errorf("%w: refused", caddy.ErrUnreachable)}), http.MethodGet, "/api/sites")
		if rec.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", rec.Code)
		}
	})
}

func TestEveryRouteIsReadOnly(t *testing.T) {
	for _, path := range []string{"/api/status", "/api/config", "/api/sites"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			t.Run(method+" "+path, func(t *testing.T) {
				f := &fakeCaddy{cfg: parse(t, `{}`)}
				rec := do(t, newHandler(f), method, path)
				if rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("status = %d, want 405", rec.Code)
				}
				if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
					t.Errorf("Allow = %q, want GET", allow)
				}
				if n := f.calls.Load(); n != 0 {
					t.Errorf("Caddy was called %d times for a rejected method, want 0", n)
				}
			})
		}
	}
}

func TestUnknownRoutes(t *testing.T) {
	for _, path := range []string{"/api/nope", "/api", "/api/config/extra", "/api/sites/"} {
		t.Run(path, func(t *testing.T) {
			rec := do(t, newHandler(&fakeCaddy{cfg: parse(t, `{}`)}), http.MethodGet, path)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			var body struct {
				Error string `json:"error"`
			}
			decode(t, rec, &body)
			if body.Error == "" {
				t.Error("404 response has no error message")
			}
		})
	}
}

func TestResponseHeaders(t *testing.T) {
	cases := map[string]func() *httptest.ResponseRecorder{
		"success": func() *httptest.ResponseRecorder {
			return do(t, newHandler(&fakeCaddy{cfg: parse(t, `{}`)}), http.MethodGet, "/api/config")
		},
		"bad gateway": func() *httptest.ResponseRecorder {
			return do(t, newHandler(&fakeCaddy{cfgErr: errors.New("boom")}), http.MethodGet, "/api/config")
		},
		"not found": func() *httptest.ResponseRecorder {
			return do(t, newHandler(&fakeCaddy{}), http.MethodGet, "/api/nope")
		},
		"method not allowed": func() *httptest.ResponseRecorder {
			return do(t, newHandler(&fakeCaddy{}), http.MethodPost, "/api/config")
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			h := run().Header()
			if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			if cc := h.Get("Cache-Control"); cc != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", cc)
			}
			if xc := h.Get("X-Content-Type-Options"); xc != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", xc)
			}
		})
	}
}

func TestHostCheck(t *testing.T) {
	tests := []struct {
		host string
		want int
	}{
		{"localhost:8484", http.StatusOK},
		{"localhost", http.StatusOK},
		{"127.0.0.1:8484", http.StatusOK},
		{"127.0.0.1", http.StatusOK},
		{"[::1]:8484", http.StatusOK},
		{"[::1]", http.StatusOK},
		{"LOCALHOST:8484", http.StatusOK},
		{"evil.example.com", http.StatusForbidden},
		{"evil.example.com:8484", http.StatusForbidden},
		{"192.168.50.10:8484", http.StatusForbidden},
		{"localhost.evil.com", http.StatusForbidden},
		{"notlocalhost:8484", http.StatusForbidden},
		{"127.0.0.1.evil.com", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			h := newHandler(&fakeCaddy{cfg: parse(t, `{}`)})
			req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Errorf("Host %q: status = %d, want %d", tt.host, rec.Code, tt.want)
			}
		})
	}

	t.Run("the host check runs before routing and before the method check", func(t *testing.T) {
		f := &fakeCaddy{cfg: parse(t, `{}`)}
		h := newHandler(f)
		for _, c := range []struct{ method, path string }{
			{http.MethodPost, "/api/config"},
			{http.MethodGet, "/api/nope"},
			{http.MethodGet, "/api/config"},
		} {
			req := httptest.NewRequest(c.method, c.path, nil)
			req.Host = "evil.example.com"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s with a foreign Host: status = %d, want 403", c.method, c.path, rec.Code)
			}
		}
		if n := f.calls.Load(); n != 0 {
			t.Errorf("Caddy was called %d times for a rejected host, want 0", n)
		}
	})
}
