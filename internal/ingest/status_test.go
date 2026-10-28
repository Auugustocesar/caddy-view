package ingest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/ingest"
)

func TestStatusBeforeTheFirstPollIsWaiting(t *testing.T) {
	path := logPath(t)
	got := ingest.New(newStore(), ingest.Options{Path: path}).Status()
	if got.State != ingest.StateWaiting || got.Path != path || got.Error != "" {
		t.Errorf("Status() = %+v, want waiting on %s with no error", got, path)
	}
}

func TestStatusFollowsTheFile(t *testing.T) {
	path := logPath(t)
	writeFile(t, path, lines(l(t, "/a"), "this is not an access log line", l(t, "/b")))
	ing := ingest.New(newStore(), ingest.Options{Path: path})
	if err := ing.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := ing.Status()
	if got.State != ingest.StateFollowing || got.Path != path || got.Error != "" {
		t.Fatalf("Status() = %+v, want following %s", got, path)
	}
	if got.Offset != sizeOf(t, path) || got.SkippedLines != 1 || got.Gaps != 0 {
		t.Errorf("Status() = %+v, want offset %d, 1 skipped line and no gaps", got, sizeOf(t, path))
	}
}

func TestStatusOfAFileThatDoesNotExistYetIsWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	ing := ingest.New(newStore(), ingest.Options{Path: path})
	if err := ing.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := ing.Status(); got.State != ingest.StateWaiting || got.Error != "" {
		t.Errorf("Status() = %+v, want waiting with no error", got)
	}
}

func TestStatusCountsAGapAfterATruncation(t *testing.T) {
	path := logPath(t)
	writeFile(t, path, lines(l(t, "/a"), l(t, "/b"), l(t, "/c")))
	ing := ingest.New(newStore(), ingest.Options{Path: path})
	if err := ing.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, lines(l(t, "/d")))
	if err := ing.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := ing.Status(); got.Gaps != 1 || got.Offset != sizeOf(t, path) {
		t.Errorf("Status() = %+v, want 1 gap and the offset of the new file", got)
	}
}

func TestStatusReportsAFailureAndRecoversWithTheStore(t *testing.T) {
	path := logPath(t)
	writeFile(t, path, lines(l(t, "/a")))
	st := newStore()
	st.fail = 1
	ing := ingest.New(st, ingest.Options{Path: path})

	if err := ing.Once(context.Background()); err == nil {
		t.Fatal("expected the first poll to fail")
	}
	if got := ing.Status(); got.State != ingest.StateFailing || got.Error == "" {
		t.Fatalf("Status() = %+v, want failing with the reason", got)
	}
	if err := ing.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := ing.Status(); got.State != ingest.StateFollowing || got.Error != "" {
		t.Errorf("Status() = %+v, want following again with no error", got)
	}
}

func TestStatusOfADirectoryIsFailing(t *testing.T) {
	ing := ingest.New(newStore(), ingest.Options{Path: t.TempDir()})
	if err := ing.Once(context.Background()); err == nil {
		t.Fatal("expected an error for a directory")
	}
	if got := ing.Status(); got.State != ingest.StateFailing || got.Error == "" {
		t.Errorf("Status() = %+v, want failing with the reason", got)
	}
}

func TestStatusCanBeReadWhileRunning(t *testing.T) {
	path := logPath(t)
	writeFile(t, path, lines(l(t, "/a")))
	ing := ingest.New(newStore(), ingest.Options{Path: path, PollInterval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ing.Run(ctx) // Run only returns nil, after the context is cancelled
	}()
	for range 200 {
		_ = ing.Status()
	}
	if err := os.WriteFile(path, []byte(lines(l(t, "/a"), l(t, "/b"))), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 200 {
		_ = ing.Status()
	}
	cancel()
	<-done
}
