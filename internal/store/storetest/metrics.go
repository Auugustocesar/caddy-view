package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// MetricStore is the part of a store the metrics contract checks.
type MetricStore interface {
	AppendMetrics(ctx context.Context, samples []store.MetricSample) error
	QueryMetrics(ctx context.Context, q store.MetricQuery) ([]store.MetricSample, error)
	LastMetrics(ctx context.Context) (map[string]store.MetricSample, error)
	Close() error
}

func sample(name, labels string, at time.Time, value, raw float64) store.MetricSample {
	return store.MetricSample{Name: name, Labels: labels, Time: at, Value: value, Raw: raw}
}

func mustAppendMetrics(t *testing.T, s interface {
	AppendMetrics(context.Context, []store.MetricSample) error
}, samples ...store.MetricSample) {
	t.Helper()
	if err := s.AppendMetrics(context.Background(), samples); err != nil {
		t.Fatalf("AppendMetrics: %v", err)
	}
}

func mustQueryMetrics(t *testing.T, s interface {
	QueryMetrics(context.Context, store.MetricQuery) ([]store.MetricSample, error)
}, q store.MetricQuery) []store.MetricSample {
	t.Helper()
	got, err := s.QueryMetrics(context.Background(), q)
	if err != nil {
		t.Fatalf("QueryMetrics(%+v): %v", q, err)
	}
	return got
}

func values(samples []store.MetricSample) []float64 {
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = s.Value
	}
	return out
}

func sameValues(a, b []float64) bool {
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

// RunMetrics checks the metrics part of the contract against stores made by newStore. Every subtest gets a fresh
// store.
func RunMetrics(t *testing.T, newStore func(t *testing.T) MetricStore) {
	t.Helper()
	fresh := func(t *testing.T) MetricStore {
		t.Helper()
		s := newStore(t)
		t.Cleanup(func() { s.Close() })
		return s
	}
	const reqs = "caddy_http_requests_total"

	t.Run("a sample reads back as it was stored", func(t *testing.T) {
		s := fresh(t)
		want := sample(reqs, `{server="srv0"}`, base.Add(1500*time.Millisecond), 7.5, 1234.25)
		mustAppendMetrics(t, s, want)

		got := mustQueryMetrics(t, s, store.MetricQuery{Name: reqs})
		if len(got) != 1 {
			t.Fatalf("got %d samples, want 1", len(got))
		}
		if !got[0].Time.Equal(want.Time) || got[0].Time.Location() != time.UTC {
			t.Errorf("Time = %v, want %v in UTC", got[0].Time, want.Time)
		}
		got[0].Time, want.Time = time.Time{}, time.Time{}
		if got[0] != want {
			t.Errorf("sample = %+v, want %+v", got[0], want)
		}
	})

	t.Run("samples come back oldest first, by time", func(t *testing.T) {
		s := fresh(t)
		// Stored out of order on purpose: charts need time order, not insertion order.
		mustAppendMetrics(t, s,
			sample(reqs, "", base.Add(2*time.Second), 3, 3),
			sample(reqs, "", base, 1, 1),
			sample(reqs, "", base.Add(time.Second), 2, 2),
		)
		if got := values(mustQueryMetrics(t, s, store.MetricQuery{Name: reqs})); !sameValues(got, []float64{1, 2, 3}) {
			t.Errorf("order = %v, want [1 2 3]", got)
		}
	})

	t.Run("selection by name, labels and time", func(t *testing.T) {
		s := fresh(t)
		mustAppendMetrics(t, s,
			sample(reqs, `{server="srv0"}`, base, 1, 1),
			sample(reqs, `{server="srv1"}`, base.Add(time.Minute), 2, 2),
			sample(reqs, `{server="srv0"}`, base.Add(2*time.Minute), 3, 3),
			sample("caddy_http_requests_in_flight", `{server="srv0"}`, base, 4, 4),
		)
		tests := []struct {
			name  string
			query store.MetricQuery
			want  []float64
		}{
			{"a name selects every label set", store.MetricQuery{Name: reqs}, []float64{1, 2, 3}},
			{"labels match exactly", store.MetricQuery{Name: reqs, Labels: `{server="srv0"}`}, []float64{1, 3}},
			{"labels that are only a prefix do not match", store.MetricQuery{Name: reqs, Labels: `{server="srv`}, []float64{}},
			{"another name", store.MetricQuery{Name: "caddy_http_requests_in_flight"}, []float64{4}},
			{"an unknown name", store.MetricQuery{Name: "nothing"}, []float64{}},
			{"time range includes From and excludes To", store.MetricQuery{Name: reqs, From: base.Add(time.Minute), To: base.Add(2 * time.Minute)}, []float64{2}},
			{"only From", store.MetricQuery{Name: reqs, From: base.Add(time.Minute)}, []float64{2, 3}},
			{"only To", store.MetricQuery{Name: reqs, To: base.Add(time.Minute)}, []float64{1}},
		}
		for _, tt := range tests {
			if got := values(mustQueryMetrics(t, s, tt.query)); !sameValues(got, tt.want) {
				t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
			}
		}
	})

	t.Run("a query needs a name and a sane limit", func(t *testing.T) {
		s := fresh(t)
		for name, q := range map[string]store.MetricQuery{
			"no name":        {},
			"negative limit": {Name: reqs, Limit: -1},
		} {
			if _, err := s.QueryMetrics(context.Background(), q); !errors.Is(err, store.ErrInvalidQuery) {
				t.Errorf("%s: error = %v, want ErrInvalidQuery", name, err)
			}
		}
	})

	t.Run("the limit keeps the oldest samples and is capped", func(t *testing.T) {
		s := fresh(t)
		many := make([]store.MetricSample, store.MaxMetricLimit+5)
		for i := range many {
			many[i] = sample(reqs, "", base.Add(time.Duration(i)*time.Second), float64(i), float64(i))
		}
		mustAppendMetrics(t, s, many...)

		if got := mustQueryMetrics(t, s, store.MetricQuery{Name: reqs, Limit: 3}); !sameValues(values(got), []float64{0, 1, 2}) {
			t.Errorf("limit 3 gave %v, want the first three", values(got))
		}
		if got := mustQueryMetrics(t, s, store.MetricQuery{Name: reqs}); len(got) != store.MaxMetricLimit {
			t.Errorf("default limit gave %d samples, want %d", len(got), store.MaxMetricLimit)
		}
		if got := mustQueryMetrics(t, s, store.MetricQuery{Name: reqs, Limit: 10 * store.MaxMetricLimit}); len(got) != store.MaxMetricLimit {
			t.Errorf("a huge limit gave %d samples, want the cap %d", len(got), store.MaxMetricLimit)
		}
	})

	t.Run("a bad sample rejects the whole batch", func(t *testing.T) {
		s := fresh(t)
		for name, bad := range map[string]store.MetricSample{
			"no name": {Time: base, Value: 1},
			"no time": {Name: reqs, Value: 1},
		} {
			err := s.AppendMetrics(context.Background(), []store.MetricSample{sample(reqs, "", base, 1, 1), bad})
			if !errors.Is(err, store.ErrInvalidEntry) {
				t.Errorf("%s: error = %v, want ErrInvalidEntry", name, err)
			}
		}
		if got := mustQueryMetrics(t, s, store.MetricQuery{Name: reqs}); len(got) != 0 {
			t.Errorf("a rejected batch stored %d samples", len(got))
		}
	})

	t.Run("LastMetrics returns the newest sample of each series", func(t *testing.T) {
		s := fresh(t)
		last, err := s.LastMetrics(context.Background())
		if err != nil || len(last) != 0 {
			t.Fatalf("LastMetrics on an empty store = %v, %v, want an empty map", last, err)
		}

		mustAppendMetrics(t, s,
			sample(reqs, `{server="srv0"}`, base, 1, 10),
			sample(reqs, `{server="srv1"}`, base, 5, 50),
		)
		mustAppendMetrics(t, s, sample(reqs, `{server="srv0"}`, base.Add(time.Minute), 2, 12))
		// Newer by insertion but older by time must not win: the contract says newest by time.
		mustAppendMetrics(t, s, sample(reqs, `{server="srv0"}`, base.Add(-time.Hour), 99, 99))

		last, err = s.LastMetrics(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(last) != 2 {
			t.Fatalf("LastMetrics has %d series, want 2: %v", len(last), last)
		}
		if got := last[reqs+`{server="srv0"}`]; got.Raw != 12 || got.Value != 2 {
			t.Errorf("srv0 = %+v, want the sample at base+1m (raw 12)", got)
		}
		if got := last[reqs+`{server="srv1"}`]; got.Raw != 50 {
			t.Errorf("srv1 = %+v, want raw 50", got)
		}
	})

	t.Run("a cancelled context stops every method", func(t *testing.T) {
		s := fresh(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		checks := map[string]error{
			"AppendMetrics": s.AppendMetrics(ctx, []store.MetricSample{sample(reqs, "", base, 1, 1)}),
		}
		_, checks["QueryMetrics"] = s.QueryMetrics(ctx, store.MetricQuery{Name: reqs})
		_, checks["LastMetrics"] = s.LastMetrics(ctx)
		for name, err := range checks {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s with a cancelled context: error = %v, want context.Canceled", name, err)
			}
		}
		if got := mustQueryMetrics(t, s, store.MetricQuery{Name: reqs}); len(got) != 0 {
			t.Errorf("a cancelled AppendMetrics stored %d samples", len(got))
		}
	})

	t.Run("an empty batch is fine and Close ends the use", func(t *testing.T) {
		s := newStore(t)
		if err := s.AppendMetrics(context.Background(), nil); err != nil {
			t.Errorf("AppendMetrics of nothing: %v", err)
		}
		s.Close()
		if err := s.AppendMetrics(context.Background(), []store.MetricSample{sample(reqs, "", base, 1, 1)}); err == nil {
			t.Error("AppendMetrics after Close succeeded, want an error")
		}
		if _, err := s.LastMetrics(context.Background()); err == nil {
			t.Error("LastMetrics after Close succeeded, want an error")
		}
	})
}
