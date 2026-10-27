package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// RollupStore is the part of a store the rollup contract checks.
type RollupStore interface {
	AppendLog(ctx context.Context, batch store.LogBatch) error
	DeleteLogsBefore(ctx context.Context, t time.Time) (int64, error)
	QueryTraffic(ctx context.Context, q store.TrafficQuery) ([]store.TrafficBucket, error)
	DeleteRollupsBefore(ctx context.Context, res store.Resolution, t time.Time) (int64, error)
	Close() error
}

// request is a log entry with the fields that rollups sum.
func request(host string, status int, at time.Time, d time.Duration, bytes int64) store.LogEntry {
	return store.LogEntry{Time: at, Host: host, Method: "GET", Path: "/", Status: status, Duration: d, Bytes: bytes}
}

func mustTraffic(t *testing.T, s RollupStore, q store.TrafficQuery) []store.TrafficBucket {
	t.Helper()
	got, err := s.QueryTraffic(context.Background(), q)
	if err != nil {
		t.Fatalf("QueryTraffic(%+v): %v", q, err)
	}
	return got
}

// latency builds a histogram from bucket index and count pairs.
func latency(pairs ...int) (h [store.LatencyBuckets]int64) {
	for i := 0; i < len(pairs); i += 2 {
		h[pairs[i]] = int64(pairs[i+1])
	}
	return h
}

// RunRollups checks the traffic rollup part of the contract against stores made by newStore. Every subtest gets a
// fresh store.
func RunRollups(t *testing.T, newStore func(t *testing.T) RollupStore) {
	t.Helper()
	fresh := func(t *testing.T) RollupStore {
		t.Helper()
		s := newStore(t)
		t.Cleanup(func() { s.Close() })
		return s
	}
	const a, b = "a.example.com", "b.example.com"

	// Entries over two minutes of one hour, from two hosts and two status classes.
	sample := func() []store.LogEntry {
		return []store.LogEntry{
			request(a, 200, base.Add(10*time.Second), 20*time.Millisecond, 100),
			request(a, 200, base.Add(20*time.Second), 30*time.Millisecond, 50),
			request(b, 200, base.Add(30*time.Second), 5*time.Millisecond, 10),
			request(a, 502, base.Add(40*time.Second), 3*time.Second, 7),
			request(a, 200, base.Add(70*time.Second), time.Millisecond, 1),
		}
	}

	t.Run("a minute bucket sums the entries it holds, per status class", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s, sample()...)

		got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerMinute})
		want := []store.TrafficBucket{
			{Start: base, Class: 2, Count: 3, Bytes: 160, DurationSum: 55 * time.Millisecond, DurationMax: 30 * time.Millisecond, Latency: latency(0, 1, 2, 1, 3, 1)},
			{Start: base, Class: 5, Count: 1, Bytes: 7, DurationSum: 3 * time.Second, DurationMax: 3 * time.Second, Latency: latency(9, 1)},
			{Start: base.Add(time.Minute), Class: 2, Count: 1, Bytes: 1, DurationSum: time.Millisecond, DurationMax: time.Millisecond, Latency: latency(0, 1)},
		}
		if len(got) != len(want) {
			t.Fatalf("got %d buckets, want %d: %+v", len(got), len(want), got)
		}
		for i := range want {
			if !got[i].Start.Equal(want[i].Start) || got[i].Start.Location() != time.UTC {
				t.Errorf("bucket %d starts %v, want %v in UTC", i, got[i].Start, want[i].Start)
			}
			got[i].Start, want[i].Start = time.Time{}, time.Time{}
			if got[i] != want[i] {
				t.Errorf("bucket %d = %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("an hour bucket is the sum of its minutes", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s, sample()...)

		got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerHour})
		if len(got) != 2 {
			t.Fatalf("got %d buckets, want 2 (classes 2 and 5): %+v", len(got), got)
		}
		two := got[0]
		if two.Class != 2 || two.Count != 4 || two.Bytes != 161 || two.DurationSum != 56*time.Millisecond || two.DurationMax != 30*time.Millisecond ||
			two.Latency != latency(0, 2, 2, 1, 3, 1) || !two.Start.Equal(base) {
			t.Errorf("class 2 hour bucket = %+v", two)
		}
		if got[1].Class != 5 || got[1].Count != 1 {
			t.Errorf("class 5 hour bucket = %+v", got[1])
		}
	})

	t.Run("a host filter selects one host", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s, sample()...)
		got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerMinute, Host: b})
		if len(got) != 1 || got[0].Count != 1 || got[0].Class != 2 {
			t.Errorf("host b buckets = %+v, want one request in class 2", got)
		}
		if got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerMinute, Host: "nobody.example.com"}); len(got) != 0 {
			t.Errorf("unknown host gave %+v, want nothing", got)
		}
	})

	t.Run("the time range applies to the bucket start", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s, sample()...)
		tests := []struct {
			name  string
			query store.TrafficQuery
			want  int
		}{
			{"From includes its own bucket", store.TrafficQuery{Resolution: store.PerMinute, From: base.Add(time.Minute)}, 1},
			{"To excludes its own bucket", store.TrafficQuery{Resolution: store.PerMinute, To: base.Add(time.Minute)}, 2},
			{"a range inside a bucket still finds it", store.TrafficQuery{Resolution: store.PerMinute, From: base, To: base.Add(time.Second)}, 2},
			{"after everything", store.TrafficQuery{Resolution: store.PerHour, From: base.Add(time.Hour)}, 0},
		}
		for _, tt := range tests {
			if got := mustTraffic(t, s, tt.query); len(got) != tt.want {
				t.Errorf("%s: got %d buckets, want %d", tt.name, len(got), tt.want)
			}
		}
	})

	t.Run("status classes", func(t *testing.T) {
		s := fresh(t)
		for _, status := range []int{100, 200, 301, 404, 503, 0, 600} {
			mustAppend(t, s, request(a, status, base, time.Millisecond, 1))
		}
		got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerMinute})
		counts := map[int]int64{}
		for _, g := range got {
			counts[g.Class] = g.Count
		}
		want := map[int]int64{0: 2, 1: 1, 2: 1, 3: 1, 4: 1, 5: 1}
		for class, n := range want {
			if counts[class] != n {
				t.Errorf("class %d count = %d, want %d (all: %v)", class, counts[class], n, counts)
			}
		}
		for i := 1; i < len(got); i++ {
			if got[i-1].Class >= got[i].Class {
				t.Errorf("classes %d then %d are not ascending", got[i-1].Class, got[i].Class)
			}
		}
	})

	t.Run("latency bucket edges are inclusive", func(t *testing.T) {
		s := fresh(t)
		bounds := store.LatencyUpperBounds()
		for _, bound := range bounds {
			mustAppend(t, s, request(a, 200, base, bound, 0))                  // on the bound: its own bucket
			mustAppend(t, s, request(a, 200, base, bound+time.Microsecond, 0)) // just over: the next one
		}
		mustAppend(t, s, request(a, 200, base, time.Hour, 0)) // over every bound: the open bucket

		got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerMinute})
		if len(got) != 1 {
			t.Fatalf("got %d buckets, want 1", len(got))
		}
		for i, n := range got[0].Latency {
			want := int64(2) // on its own bound, and just over the previous bound (or, for the open one, the huge one)
			if i == 0 {
				want = 1 // nothing lies under the first bound but the requests on it
			}
			if n != want {
				t.Errorf("latency bucket %d = %d, want %d (all: %v)", i, n, want, got[0].Latency)
			}
		}
	})

	t.Run("batches accumulate and the maximum is kept", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s, request(a, 200, base, 50*time.Millisecond, 1))
		mustAppend(t, s, request(a, 200, base.Add(time.Second), 10*time.Millisecond, 2))
		got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerMinute})
		if len(got) != 1 || got[0].Count != 2 || got[0].Bytes != 3 || got[0].DurationMax != 50*time.Millisecond || got[0].DurationSum != 60*time.Millisecond {
			t.Errorf("buckets = %+v, want one with 2 requests, 3 bytes, sum 60ms, max 50ms", got)
		}
	})

	t.Run("a time in another zone is bucketed in UTC", func(t *testing.T) {
		s := fresh(t)
		plus2 := time.FixedZone("plus2", 2*3600)
		mustAppend(t, s, request(a, 200, base.In(plus2).Add(5*time.Second), time.Millisecond, 1))
		got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerHour})
		if len(got) != 1 || !got[0].Start.Equal(base) {
			t.Errorf("buckets = %+v, want one starting at %v", got, base)
		}
	})

	t.Run("a rejected batch leaves the rollups alone", func(t *testing.T) {
		s := fresh(t)
		err := s.AppendLog(context.Background(), batchOf(request(a, 200, base, time.Millisecond, 1), store.LogEntry{Host: a, Status: 200}))
		if !errors.Is(err, store.ErrInvalidEntry) {
			t.Fatalf("error = %v, want ErrInvalidEntry", err)
		}
		if got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerMinute}); len(got) != 0 {
			t.Errorf("a rejected batch left %+v in the rollups", got)
		}
	})

	t.Run("deleting raw entries keeps the rollups", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s, sample()...)
		if n, err := s.DeleteLogsBefore(context.Background(), base.Add(24*time.Hour)); err != nil || n != 5 {
			t.Fatalf("DeleteLogsBefore = %d, %v, want 5 removed", n, err)
		}
		if got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerHour}); len(got) != 2 {
			t.Errorf("rollups after deleting the raw entries = %+v, want them kept", got)
		}
	})

	t.Run("DeleteRollupsBefore removes one resolution and keeps the boundary", func(t *testing.T) {
		s := fresh(t)
		mustAppend(t, s,
			request(a, 200, base, time.Millisecond, 1),
			request(a, 200, base.Add(time.Minute), time.Millisecond, 1),
			request(a, 200, base.Add(2*time.Hour), time.Millisecond, 1),
		)
		ctx := context.Background()

		n, err := s.DeleteRollupsBefore(ctx, store.PerMinute, base.Add(time.Minute))
		if err != nil || n != 1 {
			t.Fatalf("DeleteRollupsBefore(minute) = %d, %v, want 1 removed", n, err)
		}
		if got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerMinute}); len(got) != 2 || !got[0].Start.Equal(base.Add(time.Minute)) {
			t.Errorf("minute buckets left = %+v, want the boundary and the later one", got)
		}
		if got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerHour}); len(got) != 2 {
			t.Errorf("hour buckets = %+v, want both untouched", got)
		}

		if n, err := s.DeleteRollupsBefore(ctx, store.PerHour, base.Add(time.Hour)); err != nil || n != 1 {
			t.Errorf("DeleteRollupsBefore(hour) = %d, %v, want 1 removed", n, err)
		}
		if n, err := s.DeleteRollupsBefore(ctx, store.PerHour, base); err != nil || n != 0 {
			t.Errorf("deleting nothing = %d, %v, want 0", n, err)
		}
	})

	t.Run("bad queries", func(t *testing.T) {
		s := fresh(t)
		ctx := context.Background()
		for name, q := range map[string]store.TrafficQuery{
			"no resolution":      {},
			"unknown resolution": {Resolution: 99},
			"negative limit":     {Resolution: store.PerMinute, Limit: -1},
		} {
			if _, err := s.QueryTraffic(ctx, q); !errors.Is(err, store.ErrInvalidQuery) {
				t.Errorf("%s: error = %v, want ErrInvalidQuery", name, err)
			}
		}
		if _, err := s.DeleteRollupsBefore(ctx, 0, base); !errors.Is(err, store.ErrInvalidQuery) {
			t.Errorf("DeleteRollupsBefore without a resolution: error = %v, want ErrInvalidQuery", err)
		}
	})

	t.Run("the row limit keeps the oldest and is capped", func(t *testing.T) {
		s := fresh(t)
		many := make([]store.LogEntry, store.MaxTrafficLimit+5)
		for i := range many {
			many[i] = request(a, 200, base.Add(time.Duration(i)*time.Minute), time.Millisecond, 1)
		}
		mustAppend(t, s, many...)

		got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerMinute, Limit: 2})
		if len(got) != 2 || !got[0].Start.Equal(base) || !got[1].Start.Equal(base.Add(time.Minute)) {
			t.Errorf("limit 2 gave %+v, want the first two minutes", got)
		}
		if got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerMinute}); len(got) != store.MaxTrafficLimit {
			t.Errorf("default limit gave %d rows, want %d", len(got), store.MaxTrafficLimit)
		}
		if got := mustTraffic(t, s, store.TrafficQuery{Resolution: store.PerMinute, Limit: 10 * store.MaxTrafficLimit}); len(got) != store.MaxTrafficLimit {
			t.Errorf("a huge limit gave %d rows, want the cap %d", len(got), store.MaxTrafficLimit)
		}
	})

	t.Run("a cancelled context and a closed store stop the new methods", func(t *testing.T) {
		s := newStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err1 := s.QueryTraffic(ctx, store.TrafficQuery{Resolution: store.PerMinute})
		_, err2 := s.DeleteRollupsBefore(ctx, store.PerMinute, base)
		for name, err := range map[string]error{"QueryTraffic": err1, "DeleteRollupsBefore": err2} {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s with a cancelled context: error = %v, want context.Canceled", name, err)
			}
		}
		s.Close()
		if _, err := s.QueryTraffic(context.Background(), store.TrafficQuery{Resolution: store.PerMinute}); err == nil {
			t.Error("QueryTraffic after Close succeeded, want an error")
		}
		if _, err := s.DeleteRollupsBefore(context.Background(), store.PerMinute, base); err == nil {
			t.Error("DeleteRollupsBefore after Close succeeded, want an error")
		}
	})
}
