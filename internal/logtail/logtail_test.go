package logtail

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

const (
	pollInterval = 5 * time.Millisecond
	waitFor      = 5 * time.Second
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

// start creates the tailer (which fixes the start position synchronously) and runs it in the background.
// Lines arrive on the returned channel; stop cancels the run and returns its result.
func start(t *testing.T, path string, opts Options) (lines <-chan string, stop func() error) {
	t.Helper()
	if opts.PollInterval == 0 {
		opts.PollInterval = pollInterval
	}
	tail, err := New(path, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	out := make(chan string, 1000)
	done := make(chan error, 1)
	go func() { done <- tail.Run(ctx, func(l string) { out <- l }) }()
	return out, func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(waitFor):
			t.Fatal("Run did not return after the context was cancelled")
			return nil
		}
	}
}

// expect reads len(want) lines and compares them, in order.
func expect(t *testing.T, lines <-chan string, want ...string) {
	t.Helper()
	got := make([]string, 0, len(want))
	for range want {
		select {
		case l := <-lines:
			got = append(got, l)
		case <-time.After(waitFor):
			t.Fatalf("timed out after %d of %d lines: got %q, want %q", len(got), len(want), got, want)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

func TestEmitsLinesAppendedAfterTheStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "old\n")
	lines, _ := start(t, path, Options{})

	// "old" was there before the start, so the first line that arrives must be "a".
	appendFile(t, path, "a\nb\n")
	expect(t, lines, "a", "b")
}

func TestBacklogEmitsTheLastCompleteLinesFirst(t *testing.T) {
	tests := []struct {
		name    string
		content string
		backlog int
		want    []string
	}{
		{"fewer lines requested than exist", "l1\nl2\nl3\nl4\nl5\n", 3, []string{"l3", "l4", "l5"}},
		{"more lines requested than exist", "l1\nl2\n", 10, []string{"l1", "l2"}},
		{"exactly all lines", "l1\nl2\n", 2, []string{"l1", "l2"}},
		{"empty lines do not count as lines", "l1\n\nl2\n\n", 2, []string{"l1", "l2"}},
		{"CRLF is trimmed", "l1\r\nl2\r\n", 2, []string{"l1", "l2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "access.log")
			writeFile(t, path, tt.content)
			lines, _ := start(t, path, Options{Backlog: tt.backlog})
			appendFile(t, path, "next\n")
			expect(t, lines, append(tt.want, "next")...)
		})
	}
}

func TestBacklogSkipsAnIncompleteLastLineUntilItIsFinished(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "a\nb\npartial")
	lines, _ := start(t, path, Options{Backlog: 10})
	expect(t, lines, "a", "b")

	appendFile(t, path, " done\n")
	expect(t, lines, "partial done")
}

func TestBacklogOfAnEmptyOrMissingFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.log")
	writeFile(t, empty, "")
	lines, _ := start(t, empty, Options{Backlog: 5})
	appendFile(t, empty, "x\n")
	expect(t, lines, "x")

	missing := filepath.Join(dir, "missing.log")
	lines, _ = start(t, missing, Options{Backlog: 5})
	appendFile(t, missing, "y\n")
	expect(t, lines, "y")
}

func TestHoldsPartialLinesUntilTheNewlineArrives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "")
	lines, _ := start(t, path, Options{})

	appendFile(t, path, "hel")
	appendFile(t, path, "lo\nsecond")
	appendFile(t, path, "\n")
	expect(t, lines, "hello", "second")
}

func TestSkipsEmptyLinesAndTrimsCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "")
	lines, _ := start(t, path, Options{})

	appendFile(t, path, "\n\r\nx\r\n\ny\n")
	expect(t, lines, "x", "y")
}

func TestWaitsForTheFileToBeCreatedAndReadsItFromTheStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "later.log")
	lines, _ := start(t, path, Options{})

	writeFile(t, path, "first\nsecond\n")
	expect(t, lines, "first", "second")
}

func TestFollowsTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "a\nb\n")
	lines, _ := start(t, path, Options{})

	appendFile(t, path, "c\n")
	expect(t, lines, "c")

	// Rewriting the file with less data than was already read means it was truncated.
	writeFile(t, path, "d\n")
	expect(t, lines, "d")
}

func TestFollowsRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "old1\n")
	lines, _ := start(t, path, Options{})

	appendFile(t, path, "old2\n")
	expect(t, lines, "old2")

	// The roller renames the file and creates a new one at the same path.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "new1\nnew2\n")
	expect(t, lines, "new1", "new2")

	// Lines written to the rotated file are not followed, and appends to the new one are.
	appendFile(t, path+".1", "late\n")
	appendFile(t, path, "new3\n")
	expect(t, lines, "new3")
}

func TestCutsLongLinesAndSkipsTheirRemainder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "")
	lines, _ := start(t, path, Options{MaxLineBytes: 16})

	// The long line arrives in two pieces, and the cap is crossed in the second one.
	appendFile(t, path, "0123456789")
	appendFile(t, path, "abcdefghijklmnopqrstuvwxyz\nok\n")
	expect(t, lines, "0123456789abcdef [truncated]", "ok")
}

func TestCutsLongLinesOnAValidUTF8Boundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "")
	lines, _ := start(t, path, Options{MaxLineBytes: 3})

	// "é" takes two bytes, so three bytes would cut the second one in half.
	appendFile(t, path, "éééé\nok\n")
	expect(t, lines, "é [truncated]", "ok")
}

func TestLineExactlyAtTheCapIsNotCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "")
	lines, _ := start(t, path, Options{MaxLineBytes: 5})

	appendFile(t, path, "12345\n123456\n")
	expect(t, lines, "12345", "12345 [truncated]")
}

func TestRunReturnsNilWhenTheContextIsCancelled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "")
	_, stop := start(t, path, Options{})
	if err := stop(); err != nil {
		t.Errorf("Run = %v after cancellation, want nil", err)
	}
}

func TestRunReturnsWhenTheFileNeverAppears(t *testing.T) {
	_, stop := start(t, filepath.Join(t.TempDir(), "never.log"), Options{})
	if err := stop(); err != nil {
		t.Errorf("Run = %v after cancellation, want nil", err)
	}
}

func TestNewRejectsADirectory(t *testing.T) {
	if _, err := New(t.TempDir(), Options{}); err == nil {
		t.Fatal("New(directory) succeeded, want an error")
	}
}
