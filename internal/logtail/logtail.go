// Package logtail follows a log file like `tail -F`, emitting each complete line as it is written.
package logtail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"time"
	"unicode/utf8"
)

const (
	defaultPollInterval = 250 * time.Millisecond
	defaultMaxLineBytes = 1 << 20
	truncatedSuffix     = " [truncated]"
	readChunk           = 64 << 10
	// maxBacklogScan bounds how far back from the end of the file the backlog is searched for lines.
	maxBacklogScan = 32 << 20
)

// Options tunes a Tailer. The zero value is valid.
type Options struct {
	// Backlog is how many of the last complete lines to emit before following. Zero means start at the end.
	Backlog int
	// PollInterval is how often the file is checked for new data. Defaults to 250 ms.
	PollInterval time.Duration
	// MaxLineBytes caps the length of an emitted line. Longer lines are cut and marked. Defaults to 1 MiB.
	MaxLineBytes int
}

// Tailer follows one file. Create it with New, then call Run.
//
// The file is only opened while new data is read, never held open between polls, so a log roller can rename or
// delete it at any time, on Windows as well.
type Tailer struct {
	path string
	opts Options

	// initial holds the backlog, emitted first by Run.
	initial []string
	// info identifies the file being followed, so a replaced file is noticed. It is nil until the file exists.
	info os.FileInfo
	// offset is how much of the file has been consumed.
	offset int64

	// pending is the start of the line being read, which has not seen its newline yet.
	pending []byte
	// cut means pending hit MaxLineBytes: the rest of the line is dropped and the line is emitted marked.
	cut bool
	// drop means the rest of the current line is dropped without emitting anything.
	drop bool
}

// New records where to start following path: the end of the file, or the start of its last Backlog lines.
// The file does not need to exist yet. A path that is a directory is an error.
func New(path string, opts Options) (*Tailer, error) {
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPollInterval
	}
	if opts.MaxLineBytes <= 0 {
		opts.MaxLineBytes = defaultMaxLineBytes
	}
	t := &Tailer{path: path, opts: opts}

	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return t, nil
	case err != nil:
		return nil, fmt.Errorf("logtail: %w", err)
	case info.IsDir():
		return nil, fmt.Errorf("logtail: %s is a directory", path)
	}
	t.info = info

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("logtail: %w", err)
	}
	defer f.Close()
	if err := t.position(f, info.Size()); err != nil {
		return nil, fmt.Errorf("logtail: %w", err)
	}
	return t, nil
}

// position sets the offset (and the backlog) for a file of the given size.
func (t *Tailer) position(f *os.File, size int64) error {
	t.offset = size
	if size == 0 {
		return nil
	}
	if t.opts.Backlog <= 0 {
		// Starting in the middle of a line would emit its tail as if it were a whole line.
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, size-1); err != nil {
			return err
		}
		t.drop = last[0] != '\n'
		return nil
	}

	buf, start, err := readTail(f, size, t.opts.Backlog)
	if err != nil {
		return err
	}
	lines, end := completeLines(buf, start)
	if end == 0 {
		t.drop = true // a partial line whose start is out of reach
		return nil
	}
	// Only complete lines are backlog. What follows the last newline is read later, once it is finished.
	t.offset = start + int64(end)
	for i := len(lines) - 1; i >= 0 && len(t.initial) < t.opts.Backlog; i-- {
		if line, ok := t.format(lines[i], false); ok {
			t.initial = append(t.initial, line)
		}
	}
	slices.Reverse(t.initial)
	return nil
}

// completeLines splits the complete lines out of buf, which starts at file offset start. It also returns how many
// bytes of buf they span, newlines included. The first line is left out when buf does not start at the beginning of
// the file, because it may have started earlier.
func completeLines(buf []byte, start int64) (lines [][]byte, end int) {
	end = bytes.LastIndexByte(buf, '\n') + 1
	if end == 0 {
		return nil, 0
	}
	lines = bytes.Split(buf[:end-1], []byte{'\n'})
	if start > 0 {
		lines = lines[1:]
	}
	return lines, end
}

// readTail reads backwards from the end of the file until it holds n non-empty complete lines, or the scan limit or
// the start of the file is reached. It returns the data and the file offset it starts at.
func readTail(f *os.File, size int64, n int) (buf []byte, start int64, err error) {
	start = size
	for start > 0 && size-start < maxBacklogScan {
		step := min(int64(readChunk), start)
		start -= step
		chunk := make([]byte, step)
		if _, err := f.ReadAt(chunk, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, 0, err
		}
		buf = append(chunk, buf...)
		lines, _ := completeLines(buf, start)
		nonEmpty := 0
		for _, l := range lines {
			if len(bytes.TrimSuffix(l, []byte{'\r'})) > 0 {
				nonEmpty++
			}
		}
		if nonEmpty >= n {
			break
		}
	}
	return buf, start, nil
}

// Run emits lines through emit, in order, until ctx is done, and then returns nil. emit is called from the
// calling goroutine only, so a slow emit slows the tailer down instead of buffering without bound.
func (t *Tailer) Run(ctx context.Context, emit func(line string)) error {
	for _, line := range t.initial {
		emit(line)
	}
	t.initial = nil

	ticker := time.NewTicker(t.opts.PollInterval)
	defer ticker.Stop()
	for {
		t.poll(ctx, emit)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// poll reads whatever was added to the file since the last poll. Errors are not fatal: the file may be mid-rotation,
// so the next poll simply tries again.
func (t *Tailer) poll(ctx context.Context, emit func(string)) {
	info, err := os.Stat(t.path)
	if err != nil || info.IsDir() {
		if errors.Is(err, fs.ErrNotExist) {
			t.reset(nil) // removed: whatever appears next is a new file
		}
		return
	}
	switch {
	case t.info == nil || !os.SameFile(t.info, info):
		t.reset(info) // created later, or replaced by the roller: read it from the start
	case info.Size() < t.offset:
		t.reset(info) // truncated in place
	}
	if info.Size() == t.offset {
		return
	}

	f, err := os.Open(t.path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return
	}
	buf := make([]byte, readChunk)
	for ctx.Err() == nil {
		n, err := f.Read(buf)
		t.offset += int64(n)
		t.consume(buf[:n], emit)
		if err != nil {
			return
		}
	}
}

// reset starts following a file from its beginning, forgetting any half-read line.
func (t *Tailer) reset(info os.FileInfo) {
	t.info = info
	t.offset = 0
	t.pending = t.pending[:0]
	t.cut, t.drop = false, false
}

// consume splits data into lines, emitting each one that is complete.
func (t *Tailer) consume(data []byte, emit func(string)) {
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			t.add(data)
			return
		}
		t.add(data[:i])
		t.finish(emit)
		data = data[i+1:]
	}
}

// add appends part of a line, up to MaxLineBytes.
func (t *Tailer) add(part []byte) {
	if t.cut || t.drop {
		return
	}
	room := t.opts.MaxLineBytes - len(t.pending)
	if len(part) > room {
		part = part[:room]
		t.cut = true
	}
	t.pending = append(t.pending, part...)
}

// finish ends the current line.
func (t *Tailer) finish(emit func(string)) {
	line, ok := t.format(t.pending, t.cut)
	if t.drop {
		ok = false
	}
	t.pending = t.pending[:0]
	t.cut, t.drop = false, false
	if ok {
		emit(line)
	}
}

// format turns raw line bytes into an emitted line. It reports false for lines that must be skipped.
func (t *Tailer) format(line []byte, cut bool) (string, bool) {
	if len(line) > t.opts.MaxLineBytes {
		line = line[:t.opts.MaxLineBytes]
		cut = true
	}
	if cut {
		// Do not leave half of a multi-byte character at the end.
		for range utf8.UTFMax {
			if r, size := utf8.DecodeLastRune(line); r != utf8.RuneError || size != 1 {
				break
			}
			line = line[:len(line)-1]
		}
		return string(line) + truncatedSuffix, true
	}
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(line) == 0 {
		return "", false
	}
	return string(line), true
}
