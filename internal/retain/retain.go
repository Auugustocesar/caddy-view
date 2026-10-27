// Package retain keeps the history database bounded: it deletes data past its retention and trims the oldest raw
// data while the database is over its size cap.
package retain

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// Store is the part of the history store that retention needs.
type Store interface {
	DeleteLogsBefore(ctx context.Context, t time.Time) (int64, error)
	DeleteMetricsBefore(ctx context.Context, t time.Time) (int64, error)
	DeleteRollupsBefore(ctx context.Context, res store.Resolution, t time.Time) (int64, error)
	DeleteOldestLogs(ctx context.Context, n int) (int64, error)
	DeleteOldestMetrics(ctx context.Context, n int) (int64, error)
	Size(ctx context.Context) (int64, error)
}

// Policy says how long data is kept. A zero value turns that rule off.
type Policy struct {
	// Raw is how long log entries, metric samples and minute rollups are kept.
	Raw time.Duration
	// Rollups is how long hour rollups are kept.
	Rollups time.Duration
	// MaxBytes is the most the store may hold. Above it, the oldest raw data is deleted.
	MaxBytes int64
}

// Options tunes a Pruner. Only the Policy matters for the zero value.
type Options struct {
	Policy
	// Interval is how often Run prunes. Defaults to 10 minutes.
	Interval time.Duration
	// TrimBatch is how many rows one step of the size cap deletes. Defaults to 10,000.
	TrimBatch int
	// Now gives the current time. Defaults to time.Now.
	Now func() time.Time
	// Logger receives problems from Run. Defaults to a logger that discards them.
	Logger *slog.Logger
}

// Result says what one round deleted.
type Result struct {
	// Logs, Metrics, MinuteRollups and HourRollups were deleted for being past their retention.
	Logs, Metrics, MinuteRollups, HourRollups int64
	// TrimmedLogs and TrimmedMetrics were deleted to get under the size cap.
	TrimmedLogs, TrimmedMetrics int64
	// OverCap means the store is still over the size cap and there was nothing left to trim.
	OverCap bool
}

const (
	defaultInterval  = 10 * time.Minute
	defaultTrimBatch = 10000
)

// Pruner applies a Policy to a store.
type Pruner struct {
	store Store
	opts  Options
}

// New returns a Pruner for s.
func New(s Store, opts Options) *Pruner {
	if opts.Interval <= 0 {
		opts.Interval = defaultInterval
	}
	if opts.TrimBatch <= 0 {
		opts.TrimBatch = defaultTrimBatch
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Pruner{store: s, opts: opts}
}

// Once deletes what is past its retention, then trims the oldest raw data while the store is over its size cap.
func (p *Pruner) Once(ctx context.Context) (Result, error) {
	var res Result
	now := p.opts.Now()
	var err error

	if p.opts.Raw > 0 {
		cutoff := now.Add(-p.opts.Raw)
		if res.Logs, err = p.store.DeleteLogsBefore(ctx, cutoff); err != nil {
			return res, fmt.Errorf("delete old log entries: %w", err)
		}
		if res.Metrics, err = p.store.DeleteMetricsBefore(ctx, cutoff); err != nil {
			return res, fmt.Errorf("delete old metric samples: %w", err)
		}
		if res.MinuteRollups, err = p.store.DeleteRollupsBefore(ctx, store.PerMinute, cutoff); err != nil {
			return res, fmt.Errorf("delete old minute rollups: %w", err)
		}
	}
	if p.opts.Rollups > 0 {
		if res.HourRollups, err = p.store.DeleteRollupsBefore(ctx, store.PerHour, now.Add(-p.opts.Rollups)); err != nil {
			return res, fmt.Errorf("delete old hour rollups: %w", err)
		}
	}
	if p.opts.MaxBytes > 0 {
		if err := p.trim(ctx, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// trim deletes the oldest logs, and once there are none the oldest metric samples, in batches, until the store is
// under the cap or there is nothing left to delete. Rollups are never trimmed.
func (p *Pruner) trim(ctx context.Context, res *Result) error {
	size, err := p.store.Size(ctx)
	if err != nil {
		return fmt.Errorf("measure the database: %w", err)
	}
	for size > p.opts.MaxBytes {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := p.store.DeleteOldestLogs(ctx, p.opts.TrimBatch)
		if err != nil {
			return fmt.Errorf("trim log entries: %w", err)
		}
		res.TrimmedLogs += n
		if n == 0 {
			if n, err = p.store.DeleteOldestMetrics(ctx, p.opts.TrimBatch); err != nil {
				return fmt.Errorf("trim metric samples: %w", err)
			}
			res.TrimmedMetrics += n
		}
		if n == 0 {
			res.OverCap = true
			return nil
		}
		if size, err = p.store.Size(ctx); err != nil {
			return fmt.Errorf("measure the database: %w", err)
		}
	}
	return nil
}

// Run prunes at once and then every Interval until ctx is done, and then returns nil. A failing round is logged and
// tried again.
func (p *Pruner) Run(ctx context.Context) error {
	ticker := time.NewTicker(p.opts.Interval)
	defer ticker.Stop()
	var lastErr string
	for {
		res, err := p.Once(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil && err.Error() != lastErr:
			// Logged when it first happens or changes, not once per round.
			p.opts.Logger.Warn("retention failed, will retry", "error", err)
			lastErr = err.Error()
		case err == nil:
			lastErr = ""
			p.report(res)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// report logs what a round deleted, when it deleted anything, and warns when the size cap cannot be met.
func (p *Pruner) report(res Result) {
	if res.TrimmedLogs+res.TrimmedMetrics > 0 {
		p.opts.Logger.Warn("the database was over its size cap, the oldest data was deleted",
			"logs", res.TrimmedLogs, "metric_samples", res.TrimmedMetrics, "max_bytes", p.opts.MaxBytes)
	}
	if res.OverCap {
		p.opts.Logger.Warn("the database is still over its size cap and has no raw data left to delete", "max_bytes", p.opts.MaxBytes)
	}
	if n := res.Logs + res.Metrics + res.MinuteRollups + res.HourRollups; n > 0 {
		p.opts.Logger.Info("deleted data past its retention", "logs", res.Logs, "metric_samples", res.Metrics,
			"minute_rollups", res.MinuteRollups, "hour_rollups", res.HourRollups)
	}
}
