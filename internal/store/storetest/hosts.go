package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// HostStore is the part of a store the per-host totals contract checks.
type HostStore interface {
	AppendLog(ctx context.Context, batch store.LogBatch) error
	QueryHosts(ctx context.Context, q store.HostQuery) ([]store.HostTotals, error)
	Close() error
}

func mustHosts(t *testing.T, s HostStore, q store.HostQuery) []store.HostTotals {
	t.Helper()
	got, err := s.QueryHosts(context.Background(), q)
	if err != nil {
		t.Fatalf("QueryHosts(%+v): %v", q, err)
	}
	return got
}

func hostNames(totals []store.HostTotals) []string {
	out := make([]string, len(totals))
	for i, h := range totals {
		out[i] = h.Host
	}
	return out
}

func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// RunHosts checks the per-host totals against stores made by newStore. Every subtest gets a fresh store.
func RunHosts(t *testing.T, newStore func(t *testing.T) HostStore) {
	t.Helper()
	fresh := func(t *testing.T) HostStore {
		t.Helper()
		s := newStore(t)
		t.Cleanup(func() { s.Close() })
		return s
	}
	const a, b = "a.example.com", "b.example.com"

	// Two hosts over two minutes of one hour.
	sample := func() []store.LogEntry {
		return []store.LogEntry{
			request(a, 200, base.Add(10*time.Second), 20*time.Millisecond, 100),
			request(a, 200, base.Add(20*time.Second), 30*time.Millisecond, 50),
			request(b, 200, base.Add(30*time.Second), 5*time.Millisecond, 10),
			request(a, 502, base.Add(40*time.Second), 3*time.Second, 7),
			request(a, 200, base.Add(70*time.Second), time.Millisecond, 1),
		}
	}

	t.Run("each host is totalled over every bucket and class", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s, sample()...)

		got := mustHosts(t, s, store.HostQuery{Resolution: store.PerMinute})
		want := []store.HostTotals{
			{
				Host: a, Requests: 4, Bytes: 158, Status: [6]int64{2: 3, 5: 1},
				DurationSum: 3051 * time.Millisecond, DurationMax: 3 * time.Second, Latency: latency(0, 1, 2, 1, 3, 1, 9, 1),
			},
			{
				Host: b, Requests: 1, Bytes: 10, Status: [6]int64{2: 1},
				DurationSum: 5 * time.Millisecond, DurationMax: 5 * time.Millisecond, Latency: latency(0, 1),
			},
		}
		if len(got) != len(want) {
			t.Fatalf("got %d hosts, want %d: %+v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("host %d = %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("minutes and hours give the same totals", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s, sample()...)
		perMinute := mustHosts(t, s, store.HostQuery{Resolution: store.PerMinute})
		perHour := mustHosts(t, s, store.HostQuery{Resolution: store.PerHour})
		if len(perMinute) != len(perHour) {
			t.Fatalf("minutes gave %d hosts and hours %d", len(perMinute), len(perHour))
		}
		for i := range perMinute {
			if perMinute[i] != perHour[i] {
				t.Errorf("host %d: minutes %+v, hours %+v", i, perMinute[i], perHour[i])
			}
		}
	})

	t.Run("the range applies to the bucket start", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s, sample()...)
		tests := []struct {
			name     string
			query    store.HostQuery
			requests int64 // of host a
			hosts    int
		}{
			{"To excludes its own bucket", store.HostQuery{Resolution: store.PerMinute, To: base.Add(time.Minute)}, 3, 2},
			{"From includes its own bucket", store.HostQuery{Resolution: store.PerMinute, From: base.Add(time.Minute)}, 1, 1},
			{"a range inside a bucket still finds it", store.HostQuery{Resolution: store.PerMinute, From: base, To: base.Add(time.Second)}, 3, 2},
			{"after everything", store.HostQuery{Resolution: store.PerHour, From: base.Add(time.Hour)}, 0, 0},
		}
		for _, tt := range tests {
			got := mustHosts(t, s, tt.query)
			if len(got) != tt.hosts || (tt.hosts > 0 && got[0].Host == a && got[0].Requests != tt.requests) {
				t.Errorf("%s: got %+v, want %d hosts with %d requests for %s", tt.name, got, tt.hosts, tt.requests, a)
			}
		}
	})

	t.Run("the busiest host comes first, and ties go by name", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s,
			request("c.example.com", 200, base.Add(time.Second), time.Millisecond, 1),
			request("b.example.com", 200, base.Add(time.Second), time.Millisecond, 1),
			request("b.example.com", 200, base.Add(2*time.Second), time.Millisecond, 1),
			request("a.example.com", 200, base.Add(time.Second), time.Millisecond, 1),
			request("z.example.com", 200, base.Add(time.Second), time.Millisecond, 1),
			request("z.example.com", 200, base.Add(2*time.Second), time.Millisecond, 1),
			request("z.example.com", 200, base.Add(3*time.Second), time.Millisecond, 1),
		)
		got := hostNames(mustHosts(t, s, store.HostQuery{Resolution: store.PerHour}))
		want := []string{"z.example.com", "b.example.com", "a.example.com", "c.example.com"}
		if !sameNames(got, want) {
			t.Errorf("hosts = %v, want %v", got, want)
		}
	})

	t.Run("a limit keeps the busiest", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s,
			request("a.example.com", 200, base.Add(time.Second), time.Millisecond, 1),
			request("b.example.com", 200, base.Add(time.Second), time.Millisecond, 1),
			request("b.example.com", 200, base.Add(2*time.Second), time.Millisecond, 1),
			request("c.example.com", 200, base.Add(time.Second), time.Millisecond, 1),
			request("c.example.com", 200, base.Add(2*time.Second), time.Millisecond, 1),
			request("c.example.com", 200, base.Add(3*time.Second), time.Millisecond, 1),
		)
		got := hostNames(mustHosts(t, s, store.HostQuery{Resolution: store.PerHour, Limit: 2}))
		if want := []string{"c.example.com", "b.example.com"}; !sameNames(got, want) {
			t.Errorf("hosts = %v, want %v", got, want)
		}
	})

	t.Run("without a limit it returns DefaultHostLimit hosts, and a larger limit is capped", func(t *testing.T) {
		s := fresh(t)
		var entries []store.LogEntry
		for i := range store.MaxHostLimit + 20 {
			entries = append(entries, request(fmt.Sprintf("host%04d.example.com", i), 200, base.Add(time.Second), time.Millisecond, 1))
		}
		mustAppend(t, s, entries...)
		if got := mustHosts(t, s, store.HostQuery{Resolution: store.PerHour}); len(got) != store.DefaultHostLimit {
			t.Errorf("no limit gave %d hosts, want %d", len(got), store.DefaultHostLimit)
		}
		if got := mustHosts(t, s, store.HostQuery{Resolution: store.PerHour, Limit: 1_000_000}); len(got) != store.MaxHostLimit {
			t.Errorf("a huge limit gave %d hosts, want the cap %d", len(got), store.MaxHostLimit)
		}
	})

	t.Run("a status outside 100 to 599 is counted as other", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s, request(a, 0, base.Add(time.Second), time.Millisecond, 1), request(a, 999, base.Add(2*time.Second), time.Millisecond, 1), request(a, 304, base.Add(3*time.Second), time.Millisecond, 1))
		got := mustHosts(t, s, store.HostQuery{Resolution: store.PerHour})
		if len(got) != 1 || got[0].Status != [6]int64{0: 2, 3: 1} {
			t.Errorf("hosts = %+v, want two other and one 3xx", got)
		}
	})

	t.Run("nothing stored gives an empty list, not an error", func(t *testing.T) {
		got := mustHosts(t, fresh(t), store.HostQuery{Resolution: store.PerHour})
		if got == nil || len(got) != 0 {
			t.Errorf("hosts = %#v, want an empty, non-nil list", got)
		}
	})

	t.Run("a query that cannot be answered is refused", func(t *testing.T) {
		s := fresh(t)
		for _, q := range []store.HostQuery{{}, {Resolution: 99}, {Resolution: store.PerHour, Limit: -1}} {
			if _, err := s.QueryHosts(context.Background(), q); !errors.Is(err, store.ErrInvalidQuery) {
				t.Errorf("QueryHosts(%+v) error = %v, want ErrInvalidQuery", q, err)
			}
		}
	})

	t.Run("a cancelled context and a closed store are errors", func(t *testing.T) {
		s := newStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := s.QueryHosts(ctx, store.HostQuery{Resolution: store.PerHour}); !errors.Is(err, context.Canceled) {
			t.Errorf("a cancelled context gave %v, want context.Canceled", err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.QueryHosts(context.Background(), store.HostQuery{Resolution: store.PerHour}); !errors.Is(err, store.ErrClosed) {
			t.Errorf("a closed store gave %v, want ErrClosed", err)
		}
	})
}
