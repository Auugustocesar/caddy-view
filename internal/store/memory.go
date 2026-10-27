package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is a store that lives only as long as the process.
type Memory struct {
	mu      sync.Mutex
	closed  bool
	nextID  int64
	entries []LogEntry // in insertion order, so IDs ascend
	cursors map[string]Cursor
	metrics []MetricSample // in insertion order
	rollups map[rollupKey]rollupRow
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{nextID: 1, cursors: make(map[string]Cursor), rollups: make(map[rollupKey]rollupRow)}
}

// AppendLog stores the entries and the cursor atomically.
func (m *Memory) AppendLog(ctx context.Context, batch LogBatch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateBatch(batch); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	normalized := make([]LogEntry, len(batch.Entries))
	for i, e := range batch.Entries {
		e = normalize(e)
		normalized[i] = e
		e.ID = m.nextID
		m.nextID++
		m.entries = append(m.entries, e)
	}
	for k, row := range rollupBatch(normalized) {
		total := m.rollups[k]
		total.merge(*row)
		m.rollups[k] = total
	}
	if batch.Cursor.Path != "" {
		m.cursors[batch.Cursor.Path] = batch.Cursor
	}
	return nil
}

// QueryLogs returns the entries that match q, newest first.
func (m *Memory) QueryLogs(ctx context.Context, q LogQuery) ([]LogEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, err := queryLimit(q)
	if err != nil {
		return nil, err
	}
	text := asciiLower(q.Text)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	out := []LogEntry{}
	for i := len(m.entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := m.entries[i]
		switch {
		case q.BeforeID != 0 && e.ID >= q.BeforeID,
			!q.From.IsZero() && e.Time.UnixMilli() < q.From.UnixMilli(),
			!q.To.IsZero() && e.Time.UnixMilli() >= q.To.UnixMilli(),
			q.Host != "" && e.Host != q.Host,
			q.MinStatus != 0 && e.Status < q.MinStatus,
			q.MaxStatus != 0 && e.Status > q.MaxStatus,
			text != "" && !strings.Contains(asciiLower(e.Path), text):
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// LoadCursor returns the saved cursor for path and whether there was one.
func (m *Memory) LoadCursor(ctx context.Context, path string) (Cursor, bool, error) {
	if err := ctx.Err(); err != nil {
		return Cursor{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Cursor{}, false, ErrClosed
	}
	c, ok := m.cursors[path]
	return c, ok, nil
}

// DeleteLogsBefore removes the entries older than t and returns how many it removed.
func (m *Memory) DeleteLogsBefore(ctx context.Context, t time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, ErrClosed
	}
	cutoff := t.UnixMilli()
	kept := m.entries[:0]
	for _, e := range m.entries {
		if e.Time.UnixMilli() >= cutoff {
			kept = append(kept, e)
		}
	}
	removed := int64(len(m.entries) - len(kept))
	m.entries = kept
	return removed, nil
}

// AppendMetrics stores the samples atomically. A sample without a name or a time rejects the whole batch.
func (m *Memory) AppendMetrics(ctx context.Context, samples []MetricSample) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateMetrics(samples); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	for _, s := range samples {
		m.metrics = append(m.metrics, normalizeMetric(s))
	}
	return nil
}

// QueryMetrics returns the samples of one series selection, oldest first.
func (m *Memory) QueryMetrics(ctx context.Context, q MetricQuery) ([]MetricSample, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, err := metricLimit(q)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	out := []MetricSample{}
	for _, s := range m.metrics {
		switch {
		case s.Name != q.Name,
			q.Labels != "" && s.Labels != q.Labels,
			!q.From.IsZero() && s.Time.UnixMilli() < q.From.UnixMilli(),
			!q.To.IsZero() && s.Time.UnixMilli() >= q.To.UnixMilli():
			continue
		}
		out = append(out, s)
	}
	// Stable, so samples with the same time keep their insertion order.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// LastMetrics returns the most recently stored sample of every series, keyed by name followed by labels.
func (m *Memory) LastMetrics(ctx context.Context) (map[string]MetricSample, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	out := make(map[string]MetricSample)
	for _, s := range m.metrics {
		key := s.Name + s.Labels
		// Newest by time; of two with the same time, the one stored later.
		if prev, ok := out[key]; !ok || !s.Time.Before(prev.Time) {
			out[key] = s
		}
	}
	return out, nil
}

// Close releases the store. It is safe to call more than once.
func (m *Memory) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.entries, m.cursors, m.metrics, m.rollups = nil, nil, nil, nil
	return nil
}

// QueryTraffic returns the rollup buckets that match q, oldest first.
func (m *Memory) QueryTraffic(ctx context.Context, q TrafficQuery) ([]TrafficBucket, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, err := trafficLimit(q)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	// Hosts are summed unless one is asked for, so rows are grouped by start and class.
	type group struct {
		start int64
		class int
	}
	sums := make(map[group]*rollupRow)
	for k, row := range m.rollups {
		switch {
		case k.res != q.Resolution,
			q.Host != "" && k.host != q.Host,
			!q.From.IsZero() && k.start < q.From.UnixMilli(),
			!q.To.IsZero() && k.start >= q.To.UnixMilli():
			continue
		}
		g := group{k.start, k.class}
		if sums[g] == nil {
			sums[g] = &rollupRow{}
		}
		sums[g].merge(row)
	}
	out := make([]TrafficBucket, 0, len(sums))
	for g, row := range sums {
		out = append(out, rollupKey{start: g.start, class: g.class}.bucket(*row))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].Class < out[j].Class
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// DeleteRollupsBefore removes the buckets of one resolution that start before t and returns how many it removed.
func (m *Memory) DeleteRollupsBefore(ctx context.Context, res Resolution, t time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !res.valid() {
		return 0, fmt.Errorf("%w: unknown resolution %d", ErrInvalidQuery, res)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, ErrClosed
	}
	var removed int64
	for k := range m.rollups {
		if k.res == res && k.start < t.UnixMilli() {
			delete(m.rollups, k)
			removed++
		}
	}
	return removed, nil
}

// DeleteMetricsBefore removes the metric samples older than t and returns how many it removed.
func (m *Memory) DeleteMetricsBefore(ctx context.Context, t time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, ErrClosed
	}
	cutoff := t.UnixMilli()
	kept := m.metrics[:0]
	for _, s := range m.metrics {
		if s.Time.UnixMilli() >= cutoff {
			kept = append(kept, s)
		}
	}
	removed := int64(len(m.metrics) - len(kept))
	m.metrics = kept
	return removed, nil
}

// DeleteOldestLogs removes the n oldest log entries, by insertion order, and returns how many it removed.
func (m *Memory) DeleteOldestLogs(ctx context.Context, n int) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: negative count %d", ErrInvalidQuery, n)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, ErrClosed
	}
	n = min(n, len(m.entries))
	m.entries = append([]LogEntry(nil), m.entries[n:]...)
	return int64(n), nil
}

// DeleteOldestMetrics removes the n oldest metric samples, by time, and returns how many it removed.
func (m *Memory) DeleteOldestMetrics(ctx context.Context, n int) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: negative count %d", ErrInvalidQuery, n)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, ErrClosed
	}
	n = min(n, len(m.metrics))
	order := make([]int, len(m.metrics))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return m.metrics[order[a]].Time.Before(m.metrics[order[b]].Time) })
	drop := make(map[int]bool, n)
	for _, i := range order[:n] {
		drop[i] = true
	}
	kept := make([]MetricSample, 0, len(m.metrics)-n)
	for i, s := range m.metrics {
		if !drop[i] {
			kept = append(kept, s)
		}
	}
	m.metrics = kept
	return int64(n), nil
}

// Size estimates the bytes the store holds, which is enough for a size cap to bound memory.
func (m *Memory) Size(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, ErrClosed
	}
	const row = 64 // fixed part of a row: numbers, slice headers and map overhead, roughly
	var size int64
	for _, e := range m.entries {
		size += row + int64(len(e.Host)+len(e.Method)+len(e.Path)+len(e.ClientIP))
	}
	for _, s := range m.metrics {
		size += row + int64(len(s.Name)+len(s.Labels))
	}
	for k := range m.rollups {
		size += row + 8*LatencyBuckets + int64(len(k.host))
	}
	for _, c := range m.cursors {
		size += row + int64(len(c.Path)+len(c.FileID)+len(c.Fingerprint))
	}
	return size, nil
}
