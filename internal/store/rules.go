package store

import (
	"errors"
	"fmt"
	"time"
)

// ErrClosed means the store was used after Close.
var ErrClosed = errors.New("store is closed")

// validateBatch checks every entry before anything is written, so a bad entry rejects the whole batch.
func validateBatch(b LogBatch) error {
	for i, e := range b.Entries {
		switch {
		case e.Time.IsZero():
			return fmt.Errorf("%w: entry %d has no time", ErrInvalidEntry, i)
		case e.Host == "":
			return fmt.Errorf("%w: entry %d has no host", ErrInvalidEntry, i)
		}
	}
	return nil
}

// normalize rounds an entry to the precision the stores keep: milliseconds in UTC for the time, microseconds for the
// duration. The ID is cleared because the store assigns it.
func normalize(e LogEntry) LogEntry {
	e.ID = 0
	e.Time = time.UnixMilli(e.Time.UnixMilli()).UTC()
	e.Duration = time.Duration(e.Duration.Microseconds()) * time.Microsecond
	return e
}

// queryLimit applies the default and the cap to the limit of a query.
func queryLimit(q LogQuery) (int, error) {
	switch {
	case q.Limit < 0:
		return 0, fmt.Errorf("%w: negative limit %d", ErrInvalidQuery, q.Limit)
	case q.Limit == 0:
		return DefaultLimit, nil
	case q.Limit > MaxLimit:
		return MaxLimit, nil
	}
	return q.Limit, nil
}

// asciiLower folds ASCII letters only. SQLite's lower() does the same, and both stores must agree.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
