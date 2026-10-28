package cli

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// serve logs from several goroutines and also prints its own errors, all to the stderr it was given, which in tests
// is a plain buffer. The writer it wraps that in must make concurrent writes safe and keep each one whole.
func TestSyncWriterKeepsConcurrentWritesWhole(t *testing.T) {
	var buf bytes.Buffer
	w := &syncWriter{w: &buf}

	const writers, lines = 8, 200
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range lines {
				if _, err := w.Write([]byte(strings.Repeat(string(rune('a'+i)), 40) + "\n")); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()

	got := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(got) != writers*lines {
		t.Fatalf("got %d lines, want %d", len(got), writers*lines)
	}
	for _, line := range got {
		if len(line) != 40 || strings.Trim(line, line[:1]) != "" {
			t.Fatalf("a write was torn: %q", line)
		}
	}
}
