package auth

import (
	"sync"
	"time"
)

// maxTrackedClients bounds the table of failures, so that guessing from many addresses cannot grow it without end.
const maxTrackedClients = 10_000

// limiter counts failed logins per key within a sliding window. It lives in memory: it is a brake on guessing, not a
// record, and a restart forgetting it costs an attacker nothing they did not already have.
type limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu       sync.Mutex
	failures map[string][]time.Time // oldest first, at most max per key
}

func newLimiter(max int, window time.Duration, now func() time.Time) *limiter {
	return &limiter{max: max, window: window, now: now, failures: make(map[string][]time.Time)}
}

// live returns the failures of a key that are still inside the window.
func (l *limiter) live(key string, now time.Time) []time.Time {
	times := l.failures[key]
	i := 0
	for i < len(times) && now.Sub(times[i]) >= l.window {
		i++
	}
	if i > 0 {
		times = times[i:]
		if len(times) == 0 {
			delete(l.failures, key)
			return nil
		}
		l.failures[key] = times
	}
	return times
}

// wait returns how long the key must wait before it may try again, or zero when it may try now.
func (l *limiter) wait(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	times := l.live(key, now)
	if len(times) < l.max {
		return 0
	}
	// Only the failures that count are kept, so the oldest of them is the one whose window must end.
	return times[len(times)-l.max].Add(l.window).Sub(now)
}

// fail records a failure of the key.
func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k := range l.failures {
		l.live(k, now) // forgets the keys whose failures have all expired
	}
	times := append(l.live(key, now), now)
	if len(times) > l.max {
		times = times[len(times)-l.max:]
	}
	l.failures[key] = times
	for len(l.failures) > maxTrackedClients {
		l.evictOldest()
	}
}

// evictOldest forgets the key whose latest failure is the oldest.
func (l *limiter) evictOldest() {
	var oldest string
	var at time.Time
	first := true
	for k, times := range l.failures {
		if last := times[len(times)-1]; first || last.Before(at) {
			oldest, at, first = k, last, false
		}
	}
	delete(l.failures, oldest)
}

// clear forgets the failures of a key, after a success.
func (l *limiter) clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// size is the number of keys kept.
func (l *limiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.failures)
}
