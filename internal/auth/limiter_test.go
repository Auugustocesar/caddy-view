package auth

import (
	"testing"
	"time"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestLimiter(max int, window time.Duration) (*limiter, *clock) {
	c := &clock{now: time.Date(2026, time.November, 6, 12, 0, 0, 0, time.UTC)}
	return newLimiter(max, window, c.Now), c
}

func TestLimiterAllowsUpToTheMaximumFailures(t *testing.T) {
	l, _ := newTestLimiter(3, time.Minute)
	for i := range 3 {
		if wait := l.wait("a"); wait != 0 {
			t.Fatalf("before failure %d: wait = %v, want 0", i+1, wait)
		}
		l.fail("a")
	}
	if wait := l.wait("a"); wait <= 0 {
		t.Errorf("after 3 failures: wait = %v, want it to be positive", wait)
	}
}

func TestLimiterCountsEachKeyOnItsOwn(t *testing.T) {
	l, _ := newTestLimiter(1, time.Minute)
	l.fail("a")
	if wait := l.wait("b"); wait != 0 {
		t.Errorf("wait for another key = %v, want 0", wait)
	}
}

func TestLimiterWaitIsWhatIsLeftOfTheWindow(t *testing.T) {
	l, c := newTestLimiter(2, 10*time.Minute)
	l.fail("a")
	c.advance(4 * time.Minute)
	l.fail("a")
	// The window of the oldest failure ends in 6 minutes, and then only one failure counts.
	if wait := l.wait("a"); wait != 6*time.Minute {
		t.Errorf("wait = %v, want 6m", wait)
	}
	c.advance(6 * time.Minute)
	if wait := l.wait("a"); wait != 0 {
		t.Errorf("wait after the oldest failure left the window = %v, want 0", wait)
	}
}

func TestLimiterClearsAKeyOnSuccess(t *testing.T) {
	l, _ := newTestLimiter(2, time.Minute)
	l.fail("a")
	l.clear("a")
	l.fail("a")
	if wait := l.wait("a"); wait != 0 {
		t.Errorf("wait = %v, want 0: the first failure was cleared", wait)
	}
}

func TestLimiterForgetsKeysWhoseFailuresAreOld(t *testing.T) {
	l, c := newTestLimiter(5, time.Minute)
	for _, k := range []string{"a", "b", "c"} {
		l.fail(k)
	}
	c.advance(2 * time.Minute)
	l.fail("d") // recording a failure sweeps what has expired
	if got := l.size(); got != 1 {
		t.Errorf("keys kept = %d, want 1: an attacker must not be able to grow the table without end", got)
	}
}

func TestLimiterBoundsItsTableWhenEveryKeyIsRecent(t *testing.T) {
	l, _ := newTestLimiter(5, time.Hour)
	for i := range maxTrackedClients + 100 {
		l.fail(string(rune('a'+i%26)) + time.Duration(i).String())
	}
	if got := l.size(); got > maxTrackedClients {
		t.Errorf("keys kept = %d, want at most %d", got, maxTrackedClients)
	}
}
