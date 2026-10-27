// Package store keeps access logs, ingestion cursors and, in later slices, metrics and rollups.
//
// There are two implementations, SQLite for real use and Memory for tests and --no-persist. Both satisfy the same
// contract, which storetest checks. Timestamps are kept as Unix milliseconds in UTC and durations as microseconds,
// so values read back are rounded to that precision.
package store

import (
	"errors"
	"time"
)

// ErrInvalidEntry means a log entry cannot be stored: it has no time or no host.
var ErrInvalidEntry = errors.New("invalid log entry")

// ErrInvalidQuery means a log query has an impossible parameter, such as a negative limit.
var ErrInvalidQuery = errors.New("invalid log query")

// ErrSchemaTooNew means the database was written by a newer caddy-view than this one.
var ErrSchemaTooNew = errors.New("database schema is newer than this version of caddy-view")

const (
	// DefaultLimit is how many entries QueryLogs returns when the query does not say.
	DefaultLimit = 100
	// MaxLimit caps the entries QueryLogs returns in one call.
	MaxLimit = 1000
)

// LogEntry is one access log line, reduced to what the UI searches and summarises.
type LogEntry struct {
	// ID is assigned by the store and grows with insertion order. It is ignored on insert.
	ID       int64
	Time     time.Time
	Host     string
	Method   string
	Path     string
	Status   int
	Duration time.Duration
	Bytes    int64
	ClientIP string
}

// Cursor is how far a log file has been ingested.
type Cursor struct {
	// Path is the log file the cursor belongs to.
	Path string
	// FileID identifies the file on its file system (inode, or the Windows file index), so a different file at the
	// same path is noticed.
	FileID string
	// Fingerprint is a hash of the first bytes of the file, which notices a file replaced in place.
	Fingerprint string
	// Offset is the number of bytes already consumed.
	Offset int64
	// SkippedLines counts lines that could not be parsed.
	SkippedLines int64
	// Gaps counts rotations or truncations that may have dropped lines.
	Gaps int64
}

// LogBatch is what ingestion hands to the store: entries and the cursor that comes after them.
type LogBatch struct {
	Entries []LogEntry
	Cursor  Cursor
}

// LogQuery selects log entries. The zero value selects the newest DefaultLimit entries.
type LogQuery struct {
	// From and To bound the time as [From, To). A zero value leaves that side open.
	From, To time.Time
	// Host, when not empty, matches the host exactly.
	Host string
	// MinStatus and MaxStatus bound the status code, inclusive. Zero leaves that side open.
	MinStatus, MaxStatus int
	// Text, when not empty, must appear in the path. The match ignores case and takes the text literally.
	Text string
	// BeforeID, when not zero, returns only entries older than that ID, to page backwards.
	BeforeID int64
	// Limit is the maximum number of entries. Zero means DefaultLimit, and values above MaxLimit are capped.
	Limit int
}
