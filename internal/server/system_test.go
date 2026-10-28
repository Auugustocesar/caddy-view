package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/configmgr"
	"github.com/Auugustocesar/caddy-view/internal/ingest"
	"github.com/Auugustocesar/caddy-view/internal/scrape"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

type fakeIngest struct{ status ingest.Status }

func (f fakeIngest) Status() ingest.Status { return f.status }

type fakeScrape struct{ status scrape.Status }

func (f fakeScrape) Status() scrape.Status { return f.status }

type fakeDatabase struct {
	info DatabaseInfo
	err  error
}

func (f fakeDatabase) Info(context.Context) (DatabaseInfo, error) { return f.info, f.err }

func systemHandlerWith(t *testing.T, caddy *fakeCaddy, opts Options) http.Handler {
	t.Helper()
	opts.Endpoint = testEndpoint
	if opts.Now == nil {
		opts.Now = func() time.Time { return clock }
	}
	return New(caddy, opts)
}

// system asks /api/system and returns the answer as a generic map.
func system(t *testing.T, h http.Handler) map[string]any {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/system")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/system = %d: %s", rec.Code, rec.Body)
	}
	var out map[string]any
	decode(t, rec, &out)
	return out
}

func section(t *testing.T, got map[string]any, name string) map[string]any {
	t.Helper()
	s, ok := got[name].(map[string]any)
	if !ok {
		t.Fatalf("the answer has no %q section: %v", name, got)
	}
	return s
}

func TestSystemWithNothingBehindItReportsCaddyAndNoEditing(t *testing.T) {
	got := system(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{}))
	if c := section(t, got, "caddy"); c["reachable"] != true || c["endpoint"] != testEndpoint {
		t.Errorf("caddy = %v, want reachable at %s", c, testEndpoint)
	}
	if e := section(t, got, "editing"); e["enabled"] != false {
		t.Errorf("editing = %v, want disabled", e)
	}
	for _, name := range []string{"ingestion", "scraper", "database", "retention"} {
		if _, has := got[name]; has {
			t.Errorf("the answer has a %q section without a source for it: %v", name, got[name])
		}
	}
}

func TestSystemSaysWhenCaddyCannotBeReached(t *testing.T) {
	got := system(t, systemHandlerWith(t, &fakeCaddy{cfgErr: errors.New("connection refused")}, Options{}))
	c := section(t, got, "caddy")
	if c["reachable"] != false || c["error"] == "" {
		t.Errorf("caddy = %v, want unreachable with the reason", c)
	}
}

func TestSystemIngestion(t *testing.T) {
	m := store.NewMemory()
	seed(t, m, req("a.example.com", 200, clock.Add(-90*time.Second), time.Millisecond, 1), req("a.example.com", 200, clock.Add(-30*time.Second), time.Millisecond, 1))
	st := ingest.Status{State: ingest.StateFollowing, Path: "/var/log/caddy/access.log", Offset: 4096, SkippedLines: 2, Gaps: 1}

	got := system(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{Ingest: fakeIngest{st}, History: m}))
	i := section(t, got, "ingestion")
	want := map[string]any{
		"state": "following", "file": "/var/log/caddy/access.log", "offset": float64(4096),
		"skippedLines": float64(2), "gaps": float64(1), "newestEntry": clock.Add(-30 * time.Second).Format(time.RFC3339Nano),
	}
	for k, v := range want {
		if i[k] != v {
			t.Errorf("ingestion[%q] = %v, want %v", k, i[k], v)
		}
	}
	if _, has := i["error"]; has {
		t.Errorf("ingestion has an error %v although it works", i["error"])
	}
}

func TestSystemIngestionWithNothingStoredYetHasNoNewestEntry(t *testing.T) {
	st := ingest.Status{State: ingest.StateWaiting, Path: "/var/log/caddy/access.log"}
	got := system(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{Ingest: fakeIngest{st}, History: store.NewMemory()}))
	i := section(t, got, "ingestion")
	if i["state"] != "waiting" || i["newestEntry"] != nil {
		t.Errorf("ingestion = %v, want waiting with no newest entry", i)
	}
}

func TestSystemIngestionShowsItsError(t *testing.T) {
	st := ingest.Status{State: ingest.StateFailing, Path: "/x", Error: "access log: permission denied"}
	got := system(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{Ingest: fakeIngest{st}}))
	if i := section(t, got, "ingestion"); i["state"] != "failing" || i["error"] != "access log: permission denied" {
		t.Errorf("ingestion = %v, want failing with its error", i)
	}
}

func TestSystemScraper(t *testing.T) {
	ok := clock.Add(-10 * time.Second)
	tests := []struct {
		name   string
		status scrape.Status
		want   map[string]any
		absent []string
	}{
		{
			"working", scrape.Status{State: scrape.StateOK, LastSuccess: ok},
			map[string]any{"state": "ok", "lastSuccess": ok.Format(time.RFC3339Nano)}, []string{"error"},
		},
		{
			"never scraped", scrape.Status{State: scrape.StatePending},
			map[string]any{"state": "pending"}, []string{"lastSuccess", "error"},
		},
		{
			"metrics are not enabled in caddy", scrape.Status{State: scrape.StateDisabled, Error: "not found"},
			map[string]any{"state": "disabled", "error": "not found"}, []string{"lastSuccess"},
		},
		{
			"failing after a success", scrape.Status{State: scrape.StateFailing, LastSuccess: ok, Error: "boom"},
			map[string]any{"state": "failing", "error": "boom", "lastSuccess": ok.Format(time.RFC3339Nano)}, nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := system(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{Scrape: fakeScrape{tt.status}}))
			s := section(t, got, "scraper")
			for k, v := range tt.want {
				if s[k] != v {
					t.Errorf("scraper[%q] = %v, want %v", k, s[k], v)
				}
			}
			for _, k := range tt.absent {
				if _, has := s[k]; has {
					t.Errorf("scraper has %q = %v, want it left out", k, s[k])
				}
			}
		})
	}
}

func TestSystemDatabase(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		info := DatabaseInfo{Kind: "sqlite", File: "/data/caddy-view.db", SizeBytes: 123456, SchemaVersion: 4}
		got := system(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{Database: fakeDatabase{info: info}}))
		d := section(t, got, "database")
		want := map[string]any{"kind": "sqlite", "file": "/data/caddy-view.db", "sizeBytes": float64(123456), "schemaVersion": float64(4)}
		for k, v := range want {
			if d[k] != v {
				t.Errorf("database[%q] = %v, want %v", k, d[k], v)
			}
		}
	})
	t.Run("memory has no file and no schema", func(t *testing.T) {
		got := system(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{Database: fakeDatabase{info: DatabaseInfo{Kind: "memory", SizeBytes: 10}}}))
		d := section(t, got, "database")
		if d["kind"] != "memory" || d["file"] != nil || d["schemaVersion"] != nil {
			t.Errorf("database = %v, want memory without a file or a schema version", d)
		}
	})
	t.Run("a failure is the section's own and hides the cause", func(t *testing.T) {
		rec := do(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{Database: fakeDatabase{err: errors.New("disk /dev/sda1 is full")}}), http.MethodGet, "/api/system")
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "sda1") {
			t.Fatalf("status = %d, body %s; want 200 without the cause", rec.Code, rec.Body)
		}
		var got map[string]any
		decode(t, rec, &got)
		if d := section(t, got, "database"); d["error"] == nil || d["error"] == "" {
			t.Errorf("database = %v, want an error", d)
		}
	})
}

func TestSystemRetention(t *testing.T) {
	r := &Retention{Raw: 14 * 24 * time.Hour, Rollups: 90 * 24 * time.Hour, MaxBytes: 1 << 30}
	got := system(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{Retention: r}))
	s := section(t, got, "retention")
	want := map[string]any{"rawSeconds": float64(14 * 86400), "rollupsSeconds": float64(90 * 86400), "maxBytes": float64(1 << 30)}
	for k, v := range want {
		if s[k] != v {
			t.Errorf("retention[%q] = %v, want %v", k, s[k], v)
		}
	}
}

func TestSystemEditing(t *testing.T) {
	t.Run("on, with what Reconcile found", func(t *testing.T) {
		e := newEditor()
		e.drift = configmgr.Drift{Known: true, Drifted: true, Revision: 7, Running: configmgr.RunDiffers}
		got := system(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{Editor: e, CaddyfilePath: "/etc/caddy/Caddyfile"}))
		ed := section(t, got, "editing")
		if ed["enabled"] != true || ed["caddyfile"] != "/etc/caddy/Caddyfile" {
			t.Fatalf("editing = %v, want enabled on /etc/caddy/Caddyfile", ed)
		}
		d := section(t, ed, "drift")
		want := map[string]any{"known": true, "drifted": true, "revision": float64(7), "running": "differs"}
		for k, v := range want {
			if d[k] != v {
				t.Errorf("drift[%q] = %v, want %v", k, d[k], v)
			}
		}
	})
	t.Run("the three answers of Running", func(t *testing.T) {
		for state, want := range map[configmgr.RunState]string{
			configmgr.RunMatches: "matches", configmgr.RunDiffers: "differs", configmgr.RunUnknown: "unknown",
		} {
			e := newEditor()
			e.drift = configmgr.Drift{Running: state}
			got := system(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{Editor: e}))
			if d := section(t, section(t, got, "editing"), "drift"); d["running"] != want {
				t.Errorf("running for %d = %v, want %q", state, d["running"], want)
			}
		}
	})
	t.Run("a failure to compare leaves the drift out and says so", func(t *testing.T) {
		e := newEditor()
		e.driftErr = errors.New("open /etc/caddy/Caddyfile: permission denied")
		rec := do(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{Editor: e}), http.MethodGet, "/api/system")
		if strings.Contains(rec.Body.String(), "permission denied") {
			t.Fatalf("the cause leaked: %s", rec.Body)
		}
		var got map[string]any
		decode(t, rec, &got)
		ed := section(t, got, "editing")
		if _, has := ed["drift"]; has || ed["enabled"] != true || ed["error"] == nil {
			t.Errorf("editing = %v, want enabled, with an error and no drift", ed)
		}
	})
}

func TestSystemOnlyAnswersGETAndKeepsTheHostCheck(t *testing.T) {
	h := systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{})
	if rec := send(t, h, http.MethodPost, "/api/system", "{}", jsonType); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodGet {
		t.Errorf("POST = %d, Allow %q; want 405, Allow GET", rec.Code, rec.Header().Get("Allow"))
	}
	req := httptest.NewRequest(http.MethodGet, "http://evil.example.com/api/system", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a foreign Host = %d, want 403", rec.Code)
	}
}
