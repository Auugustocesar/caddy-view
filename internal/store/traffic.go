package store

import (
	"fmt"
	"math"
	"time"
)

// Resolution is the width of a traffic rollup bucket.
type Resolution int

const (
	// PerMinute buckets are aligned to the minute, UTC.
	PerMinute Resolution = iota + 1
	// PerHour buckets are aligned to the hour, UTC.
	PerHour
)

// MaxTrafficLimit caps the rows QueryTraffic returns in one call, and is the default.
const MaxTrafficLimit = 10000

// LatencyBuckets is the number of counters in a latency histogram: one per upper bound plus an open one.
const LatencyBuckets = 12

// latencyBounds are the upper bounds, inclusive, of the finite latency buckets.
var latencyBounds = [LatencyBuckets - 1]time.Duration{
	5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond,
	100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond,
	time.Second, 2500 * time.Millisecond, 5 * time.Second, 10 * time.Second,
}

// LatencyUpperBounds returns the inclusive upper bounds of the finite latency buckets, in order. The last bucket of
// a histogram has no bound.
func LatencyUpperBounds() []time.Duration {
	out := make([]time.Duration, len(latencyBounds))
	copy(out, latencyBounds[:])
	return out
}

// LatencyQuantile estimates a quantile (0 to 1) from a latency histogram as the upper bound of the bucket that holds
// it. For the open bucket it returns the last finite bound, which means "at least". An empty histogram gives 0.
func LatencyQuantile(latency [LatencyBuckets]int64, q float64) time.Duration {
	var total int64
	for _, n := range latency {
		total += n
	}
	if total == 0 {
		return 0
	}
	rank := int64(math.Ceil(min(max(q, 0), 1) * float64(total)))
	rank = max(rank, 1)
	var seen int64
	for i, n := range latency {
		seen += n
		if seen >= rank {
			return latencyBounds[min(i, len(latencyBounds)-1)]
		}
	}
	return latencyBounds[len(latencyBounds)-1]
}

// TrafficQuery selects rollup buckets. Resolution is required.
type TrafficQuery struct {
	Resolution Resolution
	// From and To bound the bucket start as [From, To). A zero value leaves that side open.
	From, To time.Time
	// Host, when not empty, selects one host. Otherwise the hosts are summed.
	Host string
	// Limit is the maximum number of rows. Zero means MaxTrafficLimit, and larger values are capped.
	Limit int
}

// TrafficBucket is the traffic of one status class during one bucket of time.
type TrafficBucket struct {
	// Start is the beginning of the bucket, UTC.
	Start time.Time
	// Class is the status class: 1 to 5 for 1xx to 5xx, 0 for anything else.
	Class int
	// Count is the number of requests and Bytes the response bytes sent.
	Count, Bytes int64
	// DurationSum adds up the request durations, and DurationMax is the longest one.
	DurationSum, DurationMax time.Duration
	// Latency counts requests per latency bucket, see LatencyUpperBounds.
	Latency [LatencyBuckets]int64
}

// valid reports whether r is one of the resolutions.
func (r Resolution) valid() bool { return r == PerMinute || r == PerHour }

// width is the length of a bucket in milliseconds.
func (r Resolution) width() int64 {
	if r == PerHour {
		return time.Hour.Milliseconds()
	}
	return time.Minute.Milliseconds()
}

// bucketStart returns the start, in Unix milliseconds, of the bucket that holds the time ts.
func (r Resolution) bucketStart(ts int64) int64 { return ts - ((ts%r.width())+r.width())%r.width() }

// trafficLimit checks a traffic query and applies the default and the cap to its limit.
func trafficLimit(q TrafficQuery) (int, error) {
	switch {
	case !q.Resolution.valid():
		return 0, fmt.Errorf("%w: unknown resolution %d", ErrInvalidQuery, q.Resolution)
	case q.Limit < 0:
		return 0, fmt.Errorf("%w: negative limit %d", ErrInvalidQuery, q.Limit)
	case q.Limit == 0 || q.Limit > MaxTrafficLimit:
		return MaxTrafficLimit, nil
	}
	return q.Limit, nil
}

// statusClass is 1 to 5 for the status codes 100 to 599, and 0 for anything else.
func statusClass(status int) int {
	if status < 100 || status > 599 {
		return 0
	}
	return status / 100
}

// latencyBucket is the index of the latency bucket that holds d.
func latencyBucket(d time.Duration) int {
	for i, bound := range latencyBounds {
		if d <= bound {
			return i
		}
	}
	return LatencyBuckets - 1
}

// rollupKey identifies one row of the rollup table.
type rollupKey struct {
	res   Resolution
	start int64 // Unix milliseconds
	host  string
	class int
}

// rollupRow holds the sums of one rollup row. Durations are in microseconds.
type rollupRow struct {
	count, bytes, sumUS, maxUS int64
	latency                    [LatencyBuckets]int64
}

// add counts one entry, which must already be normalized.
func (r *rollupRow) add(e LogEntry) {
	r.count++
	r.bytes += e.Bytes
	us := e.Duration.Microseconds()
	r.sumUS += us
	r.maxUS = max(r.maxUS, us)
	r.latency[latencyBucket(e.Duration)]++
}

// merge adds another row to r.
func (r *rollupRow) merge(o rollupRow) {
	r.count += o.count
	r.bytes += o.bytes
	r.sumUS += o.sumUS
	r.maxUS = max(r.maxUS, o.maxUS)
	for i, n := range o.latency {
		r.latency[i] += n
	}
}

// bucket turns a row into the value the stores return.
func (k rollupKey) bucket(r rollupRow) TrafficBucket {
	return TrafficBucket{
		Start: time.UnixMilli(k.start).UTC(), Class: k.class, Count: r.count, Bytes: r.bytes,
		DurationSum: time.Duration(r.sumUS) * time.Microsecond, DurationMax: time.Duration(r.maxUS) * time.Microsecond,
		Latency: r.latency,
	}
}

// rollupBatch sums the entries of a batch into the rows they belong to, for both resolutions. The entries must be
// normalized.
func rollupBatch(entries []LogEntry) map[rollupKey]*rollupRow {
	out := make(map[rollupKey]*rollupRow)
	for _, e := range entries {
		for _, res := range []Resolution{PerMinute, PerHour} {
			k := rollupKey{res: res, start: res.bucketStart(e.Time.UnixMilli()), host: e.Host, class: statusClass(e.Status)}
			row := out[k]
			if row == nil {
				row = &rollupRow{}
				out[k] = row
			}
			row.add(e)
		}
	}
	return out
}
