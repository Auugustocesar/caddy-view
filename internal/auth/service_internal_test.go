package auth

import (
	"context"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// An unknown username must cost one password check, like a known one, so that the time of the answer does not say
// which usernames exist.
func TestLoginChecksAPasswordEvenForAnUnknownUser(t *testing.T) {
	now := time.Date(2026, time.November, 6, 12, 0, 0, 0, time.UTC)
	svc := New(store.NewMemory(), Options{Now: func() time.Time { return now }, Params: Params{Time: 1, Memory: 8, Threads: 1}})
	checks := 0
	real := svc.verify
	svc.verify = func(hash, password string) (bool, error) {
		checks++
		return real(hash, password)
	}
	if _, err := svc.Login(context.Background(), "ghost", "whatever", "c"); err == nil {
		t.Fatal("Login of an unknown user succeeded")
	}
	if checks != 1 {
		t.Errorf("password checks for an unknown user = %d, want 1", checks)
	}
}
