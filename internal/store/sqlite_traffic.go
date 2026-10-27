package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// latencyColumns are the names of the latency counters of traffic_rollup, in bucket order.
func latencyColumns() []string {
	cols := make([]string, LatencyBuckets)
	for i := range cols {
		cols[i] = "lat" + strconv.Itoa(i)
	}
	return cols
}

// upsertRollupSQL adds a row to traffic_rollup, or adds it to the row that is already there: the counters are
// summed and the maximum duration is kept.
func upsertRollupSQL() string {
	cols := latencyColumns()
	names := "resolution, bucket, host, class, requests, bytes, duration_sum_us, duration_max_us, " + strings.Join(cols, ", ")
	marks := strings.TrimSuffix(strings.Repeat("?, ", 8+len(cols)), ", ")

	sets := []string{
		"requests = requests + excluded.requests",
		"bytes = bytes + excluded.bytes",
		"duration_sum_us = duration_sum_us + excluded.duration_sum_us",
		"duration_max_us = max(duration_max_us, excluded.duration_max_us)",
	}
	for _, c := range cols {
		sets = append(sets, c+" = "+c+" + excluded."+c)
	}
	return "INSERT INTO traffic_rollup (" + names + ") VALUES (" + marks + ") " +
		"ON CONFLICT (resolution, bucket, host, class) DO UPDATE SET " + strings.Join(sets, ", ")
}

// addRollups adds the entries, which must be normalized, to the minute and hour rollups. It runs in the transaction
// that stores the entries, which is what keeps the rollups equal to the raw entries.
func addRollups(ctx context.Context, tx *sql.Tx, entries []LogEntry) error {
	stmt, err := tx.PrepareContext(ctx, upsertRollupSQL())
	if err != nil {
		return fmt.Errorf("add rollups: %w", err)
	}
	defer stmt.Close()
	for k, row := range rollupBatch(entries) {
		args := []any{int(k.res), k.start, k.host, k.class, row.count, row.bytes, row.sumUS, row.maxUS}
		for _, n := range row.latency {
			args = append(args, n)
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return fmt.Errorf("add rollups: %w", err)
		}
	}
	return nil
}

// QueryTraffic returns the rollup buckets that match q, oldest first.
func (s *SQLite) QueryTraffic(ctx context.Context, q TrafficQuery) ([]TrafficBucket, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, err := trafficLimit(q)
	if err != nil {
		return nil, err
	}
	if s.closed.Load() {
		return nil, ErrClosed
	}

	cols := latencyColumns()
	sums := make([]string, len(cols))
	for i, c := range cols {
		sums[i] = "SUM(" + c + ")"
	}
	query := "SELECT bucket, class, SUM(requests), SUM(bytes), SUM(duration_sum_us), MAX(duration_max_us), " +
		strings.Join(sums, ", ") + " FROM traffic_rollup WHERE resolution = ?"
	args := []any{int(q.Resolution)}
	if q.Host != "" {
		query += " AND host = ?"
		args = append(args, q.Host)
	}
	if !q.From.IsZero() {
		query += " AND bucket >= ?"
		args = append(args, q.From.UnixMilli())
	}
	if !q.To.IsZero() {
		query += " AND bucket < ?"
		args = append(args, q.To.UnixMilli())
	}
	query += " GROUP BY bucket, class ORDER BY bucket, class LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query traffic: %w", err)
	}
	defer rows.Close()
	out := []TrafficBucket{}
	for rows.Next() {
		var b TrafficBucket
		var start, sumUS, maxUS int64
		dest := []any{&start, &b.Class, &b.Count, &b.Bytes, &sumUS, &maxUS}
		for i := range b.Latency {
			dest = append(dest, &b.Latency[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("query traffic: %w", err)
		}
		b.Start = time.UnixMilli(start).UTC()
		b.DurationSum = time.Duration(sumUS) * time.Microsecond
		b.DurationMax = time.Duration(maxUS) * time.Microsecond
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query traffic: %w", err)
	}
	return out, nil
}

// exec runs a statement that deletes rows and returns how many it removed. Every method that deletes goes through
// it, so the checks of the context and of Close are in one place.
func (s *SQLite) exec(ctx context.Context, what, query string, args ...any) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.closed.Load() {
		return 0, ErrClosed
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", what, err)
	}
	return n, nil
}

// DeleteRollupsBefore removes the buckets of one resolution that start before t and returns how many it removed.
func (s *SQLite) DeleteRollupsBefore(ctx context.Context, res Resolution, t time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !res.valid() {
		return 0, fmt.Errorf("%w: unknown resolution %d", ErrInvalidQuery, res)
	}
	return s.exec(ctx, "delete rollups", "DELETE FROM traffic_rollup WHERE resolution = ? AND bucket < ?", int(res), t.UnixMilli())
}

// DeleteMetricsBefore removes the metric samples older than t and returns how many it removed.
func (s *SQLite) DeleteMetricsBefore(ctx context.Context, t time.Time) (int64, error) {
	return s.exec(ctx, "delete metrics", "DELETE FROM metric_samples WHERE ts < ?", t.UnixMilli())
}

// DeleteOldestLogs removes the n oldest log entries, by insertion order, and returns how many it removed.
func (s *SQLite) DeleteOldestLogs(ctx context.Context, n int) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: negative count %d", ErrInvalidQuery, n)
	}
	return s.exec(ctx, "delete oldest logs",
		"DELETE FROM access_log_entries WHERE id IN (SELECT id FROM access_log_entries ORDER BY id LIMIT ?)", n)
}

// DeleteOldestMetrics removes the n oldest metric samples, by time, and returns how many it removed.
func (s *SQLite) DeleteOldestMetrics(ctx context.Context, n int) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: negative count %d", ErrInvalidQuery, n)
	}
	return s.exec(ctx, "delete oldest metrics",
		"DELETE FROM metric_samples WHERE id IN (SELECT id FROM metric_samples ORDER BY ts, id LIMIT ?)", n)
}

// Size returns the bytes in use: the pages that hold data, not the length of the file. SQLite keeps the pages that
// deleting frees and reuses them, so the file never grows past what was used at its peak.
func (s *SQLite) Size(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.closed.Load() {
		return 0, ErrClosed
	}
	var size int64
	err := s.db.QueryRowContext(ctx, `SELECT
		((SELECT page_count FROM pragma_page_count) - (SELECT freelist_count FROM pragma_freelist_count)) *
		(SELECT page_size FROM pragma_page_size)`).Scan(&size)
	if err != nil {
		return 0, fmt.Errorf("size: %w", err)
	}
	return size, nil
}
