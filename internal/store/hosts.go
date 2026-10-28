package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// DefaultHostLimit is how many hosts QueryHosts returns when the query does not say.
	DefaultHostLimit = 100
	// MaxHostLimit caps the hosts QueryHosts returns in one call.
	MaxHostLimit = 500
)

// HostQuery selects the traffic to total per host. Resolution is required.
type HostQuery struct {
	Resolution Resolution
	// From and To bound the bucket start as [From, To), as in TrafficQuery. A zero value leaves that side open.
	From, To time.Time
	// Limit is the most hosts to return, the busiest first. Zero means DefaultHostLimit, and larger values are capped
	// at MaxHostLimit.
	Limit int
}

// HostTotals is what one host received over a range.
type HostTotals struct {
	// Host is the host as it was logged.
	Host string
	// Requests and Bytes are the number of requests and the response bytes sent.
	Requests, Bytes int64
	// Status counts requests by status class: index 1 to 5 for 1xx to 5xx, index 0 for anything else.
	Status [6]int64
	// DurationSum adds up the request durations, and DurationMax is the longest one.
	DurationSum, DurationMax time.Duration
	// Latency counts requests per latency bucket, see LatencyUpperBounds.
	Latency [LatencyBuckets]int64
}

// hostLimit checks a host query and applies the default and the cap to its limit.
func hostLimit(q HostQuery) (int, error) {
	switch {
	case !q.Resolution.valid():
		return 0, fmt.Errorf("%w: unknown resolution %d", ErrInvalidQuery, q.Resolution)
	case q.Limit < 0:
		return 0, fmt.Errorf("%w: negative limit %d", ErrInvalidQuery, q.Limit)
	case q.Limit == 0:
		return DefaultHostLimit, nil
	}
	return min(q.Limit, MaxHostLimit), nil
}

// hostSums totals rollup rows per host.
type hostSums map[string]*HostTotals

// add puts one rollup row of a status class into the totals of its host.
func (s hostSums) add(host string, class int, row rollupRow) {
	t := s[host]
	if t == nil {
		t = &HostTotals{Host: host}
		s[host] = t
	}
	t.Requests += row.count
	t.Bytes += row.bytes
	if class >= 0 && class < len(t.Status) {
		t.Status[class] += row.count
	}
	t.DurationSum += time.Duration(row.sumUS) * time.Microsecond
	t.DurationMax = max(t.DurationMax, time.Duration(row.maxUS)*time.Microsecond)
	for i, n := range row.latency {
		t.Latency[i] += n
	}
}

// busiest returns the totals, the busiest host first and then by name, at most limit of them. The list is never nil.
func (s hostSums) busiest(limit int) []HostTotals {
	out := make([]HostTotals, 0, len(s))
	for _, t := range s {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].Host < out[j].Host
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// QueryHosts totals the rollups per host, the busiest first and then by name.
func (m *Memory) QueryHosts(ctx context.Context, q HostQuery) ([]HostTotals, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, err := hostLimit(q)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	sums := hostSums{}
	for k, row := range m.rollups {
		switch {
		case k.res != q.Resolution,
			!q.From.IsZero() && k.start < q.From.UnixMilli(),
			!q.To.IsZero() && k.start >= q.To.UnixMilli():
			continue
		}
		sums.add(k.host, k.class, row)
	}
	return sums.busiest(limit), nil
}

// QueryHosts totals the rollups per host, the busiest first and then by name. The busiest hosts are chosen in the
// query, so that a flood of made-up Host headers does not make it read every host there ever was.
func (s *SQLite) QueryHosts(ctx context.Context, q HostQuery) ([]HostTotals, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, err := hostLimit(q)
	if err != nil {
		return nil, err
	}
	if s.closed.Load() {
		return nil, ErrClosed
	}

	where := "resolution = ?"
	args := []any{int(q.Resolution)}
	if !q.From.IsZero() {
		where += " AND bucket >= ?"
		args = append(args, q.From.UnixMilli())
	}
	if !q.To.IsZero() {
		where += " AND bucket < ?"
		args = append(args, q.To.UnixMilli())
	}

	cols := latencyColumns()
	sums := make([]string, len(cols))
	for i, c := range cols {
		sums[i] = "SUM(" + c + ")"
	}
	query := "SELECT host, class, SUM(requests), SUM(bytes), SUM(duration_sum_us), MAX(duration_max_us), " +
		strings.Join(sums, ", ") + " FROM traffic_rollup WHERE " + where +
		" AND host IN (SELECT host FROM traffic_rollup WHERE " + where +
		" GROUP BY host ORDER BY SUM(requests) DESC, host ASC LIMIT ?) GROUP BY host, class"
	all := append(append(append([]any{}, args...), args...), limit)

	rows, err := s.db.QueryContext(ctx, query, all...)
	if err != nil {
		return nil, fmt.Errorf("query hosts: %w", err)
	}
	defer rows.Close()
	totals := hostSums{}
	for rows.Next() {
		var host string
		var class int
		var row rollupRow
		dest := []any{&host, &class, &row.count, &row.bytes, &row.sumUS, &row.maxUS}
		for i := range row.latency {
			dest = append(dest, &row.latency[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("query hosts: %w", err)
		}
		totals.add(host, class, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query hosts: %w", err)
	}
	return totals.busiest(limit), nil
}
