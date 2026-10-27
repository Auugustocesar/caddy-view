package ingest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/ingest"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

const (
	waitFor      = 5 * time.Second
	pollInterval = 5 * time.Millisecond
)

var errBoom = errors.New("the disk is on fire")

// flakyStore is a memory store that can be made to fail, and that reports every successful write.
type flakyStore struct {
	*store.Memory

	mu      sync.Mutex
	appends int // calls, failed or not
	okFirst int // how many calls succeed before the failures start
	fail    int // how many calls fail once okFirst succeeded
	stored  chan struct{}
}

func newStore() *flakyStore {
	return &flakyStore{Memory: store.NewMemory(), stored: make(chan struct{}, 1000)}
}

func (f *flakyStore) AppendLog(ctx context.Context, b store.LogBatch) error {
	f.mu.Lock()
	f.appends++
	failing := f.appends > f.okFirst && f.fail > 0
	if failing {
		f.fail--
	}
	f.mu.Unlock()
	if failing {
		return errBoom
	}
	err := f.Memory.AppendLog(ctx, b)
	if err == nil {
		f.stored <- struct{}{}
	}
	return err
}

func (f *flakyStore) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.appends
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func logPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "access.log")
}

// lines joins log lines the way a file has them: each one ends with a newline.
func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

func once(t *testing.T, s ingest.Store, opts ingest.Options) {
	t.Helper()
	if err := ingest.New(s, opts).Once(context.Background()); err != nil {
		t.Fatalf("Once: %v", err)
	}
}

// stored returns the paths of the stored entries, oldest first.
func stored(t *testing.T, s interface {
	QueryLogs(context.Context, store.LogQuery) ([]store.LogEntry, error)
}) []string {
	t.Helper()
	got, err := s.QueryLogs(context.Background(), store.LogQuery{Limit: store.MaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(got))
	for i, e := range got {
		out[len(got)-1-i] = e.Path
	}
	return out
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func wantStored(t *testing.T, s *flakyStore, want ...string) {
	t.Helper()
	if got := stored(t, s); !sameList(got, want) {
		t.Errorf("stored paths = %v, want %v", got, want)
	}
}

func cursorOf(t *testing.T, s *flakyStore, path string) store.Cursor {
	t.Helper()
	c, ok, err := s.LoadCursor(context.Background(), path)
	if err != nil || !ok {
		t.Fatalf("LoadCursor = found %v, err %v, want a cursor", ok, err)
	}
	return c
}

func sizeOf(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func l(t *testing.T, path string) string { return caddyLine(t, "app.example.com", path, 200) }

func TestIngestsTheFileFromItsStartAndNeverRepeats(t *testing.T) {
	path, s := logPath(t), newStore()
	writeFile(t, path, lines(l(t, "/a"), l(t, "/b")))

	opts := ingest.Options{Path: path}
	once(t, s, opts)
	wantStored(t, s, "/a", "/b")
	c := cursorOf(t, s, path)
	if c.Offset != sizeOf(t, path) || c.Gaps != 0 || c.SkippedLines != 0 {
		t.Errorf("cursor = %+v, want offset %d with no gaps or skipped lines", c, sizeOf(t, path))
	}

	once(t, s, opts) // nothing new
	wantStored(t, s, "/a", "/b")
}

func TestPicksUpAppendedLines(t *testing.T) {
	path, s := logPath(t), newStore()
	writeFile(t, path, lines(l(t, "/a")))
	in := ingest.New(s, ingest.Options{Path: path})
	if err := in.Once(context.Background()); err != nil {
		t.Fatal(err)
	}

	appendFile(t, path, lines(l(t, "/b"), l(t, "/c")))
	if err := in.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantStored(t, s, "/a", "/b", "/c")
	if c := cursorOf(t, s, path); c.Gaps != 0 {
		t.Errorf("Gaps = %d after a plain append, want 0 (growing is not a rotation)", c.Gaps)
	}
}

func TestWaitsForTheEndOfAPartialLine(t *testing.T) {
	path, s := logPath(t), newStore()
	whole, half := l(t, "/whole"), l(t, "/half")
	cut := len(half) / 2
	writeFile(t, path, whole+"\n"+half[:cut])

	opts := ingest.Options{Path: path}
	once(t, s, opts)
	wantStored(t, s, "/whole")
	if c := cursorOf(t, s, path); c.Offset != int64(len(whole)+1) {
		t.Errorf("Offset = %d, want %d: just after the last complete line", c.Offset, len(whole)+1)
	}

	appendFile(t, path, half[cut:]+"\n")
	once(t, s, opts)
	wantStored(t, s, "/whole", "/half")
}

func TestResumesAfterARestartWithoutRepeating(t *testing.T) {
	path, s := logPath(t), newStore()
	writeFile(t, path, lines(l(t, "/a"), l(t, "/b")))
	once(t, s, ingest.Options{Path: path})

	// A new Ingester is what a restarted process has: only the store remembers.
	appendFile(t, path, lines(l(t, "/c")))
	once(t, s, ingest.Options{Path: path})
	wantStored(t, s, "/a", "/b", "/c")
}

func TestSkipsLinesThatAreNotAccessLogEntriesAndCountsThem(t *testing.T) {
	path, s := logPath(t), newStore()
	writeFile(t, path, lines(l(t, "/a"), "this is not json", `{"msg":"server running"}`, l(t, "/b")))

	once(t, s, ingest.Options{Path: path})
	wantStored(t, s, "/a", "/b")
	c := cursorOf(t, s, path)
	if c.SkippedLines != 2 || c.Offset != sizeOf(t, path) {
		t.Errorf("cursor = %+v, want 2 skipped lines and the offset at the end", c)
	}
}

func TestSkipsLinesThatAreTooLong(t *testing.T) {
	path, s := logPath(t), newStore()
	huge := caddyLine(t, "h", "/"+strings.Repeat("x", 5000), 200)
	writeFile(t, path, lines(l(t, "/a"), huge, l(t, "/b")))

	once(t, s, ingest.Options{Path: path, MaxLineBytes: 1024})
	wantStored(t, s, "/a", "/b")
	if c := cursorOf(t, s, path); c.SkippedLines != 1 {
		t.Errorf("SkippedLines = %d, want 1 for the long line", c.SkippedLines)
	}
}

func TestDropsTheQueryStringUnlessAskedToKeepIt(t *testing.T) {
	for _, tt := range []struct {
		name string
		keep bool
		want string
	}{{"dropped by default", false, "/login"}, {"kept on request", true, "/login?token=secret"}} {
		t.Run(tt.name, func(t *testing.T) {
			path, s := logPath(t), newStore()
			writeFile(t, path, lines(l(t, "/login?token=secret")))
			once(t, s, ingest.Options{Path: path, KeepQuery: tt.keep})
			wantStored(t, s, tt.want)
		})
	}
}

func TestAFileThatDoesNotExistYetIsNotAnError(t *testing.T) {
	path, s := logPath(t), newStore()
	opts := ingest.Options{Path: path}
	once(t, s, opts)
	wantStored(t, s)
	if _, ok, _ := s.LoadCursor(context.Background(), path); ok {
		t.Error("a cursor was saved for a file that does not exist")
	}

	writeFile(t, path, lines(l(t, "/late")))
	once(t, s, opts)
	wantStored(t, s, "/late")
}

func TestADirectoryIsAnError(t *testing.T) {
	err := ingest.New(newStore(), ingest.Options{Path: t.TempDir()}).Once(context.Background())
	if err == nil {
		t.Error("Once on a directory succeeded, want an error")
	}
}

func TestRotationReadsTheNewFileFromItsStartAndRecordsAGap(t *testing.T) {
	path, s := logPath(t), newStore()
	writeFile(t, path, lines(l(t, "/old1"), l(t, "/old2")))
	opts := ingest.Options{Path: path}
	once(t, s, opts)

	// The roller renames the file and Caddy starts a new, larger one at the same path.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, lines(l(t, "/new1"), l(t, "/new2"), l(t, "/new3")))
	once(t, s, opts)

	wantStored(t, s, "/old1", "/old2", "/new1", "/new2", "/new3")
	c := cursorOf(t, s, path)
	if c.Gaps != 1 || c.Offset != sizeOf(t, path) {
		t.Errorf("cursor = %+v, want 1 gap and the offset at the end of the new file", c)
	}
}

func TestTruncationReadsFromTheStartAndRecordsAGap(t *testing.T) {
	path, s := logPath(t), newStore()
	writeFile(t, path, lines(l(t, "/a"), l(t, "/b"), l(t, "/c")))
	opts := ingest.Options{Path: path}
	once(t, s, opts)

	writeFile(t, path, lines(l(t, "/a"))) // the same first line, but the file is now smaller
	once(t, s, opts)

	wantStored(t, s, "/a", "/b", "/c", "/a")
	if c := cursorOf(t, s, path); c.Gaps != 1 || c.Offset != sizeOf(t, path) {
		t.Errorf("cursor = %+v, want 1 gap and the offset at the end", c)
	}
}

func TestAFileReplacedInPlaceIsNoticedByItsContent(t *testing.T) {
	path, s := logPath(t), newStore()
	// Same size and same inode afterwards, so only the content tells the files apart.
	writeFile(t, path, lines(caddyLine(t, "a.example.com", "/old", 200)))
	opts := ingest.Options{Path: path}
	once(t, s, opts)

	writeFile(t, path, lines(caddyLine(t, "b.example.com", "/new", 200)))
	once(t, s, opts)

	wantStored(t, s, "/old", "/new")
	if c := cursorOf(t, s, path); c.Gaps != 1 {
		t.Errorf("Gaps = %d, want 1 for a replaced file", c.Gaps)
	}
}

func TestEntriesAreStoredInBatches(t *testing.T) {
	path, s := logPath(t), newStore()
	writeFile(t, path, lines(l(t, "/1"), l(t, "/2"), l(t, "/3"), l(t, "/4"), l(t, "/5")))

	once(t, s, ingest.Options{Path: path, BatchSize: 2})
	wantStored(t, s, "/1", "/2", "/3", "/4", "/5")
	if got := s.calls(); got != 3 {
		t.Errorf("AppendLog was called %d times, want 3 batches of 2, 2 and 1", got)
	}
}

func TestAFailedBatchLosesNothingAndRepeatsNothing(t *testing.T) {
	path, s := logPath(t), newStore()
	s.okFirst, s.fail = 1, 1 // the first batch is stored, the second one fails
	one, two := l(t, "/1"), l(t, "/2")
	writeFile(t, path, lines(one, two, l(t, "/3"), l(t, "/4"), l(t, "/5")))
	opts := ingest.Options{Path: path, BatchSize: 2}

	err := ingest.New(s, opts).Once(context.Background())
	if !errors.Is(err, errBoom) {
		t.Fatalf("Once: error = %v, want the store's error", err)
	}
	wantStored(t, s, "/1", "/2")
	if c := cursorOf(t, s, path); c.Offset != int64(len(one)+len(two)+2) {
		t.Errorf("Offset = %d, want %d: the end of the stored batch, not further", c.Offset, len(one)+len(two)+2)
	}

	once(t, s, opts) // the store is healthy again
	wantStored(t, s, "/1", "/2", "/3", "/4", "/5")
}

func TestRunIngestsAsTheFileGrowsAndStopsWhenCancelled(t *testing.T) {
	path, s := logPath(t), newStore()
	writeFile(t, path, lines(l(t, "/a")))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- ingest.New(s, ingest.Options{Path: path, PollInterval: pollInterval}).Run(ctx) }()

	waitStored(t, s)
	wantStored(t, s, "/a")
	appendFile(t, path, lines(l(t, "/b")))
	waitStored(t, s)
	wantStored(t, s, "/a", "/b")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after cancellation, want nil", err)
		}
	case <-time.After(waitFor):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

func TestRunRetriesAFailingStore(t *testing.T) {
	path, s := logPath(t), newStore()
	s.fail = 3
	writeFile(t, path, lines(l(t, "/a")))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go ingest.New(s, ingest.Options{Path: path, PollInterval: pollInterval}).Run(ctx)

	waitStored(t, s)
	wantStored(t, s, "/a") // stored once, after the failures
}

func TestRunWithACancelledContextReturnsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ingest.New(newStore(), ingest.Options{Path: logPath(t)}).Run(ctx); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// waitStored blocks until the store reports one more successful write.
func waitStored(t *testing.T, s *flakyStore) {
	t.Helper()
	select {
	case <-s.stored:
	case <-time.After(waitFor):
		t.Fatal("timed out waiting for a write to the store")
	}
}
