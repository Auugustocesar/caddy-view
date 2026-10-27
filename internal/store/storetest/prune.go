package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// PruneStore is the part of a store the retention contract checks.
type PruneStore interface {
	AppendLog(ctx context.Context, batch store.LogBatch) error
	QueryLogs(ctx context.Context, q store.LogQuery) ([]store.LogEntry, error)
	AppendMetrics(ctx context.Context, samples []store.MetricSample) error
	QueryMetrics(ctx context.Context, q store.MetricQuery) ([]store.MetricSample, error)
	DeleteMetricsBefore(ctx context.Context, t time.Time) (int64, error)
	DeleteOldestLogs(ctx context.Context, n int) (int64, error)
	DeleteOldestMetrics(ctx context.Context, n int) (int64, error)
	Size(ctx context.Context) (int64, error)
	Close() error
}

func mustSize(t *testing.T, s PruneStore) int64 {
	t.Helper()
	n, err := s.Size(context.Background())
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	return n
}

// RunPrune checks the retention part of the contract against stores made by newStore. Every subtest gets a fresh
// store.
func RunPrune(t *testing.T, newStore func(t *testing.T) PruneStore) {
	t.Helper()
	fresh := func(t *testing.T) PruneStore {
		t.Helper()
		s := newStore(t)
		t.Cleanup(func() { s.Close() })
		return s
	}
	const name = "caddy_http_requests_total"
	ctx := context.Background()

	t.Run("DeleteMetricsBefore removes older samples and keeps the boundary", func(t *testing.T) {
		s := fresh(t)
		mustAppendMetrics(t, s,
			sample(name, "", base, 1, 1),
			sample(name, "", base.Add(time.Second), 2, 2),
			sample(name, "", base.Add(2*time.Second), 3, 3),
			sample(name, "", base.Add(3*time.Second), 4, 4),
		)
		n, err := s.DeleteMetricsBefore(ctx, base.Add(2*time.Second))
		if err != nil || n != 2 {
			t.Fatalf("DeleteMetricsBefore = %d, %v, want 2 removed", n, err)
		}
		if got := values(mustQueryMetrics(t, s, store.MetricQuery{Name: name})); !sameValues(got, []float64{3, 4}) {
			t.Errorf("samples left = %v, want [3 4]", got)
		}
		if n, err := s.DeleteMetricsBefore(ctx, base); err != nil || n != 0 {
			t.Errorf("deleting nothing = %d, %v, want 0", n, err)
		}
	})

	t.Run("DeleteOldestLogs removes the oldest by insertion order", func(t *testing.T) {
		s := fresh(t)
		// Times out of order on purpose: the oldest is the one stored first, not the one with the smallest time.
		for i, offset := range []time.Duration{5, 1, 4, 2, 3} {
			mustAppend(t, s, entry("h", fmt.Sprintf("/%d", i+1), 200, base.Add(offset*time.Second)))
		}
		n, err := s.DeleteOldestLogs(ctx, 2)
		if err != nil || n != 2 {
			t.Fatalf("DeleteOldestLogs(2) = %d, %v, want 2 removed", n, err)
		}
		if got := paths(mustQuery(t, s, store.LogQuery{})); !equal(got, []string{"/5", "/4", "/3"}) {
			t.Errorf("entries left = %v, want [/5 /4 /3]", got)
		}
		if n, err := s.DeleteOldestLogs(ctx, 0); err != nil || n != 0 {
			t.Errorf("DeleteOldestLogs(0) = %d, %v, want 0 and no error", n, err)
		}
		if n, err := s.DeleteOldestLogs(ctx, 100); err != nil || n != 3 {
			t.Errorf("DeleteOldestLogs(100) = %d, %v, want the 3 that were left", n, err)
		}
		if _, err := s.DeleteOldestLogs(ctx, -1); !errors.Is(err, store.ErrInvalidQuery) {
			t.Errorf("DeleteOldestLogs(-1): error = %v, want ErrInvalidQuery", err)
		}
	})

	t.Run("DeleteOldestMetrics removes the oldest by time", func(t *testing.T) {
		s := fresh(t)
		mustAppendMetrics(t, s,
			sample(name, "", base.Add(2*time.Second), 3, 3),
			sample(name, "", base, 1, 1),
			sample(name, "", base.Add(time.Second), 2, 2),
		)
		n, err := s.DeleteOldestMetrics(ctx, 1)
		if err != nil || n != 1 {
			t.Fatalf("DeleteOldestMetrics(1) = %d, %v, want 1 removed", n, err)
		}
		if got := values(mustQueryMetrics(t, s, store.MetricQuery{Name: name})); !sameValues(got, []float64{2, 3}) {
			t.Errorf("samples left = %v, want [2 3]", got)
		}
		if _, err := s.DeleteOldestMetrics(ctx, -1); !errors.Is(err, store.ErrInvalidQuery) {
			t.Errorf("DeleteOldestMetrics(-1): error = %v, want ErrInvalidQuery", err)
		}
	})

	t.Run("Size grows with the data and shrinks when it is deleted", func(t *testing.T) {
		s := fresh(t)
		empty := mustSize(t, s)

		const rows = 3000
		long := "/" + strings.Repeat("x", 200)
		entries := make([]store.LogEntry, rows)
		for i := range entries {
			entries[i] = entry("h.example.com", fmt.Sprintf("%s%d", long, i), 200, base)
		}
		mustAppend(t, s, entries...)
		full := mustSize(t, s)
		if full <= empty {
			t.Fatalf("Size with %d entries = %d, want more than the empty %d", rows, full, empty)
		}

		if n, err := s.DeleteOldestLogs(ctx, rows); err != nil || n != rows {
			t.Fatalf("DeleteOldestLogs = %d, %v, want %d", n, err, rows)
		}
		if after := mustSize(t, s); after >= full {
			t.Errorf("Size after deleting everything = %d, want less than %d", after, full)
		}
	})

	t.Run("a cancelled context and a closed store stop the new methods", func(t *testing.T) {
		s := newStore(t)
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		checks := map[string]error{}
		_, checks["DeleteMetricsBefore"] = s.DeleteMetricsBefore(cancelled, base)
		_, checks["DeleteOldestLogs"] = s.DeleteOldestLogs(cancelled, 1)
		_, checks["DeleteOldestMetrics"] = s.DeleteOldestMetrics(cancelled, 1)
		_, checks["Size"] = s.Size(cancelled)
		for name, err := range checks {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s with a cancelled context: error = %v, want context.Canceled", name, err)
			}
		}
		s.Close()
		if _, err := s.DeleteMetricsBefore(ctx, base); err == nil {
			t.Error("DeleteMetricsBefore after Close succeeded, want an error")
		}
		if _, err := s.DeleteOldestLogs(ctx, 1); err == nil {
			t.Error("DeleteOldestLogs after Close succeeded, want an error")
		}
		if _, err := s.Size(ctx); err == nil {
			t.Error("Size after Close succeeded, want an error")
		}
	})
}
