// Package scrape reads Caddy's /metrics and stores the series the UI charts, turning counters into increases.
package scrape

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/promtext"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

const defaultInterval = 15 * time.Second

// Source serves the Prometheus text exposition of Caddy. caddy.Client satisfies it.
type Source interface {
	Metrics(ctx context.Context) ([]byte, error)
}

// Store is the part of the history store that the scraper needs.
type Store interface {
	AppendMetrics(ctx context.Context, samples []store.MetricSample) error
	LastMetrics(ctx context.Context) (map[string]store.MetricSample, error)
}

// Options tunes a Scraper. The zero value is valid.
type Options struct {
	// Interval is how often Run scrapes. Defaults to 15 s.
	Interval time.Duration
	// Now gives the time stamped on a scrape. Defaults to time.Now.
	Now func() time.Time
	// Logger receives scrape errors from Run. Defaults to a logger that discards them.
	Logger *slog.Logger
}

// rule says how one Caddy series is stored: whether it is a counter, and which labels survive. Every other label is
// dropped and the values summed, which is what keeps hundreds of histogram series down to a handful.
type rule struct {
	counter bool
	labels  []string
	// codeClass turns the code label (404) into a class (4xx).
	codeClass bool
}

// rules is the fixed set of series that are stored, by metric name.
var rules = map[string]rule{
	"caddy_http_requests_total":                 {counter: true, labels: []string{"server"}},
	"caddy_http_request_duration_seconds_count": {counter: true, labels: []string{"server"}, codeClass: true},
	"caddy_http_request_duration_seconds_sum":   {counter: true, labels: []string{"server"}, codeClass: true},
	"caddy_http_requests_in_flight":             {labels: []string{"server"}},
	"caddy_reverse_proxy_upstreams_healthy":     {labels: []string{"upstream"}},
	"caddy_config_last_reload_successful":       {},
}

// Scraper copies selected Caddy metrics into a store.
type Scraper struct {
	src  Source
	st   Store
	opts Options

	// previous holds the raw value of every series as last stored. It is loaded from the store on the first scrape
	// and only advances after a batch has been stored, so a failed write is not forgotten.
	previous map[string]float64
	loaded   bool
}

// New returns a Scraper that reads from src and writes to st.
func New(src Source, st Store, opts Options) *Scraper {
	if opts.Interval <= 0 {
		opts.Interval = defaultInterval
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Scraper{src: src, st: st, opts: opts, previous: make(map[string]float64)}
}

// Run scrapes every Interval until ctx is done, and then returns nil. A failing scrape is logged and tried again.
func (s *Scraper) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	var lastErr string
	for {
		err := s.Once(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil && err.Error() != lastErr:
			// Logged when it first happens or changes, not once per scrape.
			s.opts.Logger.Warn("metrics scrape failed, will retry", "error", err)
			lastErr = err.Error()
		case err == nil && lastErr != "":
			s.opts.Logger.Info("metrics scrape recovered")
			lastErr = ""
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Once scrapes Caddy one time and stores the selected series. Nothing is stored when the scrape fails.
func (s *Scraper) Once(ctx context.Context) error {
	body, err := s.src.Metrics(ctx)
	if err != nil {
		return fmt.Errorf("scrape caddy metrics: %w", err)
	}
	parsed, err := promtext.Parse(body)
	if err != nil {
		return fmt.Errorf("read caddy metrics: %w", err)
	}
	if err := s.loadPrevious(ctx); err != nil {
		return err
	}

	readings := aggregate(parsed)
	if len(readings) == 0 {
		return nil
	}
	now := s.opts.Now()
	// With nothing stored yet there is nothing to compare with: counters only record where they are.
	baseline := len(s.previous) == 0

	keys := make([]string, 0, len(readings))
	for key := range readings {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	samples := make([]store.MetricSample, 0, len(keys))
	for _, key := range keys {
		r := readings[key]
		samples = append(samples, store.MetricSample{
			Name: r.name, Labels: r.labels, Time: now, Value: s.value(key, r, baseline), Raw: r.raw,
		})
	}

	if err := s.st.AppendMetrics(ctx, samples); err != nil {
		return fmt.Errorf("store metrics: %w", err)
	}
	for key, r := range readings {
		s.previous[key] = r.raw
	}
	return nil
}

// loadPrevious reads the last stored raw values, once, so counting continues across a restart.
func (s *Scraper) loadPrevious(ctx context.Context) error {
	if s.loaded {
		return nil
	}
	last, err := s.st.LastMetrics(ctx)
	if err != nil {
		return fmt.Errorf("load previous metrics: %w", err)
	}
	for key, sample := range last {
		s.previous[key] = sample.Raw
	}
	s.loaded = true
	return nil
}

// value is what is stored as the Value of a reading: the reading itself for a gauge, the increase for a counter.
func (s *Scraper) value(key string, r reading, baseline bool) float64 {
	if !r.counter {
		return r.raw
	}
	prev, seen := s.previous[key]
	switch {
	case !seen && baseline:
		return 0
	case !seen:
		return r.raw // a series born since the last scrape: everything it counted is new
	case r.raw < prev:
		return r.raw // the counter went down, so Caddy restarted
	}
	return r.raw - prev
}

// reading is one stored series of one scrape, summed over the labels that are dropped.
type reading struct {
	name, labels string
	counter      bool
	raw          float64
}

// aggregate keeps the series in rules, drops the labels that are not wanted, and sums what falls together. Values
// that are not finite are left out, as they cannot be charted or stored.
func aggregate(parsed []promtext.Sample) map[string]reading {
	out := make(map[string]reading)
	for _, p := range parsed {
		r, ok := rules[p.Name]
		if !ok || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
			continue
		}
		kept := make(map[string]string, len(r.labels)+1)
		for _, name := range r.labels {
			if v, ok := p.Labels[name]; ok {
				kept[name] = v
			}
		}
		if r.codeClass {
			kept["code"] = statusClass(p.Labels["code"])
		}
		labels := promtext.FormatLabels(kept)
		key := p.Name + labels
		cur := out[key]
		out[key] = reading{name: p.Name, labels: labels, counter: r.counter, raw: cur.raw + p.Value}
	}
	return out
}

// statusClass turns an HTTP status code such as 404 into its class, 4xx. Anything else is "other".
func statusClass(code string) string {
	if len(code) == 3 && code[0] >= '1' && code[0] <= '5' && code[1] >= '0' && code[1] <= '9' && code[2] >= '0' && code[2] <= '9' {
		return code[:1] + "xx"
	}
	return "other"
}
