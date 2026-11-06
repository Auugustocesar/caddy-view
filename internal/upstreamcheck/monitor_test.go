package upstreamcheck

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeProber answers from a table and records every call.
type fakeProber struct {
	mu      sync.Mutex
	answers map[string]State
	calls   map[string]int
	// hook runs inside Check, before it answers.
	hook func(address string)
}

func (f *fakeProber) Check(_ context.Context, address string) Result {
	if f.hook != nil {
		f.hook(address)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[address]++
	state, ok := f.answers[address]
	if !ok {
		state = Answering
	}
	return Result{State: state, Latency: 3 * time.Millisecond, CheckedAt: time.Unix(1700000000, 0)}
}

func (f *fakeProber) callCount(address string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[address]
}

// addressList is a source of addresses that tests can change between refreshes.
type addressList struct {
	mu    sync.Mutex
	addrs []string
	err   error
}

func (l *addressList) get(context.Context) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.addrs...), l.err
}

func (l *addressList) set(addrs []string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.addrs, l.err = addrs, err
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRefreshStoresAResultPerAddress(t *testing.T) {
	p := &fakeProber{answers: map[string]State{"10.0.0.2:80": Down}}
	src := &addressList{addrs: []string{"10.0.0.1:80", "10.0.0.2:80"}}
	m := NewMonitor(p, src.get, MonitorOptions{Logger: quietLogger()})

	m.Refresh(context.Background())

	for addr, want := range map[string]State{"10.0.0.1:80": Answering, "10.0.0.2:80": Down} {
		got, ok := m.Result(addr)
		if !ok || got.State != want {
			t.Errorf("Result(%q) = %+v, %v; want state %q", addr, got, ok, want)
		}
	}
	if _, ok := m.Result("10.9.9.9:80"); ok {
		t.Error("Result of an address that was never checked reports ok")
	}
}

func TestRefreshChecksEachAddressOnce(t *testing.T) {
	p := &fakeProber{}
	src := &addressList{addrs: []string{"10.0.0.1:80", "10.0.0.1:80", "10.0.0.2:80"}}
	m := NewMonitor(p, src.get, MonitorOptions{Logger: quietLogger()})

	m.Refresh(context.Background())

	if n := p.callCount("10.0.0.1:80"); n != 1 {
		t.Errorf("checks of a repeated address = %d, want 1", n)
	}
}

func TestRefreshDropsAddressesThatAreGone(t *testing.T) {
	p := &fakeProber{}
	src := &addressList{addrs: []string{"10.0.0.1:80", "10.0.0.2:80"}}
	m := NewMonitor(p, src.get, MonitorOptions{Logger: quietLogger()})
	m.Refresh(context.Background())

	src.set([]string{"10.0.0.1:80"}, nil)
	m.Refresh(context.Background())

	if _, ok := m.Result("10.0.0.2:80"); ok {
		t.Error("a result is kept for an upstream that is no longer configured")
	}
	if _, ok := m.Result("10.0.0.1:80"); !ok {
		t.Error("the result of an upstream that is still configured was lost")
	}
}

func TestRefreshKeepsResultsWhenTheAddressesCannotBeRead(t *testing.T) {
	p := &fakeProber{}
	src := &addressList{addrs: []string{"10.0.0.1:80"}}
	m := NewMonitor(p, src.get, MonitorOptions{Logger: quietLogger()})
	m.Refresh(context.Background())

	src.set(nil, errors.New("caddy unreachable"))
	m.Refresh(context.Background())

	if _, ok := m.Result("10.0.0.1:80"); !ok {
		t.Error("results were dropped because Caddy could not be read for a moment")
	}
	if n := p.callCount("10.0.0.1:80"); n != 1 {
		t.Errorf("checks = %d, want no new check without a list of addresses", n)
	}
}

func TestRefreshDoesNotStoreResultsOfAStoppedRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &fakeProber{hook: func(string) { cancel() }}
	src := &addressList{addrs: []string{"10.0.0.1:80"}}
	m := NewMonitor(p, src.get, MonitorOptions{Logger: quietLogger()})

	m.Refresh(ctx)

	if _, ok := m.Result("10.0.0.1:80"); ok {
		t.Error("a check that finished after the context was cancelled was stored")
	}
}

func TestRefreshLimitsHowManyChecksRunAtOnce(t *testing.T) {
	const limit = 2
	var (
		mu       sync.Mutex
		inFlight int
		peak     int
	)
	// The first callers wait for each other, so that without a limit they would all be in flight together.
	var barrier sync.WaitGroup
	barrier.Add(limit)
	var arrived atomic.Int32
	p := &fakeProber{hook: func(string) {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		mu.Unlock()
		if arrived.Add(1) <= limit {
			barrier.Done()
		}
		barrier.Wait()
		mu.Lock()
		inFlight--
		mu.Unlock()
	}}
	src := &addressList{addrs: []string{"a:1", "b:1", "c:1", "d:1", "e:1", "f:1"}}
	m := NewMonitor(p, src.get, MonitorOptions{Concurrency: limit, Logger: quietLogger()})

	m.Refresh(context.Background())

	if peak > limit {
		t.Errorf("peak concurrent checks = %d, want at most %d", peak, limit)
	}
}

func TestRunChecksAtStartAndOnEveryTick(t *testing.T) {
	refreshed := make(chan struct{}, 8)
	p := &fakeProber{hook: func(string) { refreshed <- struct{}{} }}
	src := &addressList{addrs: []string{"10.0.0.1:80"}}
	tick := make(chan time.Time)
	m := NewMonitor(p, src.get, MonitorOptions{Tick: tick, Logger: quietLogger()})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()

	<-refreshed // the check at start, before any tick
	tick <- time.Now()
	<-refreshed // the check on the tick
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v, want context.Canceled", err)
	}
}
