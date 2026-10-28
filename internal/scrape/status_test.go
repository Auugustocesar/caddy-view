package scrape_test

import (
	"context"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/scrape"
)

func TestStatusBeforeTheFirstScrapeIsPending(t *testing.T) {
	c := &clock{now: t0}
	got := newScraper(&fakeSource{}, newStore(), c).Status()
	if got.State != scrape.StatePending || !got.LastSuccess.IsZero() || got.Error != "" {
		t.Errorf("Status() = %+v, want pending, never succeeded", got)
	}
}

func TestStatusAfterAScrapeIsOK(t *testing.T) {
	c := &clock{now: t0}
	src := &fakeSource{body: body(line(inFlight, 3, `server="srv0"`))}
	s := newScraper(src, newStore(), c)
	mustOnce(t, s)
	got := s.Status()
	if got.State != scrape.StateOK || !got.LastSuccess.Equal(t0) || got.Error != "" {
		t.Errorf("Status() = %+v, want ok at %v", got, t0)
	}
}

func TestStatusSaysWhenCaddyHasNoMetrics(t *testing.T) {
	c := &clock{now: t0}
	src := &fakeSource{err: &caddy.APIError{Status: 404, Message: "not found"}}
	s := newScraper(src, newStore(), c)
	if err := s.Once(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if got := s.Status(); got.State != scrape.StateDisabled || got.Error == "" {
		t.Errorf("Status() = %+v, want disabled with the reason", got)
	}
}

func TestStatusKeepsTheLastSuccessThroughAFailureAndRecovers(t *testing.T) {
	c := &clock{now: t0}
	src := &fakeSource{body: body(line(inFlight, 3, `server="srv0"`))}
	s := newScraper(src, newStore(), c)
	mustOnce(t, s)

	c.advance(time.Minute)
	src.set("", errBoom)
	if err := s.Once(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	got := s.Status()
	if got.State != scrape.StateFailing || got.Error == "" || !got.LastSuccess.Equal(t0) {
		t.Fatalf("Status() = %+v, want failing, with the reason and the success of %v kept", got, t0)
	}

	c.advance(time.Minute)
	src.set(body(line(inFlight, 4, `server="srv0"`)), nil)
	mustOnce(t, s)
	got = s.Status()
	if got.State != scrape.StateOK || got.Error != "" || !got.LastSuccess.Equal(c.now) {
		t.Errorf("Status() = %+v, want ok again at %v", got, c.now)
	}
}

func TestStatusCanBeReadWhileRunning(t *testing.T) {
	c := &clock{now: t0}
	src := &fakeSource{body: body(line(inFlight, 3, `server="srv0"`))}
	s := newScraper(src, newStore(), c)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Run(ctx) // Run only returns nil, after the context is cancelled
	}()
	for range 500 {
		_ = s.Status()
	}
	cancel()
	<-done
}
