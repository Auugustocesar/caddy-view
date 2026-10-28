package scrape

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
)

// State is how the scraper is doing.
type State string

// The states of a scraper.
const (
	// StatePending means no scrape has finished yet.
	StatePending State = "pending"
	// StateOK means the last scrape worked.
	StateOK State = "ok"
	// StateDisabled means Caddy answered that it has no metrics endpoint: `metrics` is not enabled.
	StateDisabled State = "disabled"
	// StateFailing means the last scrape failed for another reason; Status.Error says why.
	StateFailing State = "failing"
)

// Status is what a scraper reports about itself.
type Status struct {
	State State
	// LastSuccess is when a scrape last worked; the zero time when none has.
	LastSuccess time.Time
	// Error is why the last scrape failed; empty when it worked.
	Error string
}

// Status reports the outcome of the last scrape. It is safe to call while Run is going.
func (s *Scraper) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Once scrapes Caddy one time and stores the selected series. Nothing is stored when the scrape fails. The outcome is
// what Status reports afterwards.
func (s *Scraper) Once(ctx context.Context) error {
	err := s.scrape(ctx)
	s.record(ctx, err)
	return err
}

// record keeps the outcome of a scrape for Status. A scrape cut short by the end of the program says nothing.
func (s *Scraper) record(ctx context.Context, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var api *caddy.APIError
	switch {
	case err != nil && ctx.Err() != nil:
	case err == nil:
		s.status = Status{State: StateOK, LastSuccess: s.opts.Now()}
	case errors.As(err, &api) && api.Status == http.StatusNotFound:
		s.status = Status{State: StateDisabled, LastSuccess: s.status.LastSuccess, Error: err.Error()}
	default:
		s.status = Status{State: StateFailing, LastSuccess: s.status.LastSuccess, Error: err.Error()}
	}
}
