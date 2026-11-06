package upstreamcheck

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultInterval is how often the upstreams are checked.
const DefaultInterval = 10 * time.Second

// DefaultConcurrency is how many upstreams are checked at once.
const DefaultConcurrency = 8

// Prober checks one upstream. *Checker is the implementation.
type Prober interface {
	Check(ctx context.Context, address string) Result
}

// MonitorOptions configures a Monitor.
type MonitorOptions struct {
	// Interval is the time between rounds of checks. Zero means DefaultInterval.
	Interval time.Duration
	// Concurrency caps the checks that run at once. Zero means DefaultConcurrency.
	Concurrency int
	// Tick replaces the timer that starts each round. Tests use it; nil means a timer of Interval.
	Tick <-chan time.Time
	// Logger receives operational logs. A nil Logger discards them.
	Logger *slog.Logger
}

// Monitor keeps the latest result of checking each upstream, so that a request for the sites does not wait for the
// checks.
type Monitor struct {
	prober    Prober
	addresses func(ctx context.Context) ([]string, error)
	opts      MonitorOptions
	log       *slog.Logger

	mu      sync.RWMutex
	results map[string]Result
}

// NewMonitor returns a Monitor that checks the addresses that addresses returns, with prober. Nothing runs until Run or
// Refresh is called.
func NewMonitor(prober Prober, addresses func(ctx context.Context) ([]string, error), opts MonitorOptions) *Monitor {
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultConcurrency
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Monitor{prober: prober, addresses: addresses, opts: opts, log: log, results: map[string]Result{}}
}

// Result returns the latest result for an upstream, and false when it has not been checked.
func (m *Monitor) Result(address string) (Result, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.results[address]
	return r, ok
}

// Run checks the upstreams at once and then every interval, until ctx is done. It returns ctx's error.
func (m *Monitor) Run(ctx context.Context) error {
	tick := m.opts.Tick
	if tick == nil {
		t := time.NewTicker(m.opts.Interval)
		defer t.Stop()
		tick = t.C
	}
	for {
		m.Refresh(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick:
		}
	}
}

// Refresh checks every configured upstream once, with at most Concurrency checks at a time, and replaces the stored
// results with the new ones, so that an upstream that is no longer configured is forgotten. When the addresses cannot be
// read, or ctx is done before the round finishes, the stored results stay as they were.
func (m *Monitor) Refresh(ctx context.Context) {
	addrs, err := m.addresses(ctx)
	if err != nil {
		m.log.Warn("upstream check skipped, the upstreams could not be listed", "error", err)
		return
	}
	addrs = unique(addrs)

	results := make(map[string]Result, len(addrs))
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, m.opts.Concurrency)
	)
	for _, addr := range addrs {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r := m.prober.Check(ctx, addr)
			mu.Lock()
			results[addr] = r
			mu.Unlock()
		}()
	}
	wg.Wait()

	if ctx.Err() != nil {
		return // the results of a stopped round say nothing about the upstreams
	}
	m.mu.Lock()
	m.results = results
	m.mu.Unlock()
}

func unique(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
