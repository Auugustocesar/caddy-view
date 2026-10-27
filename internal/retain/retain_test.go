package retain_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/retain"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

const (
	waitFor  = 5 * time.Second
	interval = 5 * time.Millisecond

	logBytes    = 100 // what the fake says one log entry weighs
	metricBytes = 10
)

var (
	errBoom = errors.New("the disk is on fire")
	now     = time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
)

// fakeStore counts rows instead of keeping them, and reports a size made from the counts.
type fakeStore struct {
	mu sync.Mutex

	logs, metrics int64
	// fixed is size that no deletion can remove, like rollups.
	fixed int64

	// What the deletions received.
	logsBefore, metricsBefore          []time.Time
	minuteBefore, hourBefore           []time.Time
	oldestLogsCalls, oldestMetricCalls int
	sizeCalls                          int

	// What the time-based deletions report as removed.
	reportLogs, reportMetrics, reportMinute, reportHour int64

	// failOn makes the named method fail.
	failOn string
	// pruned is told about every DeleteLogsBefore, so Run can be observed.
	pruned chan struct{}
}

func newFake() *fakeStore { return &fakeStore{pruned: make(chan struct{}, 1000)} }

func (f *fakeStore) fail(name string) error {
	if f.failOn == name {
		return errBoom
	}
	return nil
}

func (f *fakeStore) DeleteLogsBefore(_ context.Context, t time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logsBefore = append(f.logsBefore, t)
	select {
	case f.pruned <- struct{}{}:
	default:
	}
	return f.reportLogs, f.fail("DeleteLogsBefore")
}

func (f *fakeStore) DeleteMetricsBefore(_ context.Context, t time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metricsBefore = append(f.metricsBefore, t)
	return f.reportMetrics, f.fail("DeleteMetricsBefore")
}

func (f *fakeStore) DeleteRollupsBefore(_ context.Context, res store.Resolution, t time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if res == store.PerMinute {
		f.minuteBefore = append(f.minuteBefore, t)
		return f.reportMinute, f.fail("DeleteRollupsBefore")
	}
	f.hourBefore = append(f.hourBefore, t)
	return f.reportHour, f.fail("DeleteRollupsBefore")
}

func (f *fakeStore) DeleteOldestLogs(_ context.Context, n int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.oldestLogsCalls++
	removed := min(int64(n), f.logs)
	f.logs -= removed
	return removed, f.fail("DeleteOldestLogs")
}

func (f *fakeStore) DeleteOldestMetrics(_ context.Context, n int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.oldestMetricCalls++
	removed := min(int64(n), f.metrics)
	f.metrics -= removed
	return removed, f.fail("DeleteOldestMetrics")
}

func (f *fakeStore) Size(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sizeCalls++
	return f.fixed + f.logs*logBytes + f.metrics*metricBytes, f.fail("Size")
}

func newPruner(f *fakeStore, p retain.Policy) *retain.Pruner {
	return retain.New(f, retain.Options{Policy: p, Interval: interval, TrimBatch: 100, Now: func() time.Time { return now }})
}

func once(t *testing.T, p *retain.Pruner) retain.Result {
	t.Helper()
	res, err := p.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	return res
}

func only(t *testing.T, name string, got []time.Time) time.Time {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("%s was called %d times, want once", name, len(got))
	}
	return got[0]
}

func TestOnceDeletesWhatIsPastItsRetention(t *testing.T) {
	f := newFake()
	f.reportLogs, f.reportMetrics, f.reportMinute, f.reportHour = 11, 22, 33, 44
	res := once(t, newPruner(f, retain.Policy{Raw: 14 * 24 * time.Hour, Rollups: 90 * 24 * time.Hour}))

	raw, rollups := now.Add(-14*24*time.Hour), now.Add(-90*24*time.Hour)
	for name, tt := range map[string]struct{ got, want time.Time }{
		"logs":           {only(t, "DeleteLogsBefore", f.logsBefore), raw},
		"metrics":        {only(t, "DeleteMetricsBefore", f.metricsBefore), raw},
		"minute rollups": {only(t, "DeleteRollupsBefore(minute)", f.minuteBefore), raw},
		"hour rollups":   {only(t, "DeleteRollupsBefore(hour)", f.hourBefore), rollups},
	} {
		if !tt.got.Equal(tt.want) {
			t.Errorf("%s were cut at %v, want %v", name, tt.got, tt.want)
		}
	}
	want := retain.Result{Logs: 11, Metrics: 22, MinuteRollups: 33, HourRollups: 44}
	if res != want {
		t.Errorf("Result = %+v, want %+v", res, want)
	}
}

func TestAZeroRetentionKeepsThatDataForever(t *testing.T) {
	f := newFake()
	once(t, newPruner(f, retain.Policy{Raw: 0, Rollups: 24 * time.Hour}))
	if len(f.logsBefore)+len(f.metricsBefore)+len(f.minuteBefore) != 0 {
		t.Errorf("raw data was deleted with a zero retention: %v %v %v", f.logsBefore, f.metricsBefore, f.minuteBefore)
	}
	if len(f.hourBefore) != 1 {
		t.Errorf("hour rollups were cut %d times, want once", len(f.hourBefore))
	}

	f = newFake()
	once(t, newPruner(f, retain.Policy{Raw: time.Hour, Rollups: 0}))
	if len(f.hourBefore) != 0 || len(f.logsBefore) != 1 {
		t.Errorf("with a zero rollup retention: hour cuts %d, log cuts %d, want 0 and 1", len(f.hourBefore), len(f.logsBefore))
	}
}

func TestNoSizeCapMeansSizeIsNeverAsked(t *testing.T) {
	f := newFake()
	f.logs = 1_000_000
	once(t, newPruner(f, retain.Policy{Raw: time.Hour}))
	if f.sizeCalls != 0 || f.oldestLogsCalls != 0 {
		t.Errorf("size asked %d times, trimmed %d times, want neither without a cap", f.sizeCalls, f.oldestLogsCalls)
	}
}

func TestUnderTheCapNothingIsTrimmed(t *testing.T) {
	f := newFake()
	f.logs = 10 // 1,000 bytes
	res := once(t, newPruner(f, retain.Policy{MaxBytes: 5000}))
	if f.oldestLogsCalls != 0 || res.TrimmedLogs != 0 || res.OverCap {
		t.Errorf("trimmed %d calls, result %+v, want nothing", f.oldestLogsCalls, res)
	}
}

func TestOverTheCapTheOldestLogsGoFirstInBatches(t *testing.T) {
	f := newFake()
	f.logs = 1000 // 100,000 bytes
	res := once(t, newPruner(f, retain.Policy{MaxBytes: 30_000}))

	if res.TrimmedLogs != 700 || f.logs != 300 {
		t.Errorf("trimmed %d logs leaving %d, want 700 trimmed (batches of 100) leaving 300", res.TrimmedLogs, f.logs)
	}
	if res.TrimmedMetrics != 0 || res.OverCap {
		t.Errorf("Result = %+v, want no metric trimming and not over the cap", res)
	}
	if f.oldestLogsCalls != 7 {
		t.Errorf("DeleteOldestLogs was called %d times, want 7 batches", f.oldestLogsCalls)
	}
}

func TestWhenTheLogsAreGoneTheOldestMetricsGo(t *testing.T) {
	f := newFake()
	f.logs, f.metrics = 100, 5000 // 10,000 + 50,000 bytes
	res := once(t, newPruner(f, retain.Policy{MaxBytes: 20_000}))

	if res.TrimmedLogs != 100 || f.logs != 0 {
		t.Errorf("trimmed %d logs, %d left, want all 100 gone first", res.TrimmedLogs, f.logs)
	}
	if res.TrimmedMetrics != 3000 || f.metrics != 2000 || res.OverCap {
		t.Errorf("Result = %+v with %d metrics left, want 3000 trimmed leaving 2000", res, f.metrics)
	}
}

func TestACapThatCannotBeMetIsReportedAndDoesNotLoopForever(t *testing.T) {
	f := newFake()
	f.fixed, f.logs, f.metrics = 50_000, 10, 10 // the fixed part alone is over the cap
	res := once(t, newPruner(f, retain.Policy{MaxBytes: 20_000}))

	if !res.OverCap {
		t.Errorf("Result = %+v, want OverCap", res)
	}
	if f.logs != 0 || f.metrics != 0 {
		t.Errorf("%d logs and %d metrics left, want everything trimmable gone", f.logs, f.metrics)
	}
}

func TestAStoreErrorIsReturned(t *testing.T) {
	for _, method := range []string{"DeleteLogsBefore", "DeleteMetricsBefore", "DeleteRollupsBefore", "Size", "DeleteOldestLogs", "DeleteOldestMetrics"} {
		t.Run(method, func(t *testing.T) {
			f := newFake()
			f.failOn = method
			f.logs, f.metrics = 1000, 1000
			_, err := newPruner(f, retain.Policy{Raw: time.Hour, Rollups: time.Hour, MaxBytes: 1}).Once(context.Background())
			if !errors.Is(err, errBoom) {
				t.Errorf("error = %v, want the store's error", err)
			}
		})
	}
}

func TestRunPrunesRepeatedlyAndStopsWhenCancelled(t *testing.T) {
	f := newFake()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- newPruner(f, retain.Policy{Raw: time.Hour}).Run(ctx) }()

	for range 3 {
		select {
		case <-f.pruned:
		case <-time.After(waitFor):
			t.Fatal("timed out waiting for a round")
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

func TestRunKeepsGoingAfterAFailedRound(t *testing.T) {
	f := newFake()
	f.failOn = "DeleteLogsBefore"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go newPruner(f, retain.Policy{Raw: time.Hour}).Run(ctx)

	for range 2 { // a second round proves it went on after the first failed
		select {
		case <-f.pruned:
		case <-time.After(waitFor):
			t.Fatal("Run stopped after a failed round")
		}
	}
}

func TestRunWithACancelledContextReturnsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newPruner(newFake(), retain.Policy{Raw: time.Hour}).Run(ctx); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}
