package scrape_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/promtext"
	"github.com/Auugustocesar/caddy-view/internal/scrape"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

const (
	waitFor  = 5 * time.Second
	interval = 5 * time.Millisecond

	reqs     = "caddy_http_requests_total"
	inFlight = "caddy_http_requests_in_flight"
	durCount = "caddy_http_request_duration_seconds_count"
	durSum   = "caddy_http_request_duration_seconds_sum"
	healthy  = "caddy_reverse_proxy_upstreams_healthy"
	reload   = "caddy_config_last_reload_successful"
)

var (
	errBoom = errors.New("caddy is on fire")
	t0      = time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
)

// fakeSource serves a body that the test changes between scrapes.
type fakeSource struct {
	mu   sync.Mutex
	body string
	err  error
}

func (f *fakeSource) Metrics(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []byte(f.body), f.err
}

func (f *fakeSource) set(body string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.body, f.err = body, err
}

// flakyStore is a memory store that can be made to fail, and that reports every successful write.
type flakyStore struct {
	*store.Memory

	mu         sync.Mutex
	failAppend int
	failLast   int
	appended   chan struct{}
}

func newStore() *flakyStore {
	return &flakyStore{Memory: store.NewMemory(), appended: make(chan struct{}, 1000)}
}

func (f *flakyStore) AppendMetrics(ctx context.Context, s []store.MetricSample) error {
	f.mu.Lock()
	fail := f.failAppend > 0
	if fail {
		f.failAppend--
	}
	f.mu.Unlock()
	if fail {
		return errBoom
	}
	err := f.Memory.AppendMetrics(ctx, s)
	if err == nil {
		f.appended <- struct{}{}
	}
	return err
}

func (f *flakyStore) LastMetrics(ctx context.Context) (map[string]store.MetricSample, error) {
	f.mu.Lock()
	fail := f.failLast > 0
	if fail {
		f.failLast--
	}
	f.mu.Unlock()
	if fail {
		return nil, errBoom
	}
	return f.Memory.LastMetrics(ctx)
}

// clock gives a time that the test advances by hand.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newScraper(src scrape.Source, st scrape.Store, c *clock) *scrape.Scraper {
	return scrape.New(src, st, scrape.Options{Now: c.Now, Interval: interval})
}

func mustOnce(t *testing.T, s *scrape.Scraper) {
	t.Helper()
	if err := s.Once(context.Background()); err != nil {
		t.Fatalf("Once: %v", err)
	}
}

func query(t *testing.T, st *flakyStore, name, labels string) []store.MetricSample {
	t.Helper()
	got, err := st.QueryMetrics(context.Background(), store.MetricQuery{Name: name, Labels: labels})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// only returns the single sample of a series, failing the test when there is not exactly one.
func only(t *testing.T, st *flakyStore, name, labels string) store.MetricSample {
	t.Helper()
	got := query(t, st, name, labels)
	if len(got) != 1 {
		t.Fatalf("%s%s has %d samples, want 1: %+v", name, labels, len(got), got)
	}
	return got[0]
}

// body renders an exposition from lines, adding the comments a real one has.
func body(lines ...string) string {
	return "# HELP x A help text.\n# TYPE x counter\n" + strings.Join(lines, "\n") + "\n"
}

func line(name string, v float64, labels ...string) string {
	return fmt.Sprintf("%s{%s} %v", name, strings.Join(labels, ","), v)
}

func TestFirstScrapeStoresGaugesAndCounterBaselines(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	src.set(body(
		line(reqs, 10, `handler="subroute"`, `server="srv0"`),
		line(inFlight, 2, `handler="subroute"`, `server="srv0"`),
		line(healthy, 1, `upstream="10.0.0.5:8000"`),
		reload+" 1",
		"go_goroutines 42",
	), nil)

	mustOnce(t, newScraper(src, st, c))

	got := only(t, st, reqs, `{server="srv0"}`)
	if got.Raw != 10 || got.Value != 0 || !got.Time.Equal(t0) {
		t.Errorf("counter baseline = %+v, want raw 10, an increase of 0 (nothing to compare with) at %v", got, t0)
	}
	if g := only(t, st, inFlight, `{server="srv0"}`); g.Value != 2 || g.Raw != 2 {
		t.Errorf("gauge = %+v, want value and raw 2", g)
	}
	if g := only(t, st, healthy, `{upstream="10.0.0.5:8000"}`); g.Value != 1 {
		t.Errorf("upstream health = %+v, want 1", g)
	}
	if g := only(t, st, reload, ""); g.Value != 1 {
		t.Errorf("last reload = %+v, want 1", g)
	}
	if got := query(t, st, "go_goroutines", ""); len(got) != 0 {
		t.Errorf("a series the UI does not use was stored: %+v", got)
	}
}

func TestCountersStoreTheIncreaseSincePreviousScrape(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	s := newScraper(src, st, c)

	src.set(body(line(reqs, 15, `handler="subroute"`, `server="srv0"`)), nil)
	mustOnce(t, s)
	c.advance(15 * time.Second)
	src.set(body(line(reqs, 40, `handler="subroute"`, `server="srv0"`)), nil)
	mustOnce(t, s)

	got := query(t, st, reqs, `{server="srv0"}`)
	if len(got) != 2 || got[1].Value != 25 || got[1].Raw != 40 || !got[1].Time.Equal(t0.Add(15*time.Second)) {
		t.Errorf("samples = %+v, want a second one with an increase of 25, raw 40, 15 s later", got)
	}
}

func TestACounterThatGoesDownMeansCaddyRestartedAndCountsFromTheNewValue(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	s := newScraper(src, st, c)

	src.set(body(line(reqs, 100, `server="srv0"`)), nil)
	mustOnce(t, s)
	c.advance(time.Minute)
	src.set(body(line(reqs, 7, `server="srv0"`)), nil)
	mustOnce(t, s)
	c.advance(time.Minute)
	src.set(body(line(reqs, 9, `server="srv0"`)), nil)
	mustOnce(t, s)

	got := query(t, st, reqs, `{server="srv0"}`)
	if len(got) != 3 || got[1].Value != 7 || got[2].Value != 2 {
		t.Errorf("samples = %+v, want increases 0, 7 (reset: the new value) and 2", got)
	}
}

func TestCountersAreSummedAcrossHandlersAndMethods(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	src.set(body(
		line(reqs, 10, `handler="subroute"`, `server="srv0"`),
		line(reqs, 5, `handler="file_server"`, `server="srv0"`),
		line(reqs, 100, `handler="subroute"`, `server="srv1"`),
	), nil)
	mustOnce(t, newScraper(src, st, c))

	if got := only(t, st, reqs, `{server="srv0"}`); got.Raw != 15 {
		t.Errorf("srv0 raw = %v, want 15 (10 + 5)", got.Raw)
	}
	if got := only(t, st, reqs, `{server="srv1"}`); got.Raw != 100 {
		t.Errorf("srv1 raw = %v, want 100", got.Raw)
	}
}

func TestDurationCountAndSumAreGroupedByStatusClass(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	lab := func(code, method string) []string {
		return []string{`code="` + code + `"`, `handler="subroute"`, `method="` + method + `"`, `server="srv0"`}
	}
	src.set(body(
		line(durCount, 3, lab("200", "GET")...),
		line(durCount, 4, lab("204", "POST")...),
		line(durCount, 1, lab("101", "GET")...),
		line(durCount, 2, lab("404", "GET")...),
		line(durCount, 5, lab("502", "GET")...),
		line(durCount, 6, lab("weird", "GET")...),
		line(durSum, 0.5, lab("200", "GET")...),
		line(durSum, 0.25, lab("204", "POST")...),
	), nil)
	mustOnce(t, newScraper(src, st, c))

	for _, tt := range []struct {
		name, class string
		want        float64
	}{
		{durCount, "2xx", 7}, {durCount, "1xx", 1}, {durCount, "4xx", 2}, {durCount, "5xx", 5}, {durCount, "other", 6}, {durSum, "2xx", 0.75},
	} {
		labels := promtext.FormatLabels(map[string]string{"code": tt.class, "server": "srv0"})
		if got := only(t, st, tt.name, labels); got.Raw != tt.want {
			t.Errorf("%s%s raw = %v, want %v", tt.name, labels, got.Raw, tt.want)
		}
	}
}

func TestGaugesAreSummedPerServerButUpstreamsStayApart(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	src.set(body(
		line(inFlight, 2, `handler="a"`, `server="srv0"`),
		line(inFlight, 3, `handler="b"`, `server="srv0"`),
		line(healthy, 1, `upstream="a:1"`),
		line(healthy, 0, `upstream="b:2"`),
	), nil)
	mustOnce(t, newScraper(src, st, c))

	if got := only(t, st, inFlight, `{server="srv0"}`); got.Value != 5 {
		t.Errorf("in flight = %v, want 5", got.Value)
	}
	if got := only(t, st, healthy, `{upstream="a:1"}`); got.Value != 1 {
		t.Errorf("upstream a = %v, want 1", got.Value)
	}
	if got := only(t, st, healthy, `{upstream="b:2"}`); got.Value != 0 {
		t.Errorf("upstream b = %v, want 0", got.Value)
	}
}

func TestASeriesThatAppearsLaterCountsFromZero(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	s := newScraper(src, st, c)

	src.set(body(line(reqs, 10, `server="srv0"`)), nil)
	mustOnce(t, s)
	c.advance(time.Minute)
	// srv1 did not exist at the first scrape: everything it counted is new traffic.
	src.set(body(line(reqs, 10, `server="srv0"`), line(reqs, 4, `server="srv1"`)), nil)
	mustOnce(t, s)

	if got := only(t, st, reqs, `{server="srv1"}`); got.Value != 4 {
		t.Errorf("a new series has an increase of %v, want 4", got.Value)
	}
	if got := query(t, st, reqs, `{server="srv0"}`); got[1].Value != 0 {
		t.Errorf("an unchanged series has an increase of %v, want 0", got[1].Value)
	}
}

func TestACounterContinuesAcrossARestartOfCaddyView(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	src.set(body(line(reqs, 15, `server="srv0"`)), nil)
	mustOnce(t, newScraper(src, st, c))

	// A new Scraper is what a restarted process has: only the store remembers the last raw value.
	c.advance(time.Minute)
	src.set(body(line(reqs, 20, `server="srv0"`)), nil)
	mustOnce(t, newScraper(src, st, c))

	if got := query(t, st, reqs, `{server="srv0"}`); len(got) != 2 || got[1].Value != 5 {
		t.Errorf("samples = %+v, want the second increase to be 5, not a new baseline", got)
	}
}

func TestNonFiniteValuesAreNotStored(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	src.set(body(
		fmt.Sprintf(`%s{server="srv0"} NaN`, inFlight),
		fmt.Sprintf(`%s{server="srv1"} +Inf`, inFlight),
		line(inFlight, 1, `server="srv2"`),
	), nil)
	mustOnce(t, newScraper(src, st, c))

	got := query(t, st, inFlight, "")
	if len(got) != 1 || math.IsNaN(got[0].Value) || got[0].Labels != `{server="srv2"}` {
		t.Errorf("stored %+v, want only the finite srv2 sample", got)
	}
}

func TestAFailingSourceStoresNothing(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	src.set("", errBoom)
	if err := newScraper(src, st, c).Once(context.Background()); !errors.Is(err, errBoom) {
		t.Errorf("Once: error = %v, want the source's error", err)
	}
	if got := query(t, st, reqs, ""); len(got) != 0 {
		t.Errorf("stored %+v after a failed scrape", got)
	}
}

func TestMalformedTextStoresNothing(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	src.set(body(line(reqs, 1, `server="srv0"`), "this line is broken"), nil)
	if err := newScraper(src, st, c).Once(context.Background()); !errors.Is(err, promtext.ErrSyntax) {
		t.Errorf("Once: error = %v, want ErrSyntax", err)
	}
	if got := query(t, st, reqs, ""); len(got) != 0 {
		t.Errorf("stored %+v from a malformed exposition", got)
	}
}

func TestAnExpositionWithoutCaddyMetricsIsNotAnError(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	src.set(body("go_goroutines 42"), nil)
	mustOnce(t, newScraper(src, st, c)) // Caddy without the metrics option: the doctor explains it, the scraper just waits
	if last, _ := st.LastMetrics(context.Background()); len(last) != 0 {
		t.Errorf("stored %v, want nothing", last)
	}
}

func TestAFailedWriteDoesNotMoveTheBaseline(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	s := newScraper(src, st, c)

	src.set(body(line(reqs, 10, `server="srv0"`)), nil)
	mustOnce(t, s)

	c.advance(time.Minute)
	src.set(body(line(reqs, 15, `server="srv0"`)), nil)
	st.failAppend = 1
	if err := s.Once(context.Background()); !errors.Is(err, errBoom) {
		t.Fatalf("Once: error = %v, want the store's error", err)
	}

	c.advance(time.Minute)
	src.set(body(line(reqs, 18, `server="srv0"`)), nil)
	mustOnce(t, s)

	got := query(t, st, reqs, `{server="srv0"}`)
	if len(got) != 2 || got[1].Value != 8 {
		t.Errorf("samples = %+v, want the last increase to be 8 (18 - 10): the lost scrape is not forgotten", got)
	}
}

func TestAFailedLoadOfThePreviousStateIsRetried(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	src.set(body(line(reqs, 10, `server="srv0"`)), nil)
	st.failLast = 1
	s := newScraper(src, st, c)

	if err := s.Once(context.Background()); !errors.Is(err, errBoom) {
		t.Fatalf("Once: error = %v, want the store's error", err)
	}
	if got := query(t, st, reqs, ""); len(got) != 0 {
		t.Fatalf("stored %+v without knowing the previous state", got)
	}
	mustOnce(t, s)
	if got := only(t, st, reqs, `{server="srv0"}`); got.Raw != 10 {
		t.Errorf("after the retry raw = %v, want 10", got.Raw)
	}
}

func TestEverySampleOfAScrapeSharesTheScrapeTime(t *testing.T) {
	src, st, c := &fakeSource{}, newStore(), &clock{now: t0}
	src.set(body(line(reqs, 1, `server="srv0"`), line(inFlight, 1, `server="srv0"`)), nil)
	mustOnce(t, newScraper(src, st, c))

	last, _ := st.LastMetrics(context.Background())
	for key, s := range last {
		if !s.Time.Equal(t0) {
			t.Errorf("%s stamped %v, want %v", key, s.Time, t0)
		}
	}
}

func TestRunScrapesRepeatedlyAndStopsWhenCancelled(t *testing.T) {
	src, st := &fakeSource{}, newStore()
	src.set(body(line(reqs, 1, `server="srv0"`)), nil)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- scrape.New(src, st, scrape.Options{Interval: interval}).Run(ctx) }()

	for range 3 {
		select {
		case <-st.appended:
		case <-time.After(waitFor):
			t.Fatal("timed out waiting for a scrape")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after cancellation, want nil", err)
		}
	case <-time.After(waitFor):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

func TestRunKeepsGoingAfterAFailedScrape(t *testing.T) {
	src, st := &fakeSource{}, newStore()
	src.set("", errBoom)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go scrape.New(src, st, scrape.Options{Interval: interval}).Run(ctx)

	src.set(body(line(reqs, 1, `server="srv0"`)), nil) // Caddy comes back
	select {
	case <-st.appended:
	case <-time.After(waitFor):
		t.Fatal("Run did not recover after the source failed")
	}
}

func TestRunWithACancelledContextReturnsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scrape.New(&fakeSource{}, newStore(), scrape.Options{}).Run(ctx); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}
