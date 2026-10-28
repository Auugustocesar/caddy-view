// Package ingest reads Caddy's JSON access log into the history store. It resumes from a persistent cursor, so a
// restart neither repeats nor skips lines, and it notices rotation and truncation of the log file.
package ingest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// ErrBadLine means a line is not a usable access log entry: it is not JSON, or it lacks the request, host or time.
var ErrBadLine = errors.New("not an access log line")

// Store is the part of the history store that ingestion needs.
type Store interface {
	AppendLog(ctx context.Context, batch store.LogBatch) error
	LoadCursor(ctx context.Context, path string) (store.Cursor, bool, error)
}

// Options tunes an Ingester. Only Path is required.
type Options struct {
	// Path is the access log file to ingest.
	Path string
	// KeepQuery keeps the query string in the stored path. By default it is dropped, because it often carries tokens.
	KeepQuery bool
	// BatchSize is how many entries are stored per transaction. Defaults to 500.
	BatchSize int
	// MaxLineBytes is the longest line that is parsed; longer ones are skipped and counted. Defaults to 1 MiB.
	MaxLineBytes int
	// PollInterval is how often Run looks for new data. Defaults to 1 s.
	PollInterval time.Duration
	// Logger receives ingestion errors from Run. Defaults to a logger that discards them.
	Logger *slog.Logger
}

const (
	defaultBatchSize    = 500
	defaultMaxLineBytes = 1 << 20
	defaultPollInterval = time.Second
	readBufferBytes     = 64 << 10
	// fingerprintBytes is how much of the start of the file identifies it.
	fingerprintBytes = 256
)

// Ingester copies one access log file into a store.
type Ingester struct {
	store Store
	opts  Options

	mu     sync.Mutex
	status Status
}

// New returns an Ingester that writes to s.
func New(s Store, opts Options) *Ingester {
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultBatchSize
	}
	if opts.MaxLineBytes <= 0 {
		opts.MaxLineBytes = defaultMaxLineBytes
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPollInterval
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Ingester{store: s, opts: opts, status: Status{State: StateWaiting, Path: opts.Path}}
}

// Once polls the file one time: it ingests everything added since the cursor, and returns when it reaches the end of
// the last complete line. A missing file is not an error: there is nothing to read yet. The outcome is what Status
// reports afterwards.
func (i *Ingester) Once(ctx context.Context) error {
	err := i.poll(ctx)
	i.record(ctx, err)
	return err
}

// record keeps the outcome of a poll for Status. A poll cut short by the end of the program says nothing.
func (i *Ingester) record(ctx context.Context, pollErr error) {
	status := Status{Path: i.opts.Path}
	switch {
	case pollErr != nil && ctx.Err() != nil:
		return
	case pollErr != nil:
		status.State, status.Error = StateFailing, pollErr.Error()
	default:
		status.State = StateWaiting // until a file is found
		if _, err := os.Stat(i.opts.Path); err == nil {
			cursor, found, err := i.store.LoadCursor(ctx, i.opts.Path)
			if err != nil {
				status.State, status.Error = StateFailing, fmt.Sprintf("load cursor: %v", err)
				break
			}
			status.State = StateFollowing
			if found {
				status.Offset, status.SkippedLines, status.Gaps = cursor.Offset, cursor.SkippedLines, cursor.Gaps
			}
		}
	}
	i.mu.Lock()
	i.status = status
	i.mu.Unlock()
}

// Status reports the state of the last poll. It is safe to call while Run is going.
func (i *Ingester) Status() Status {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.status
}

// Run calls Once every PollInterval until ctx is done, and then returns nil. A failing poll is logged and retried:
// the cursor only moves when entries are stored, so nothing is lost or repeated.
func (i *Ingester) Run(ctx context.Context) error {
	ticker := time.NewTicker(i.opts.PollInterval)
	defer ticker.Stop()
	var lastErr string
	for {
		err := i.Once(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil && err.Error() != lastErr:
			// Logged when it first happens or changes, not once per poll.
			i.opts.Logger.Warn("access log ingestion failed, will retry", "path", i.opts.Path, "error", err)
			lastErr = err.Error()
		case err == nil && lastErr != "":
			i.opts.Logger.Info("access log ingestion recovered", "path", i.opts.Path)
			lastErr = ""
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// poll ingests everything that was added to the file since the cursor, and returns when it reaches the end of the
// last complete line. A missing file is not an error: there is nothing to read yet.
func (i *Ingester) poll(ctx context.Context) error {
	path := i.opts.Path
	saved, found, err := i.store.LoadCursor(ctx, path)
	if err != nil {
		return fmt.Errorf("load cursor: %w", err)
	}
	if !found {
		saved = store.Cursor{Path: path}
	}

	if info, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("access log: %w", err)
	} else if info.IsDir() {
		return fmt.Errorf("access log: %s is a directory", path)
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // rotated away between the stat and the open
	}
	if err != nil {
		return fmt.Errorf("access log: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("access log: %w", err)
	}

	cursor := saved
	id := fileID(path, info)
	if found && replaced(f, cursor, id, info.Size()) {
		// Lines written after the last poll and before the rotation may be gone.
		cursor = store.Cursor{Path: path, SkippedLines: cursor.SkippedLines, Gaps: cursor.Gaps + 1}
	}
	cursor.FileID = id
	return i.read(ctx, f, cursor, saved)
}

// replaced reports whether the file at the cursor's path is not the file the cursor was saved for.
func replaced(f *os.File, c store.Cursor, id string, size int64) bool {
	switch {
	case c.FileID != "" && id != "" && c.FileID != id:
		return true // another file took the path
	case size < c.Offset:
		return true // truncated
	}
	return c.Fingerprint != "" && c.Fingerprint != fingerprint(f, c.Fingerprint)
}

// fingerprint hashes the start of the file. The length that was hashed is part of the result ("n:hash"), so a
// fingerprint saved when the file was short can be recomputed over the same bytes after it grew. The reference
// is a previous fingerprint whose length is reused; an empty or malformed reference hashes nothing.
func fingerprint(f *os.File, reference string) string {
	lenPart, _, _ := strings.Cut(reference, ":")
	n, err := strconv.Atoi(lenPart)
	if err != nil || n <= 0 {
		return ""
	}
	return fingerprintOf(f, n)
}

// fingerprintOf hashes the first n bytes of f, or returns "" when the file is shorter than that.
func fingerprintOf(f *os.File, n int) string {
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, 0); err != nil {
		return ""
	}
	sum := sha256.Sum256(buf)
	return strconv.Itoa(n) + ":" + hex.EncodeToString(sum[:])
}

// read consumes the complete lines from the cursor's offset, storing them in batches. saved is the cursor as the
// store has it, to know whether anything changed.
func (i *Ingester) read(ctx context.Context, f *os.File, cursor, saved store.Cursor) error {
	if _, err := f.Seek(cursor.Offset, io.SeekStart); err != nil {
		return fmt.Errorf("access log: %w", err)
	}
	r := bufio.NewReaderSize(f, readBufferBytes)

	var (
		entries []store.LogEntry
		line    []byte // a line longer than the buffer, being put together
		tooLong bool   // the line being read is over MaxLineBytes and is dropped
	)
	flush := func() error {
		cursor.Fingerprint = fingerprintOf(f, min(int(cursor.Offset), fingerprintBytes))
		if err := i.store.AppendLog(ctx, store.LogBatch{Entries: entries, Cursor: cursor}); err != nil {
			return fmt.Errorf("store log entries: %w", err)
		}
		saved, entries = cursor, entries[:0]
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// Part of a long line: keep it only while it can still be under the cap.
			if !tooLong {
				line = append(line, chunk...)
				tooLong = len(line) > i.opts.MaxLineBytes
			}
			cursor.Offset += int64(len(chunk))
			continue
		}
		if err != nil {
			// io.EOF: whatever follows the last newline is an unfinished line, read again once it is complete.
			// Anything else is a read error; either way the position stays at the last complete line.
			if !errors.Is(err, io.EOF) {
				return fmt.Errorf("access log: %w", err)
			}
			break
		}

		cursor.Offset += int64(len(chunk))
		if !tooLong {
			line = append(line, chunk...)
			tooLong = len(line) > i.opts.MaxLineBytes+1 // the newline is not part of the line
		}
		if tooLong {
			cursor.SkippedLines++
		} else if entry, err := ParseLine(bytes.TrimRight(line, "\r\n"), i.opts.KeepQuery); err != nil {
			cursor.SkippedLines++
		} else {
			entries = append(entries, entry)
		}
		line, tooLong = line[:0], false

		if len(entries) >= i.opts.BatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if len(entries) > 0 || cursor != saved {
		return flush()
	}
	return nil
}
