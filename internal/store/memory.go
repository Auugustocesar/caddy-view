package store

import (
	"context"
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
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{nextID: 1, cursors: make(map[string]Cursor)}
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
	for _, e := range batch.Entries {
		e = normalize(e)
		e.ID = m.nextID
		m.nextID++
		m.entries = append(m.entries, e)
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
	m.entries, m.cursors, m.metrics = nil, nil, nil
	return nil
}
