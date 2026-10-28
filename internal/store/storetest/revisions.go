package storetest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// RevisionStore is the part of a store the revision contract checks.
type RevisionStore interface {
	AddRevision(ctx context.Context, r store.Revision) (store.Revision, error)
	UpdateRevision(ctx context.Context, number int64, status store.RevisionStatus, errMsg string) error
	LatestApplied(ctx context.Context) (store.Revision, bool, error)
	Revision(ctx context.Context, number int64) (store.Revision, error)
	Revisions(ctx context.Context, q store.RevisionQuery) ([]store.Revision, error)
	Unfinished(ctx context.Context) ([]store.Revision, error)
	Close() error
}

// digest is a well-formed SHA-256 for the tests; the store checks the shape, not the match with the content.
var digest = strings.Repeat("ab", 32)

func revision(content string, at time.Time) store.Revision {
	return store.Revision{
		Time: at, Trigger: store.TriggerManualEdit, Status: store.StatusPending, Content: content, SHA256: digest,
	}
}

func sameRevision(a, b store.Revision) bool {
	return a.Number == b.Number && a.Time.Equal(b.Time) && a.Trigger == b.Trigger && a.Status == b.Status &&
		a.Content == b.Content && a.SHA256 == b.SHA256 && a.Error == b.Error
}

func mustAddRevision(t *testing.T, s RevisionStore, r store.Revision) store.Revision {
	t.Helper()
	got, err := s.AddRevision(context.Background(), r)
	if err != nil {
		t.Fatalf("AddRevision: %v", err)
	}
	return got
}

func numbers(revs []store.Revision) []int64 {
	out := make([]int64, len(revs))
	for i, r := range revs {
		out[i] = r.Number
	}
	return out
}

func sameNumbers(a, b []int64) bool {
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

// RunRevisions checks the revision part of the contract against stores made by newStore. Every subtest gets a fresh
// store.
func RunRevisions(t *testing.T, newStore func(t *testing.T) RevisionStore) {
	t.Helper()
	fresh := func(t *testing.T) RevisionStore {
		t.Helper()
		s := newStore(t)
		t.Cleanup(func() { s.Close() })
		return s
	}
	ctx := context.Background()

	t.Run("AddRevision assigns increasing numbers and ignores the one given", func(t *testing.T) {
		s := fresh(t)
		var got []int64
		for i := range 3 {
			r := revision("a", base.Add(time.Duration(i)*time.Second))
			r.Number = 99
			got = append(got, mustAddRevision(t, s, r).Number)
		}
		if !sameNumbers(got, []int64{1, 2, 3}) {
			t.Errorf("numbers = %v, want [1 2 3]", got)
		}
	})

	t.Run("a revision reads back as it was stored", func(t *testing.T) {
		s := fresh(t)
		r := revision("example.com {\n\trespond \"hi\"\n}\n", base)
		r.Trigger = store.TriggerRollback
		added := mustAddRevision(t, s, r)
		want := r
		want.Number = 1
		if !sameRevision(added, want) {
			t.Errorf("AddRevision returned %+v, want %+v", added, want)
		}
		got, err := s.Revision(ctx, 1)
		if err != nil || !sameRevision(got, want) {
			t.Errorf("Revision(1) = %+v, %v, want %+v", got, err, want)
		}
	})

	t.Run("an empty Caddyfile is a valid revision", func(t *testing.T) {
		s := fresh(t)
		if _, err := s.AddRevision(ctx, revision("", base)); err != nil {
			t.Errorf("AddRevision of an empty content: %v", err)
		}
	})

	t.Run("a malformed revision is refused and takes no number", func(t *testing.T) {
		s := fresh(t)
		tests := []struct {
			name   string
			mutate func(*store.Revision)
		}{
			{"unknown trigger", func(r *store.Revision) { r.Trigger = "magic" }},
			{"no trigger", func(r *store.Revision) { r.Trigger = "" }},
			{"unknown status", func(r *store.Revision) { r.Status = "done" }},
			{"no status", func(r *store.Revision) { r.Status = "" }},
			{"no digest", func(r *store.Revision) { r.SHA256 = "" }},
			{"digest of the wrong shape", func(r *store.Revision) { r.SHA256 = "abc" }},
			{"digest with upper case", func(r *store.Revision) { r.SHA256 = strings.ToUpper(digest) }},
			{"no time", func(r *store.Revision) { r.Time = time.Time{} }},
		}
		for _, tt := range tests {
			r := revision("a", base)
			tt.mutate(&r)
			if _, err := s.AddRevision(ctx, r); !errors.Is(err, store.ErrInvalidRevision) {
				t.Errorf("%s: AddRevision error = %v, want ErrInvalidRevision", tt.name, err)
			}
		}
		if got := mustAddRevision(t, s, revision("a", base)); got.Number != 1 {
			t.Errorf("number after refused revisions = %d, want 1", got.Number)
		}
	})

	t.Run("Revision of an unknown number is ErrNotFound", func(t *testing.T) {
		s := fresh(t)
		mustAddRevision(t, s, revision("a", base))
		for _, n := range []int64{-1, 0, 2} {
			if _, err := s.Revision(ctx, n); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("Revision(%d) error = %v, want ErrNotFound", n, err)
			}
		}
	})

	t.Run("UpdateRevision changes the status and the error and nothing else", func(t *testing.T) {
		s := fresh(t)
		added := mustAddRevision(t, s, revision("a", base))
		if err := s.UpdateRevision(ctx, added.Number, store.StatusFailed, "boom"); err != nil {
			t.Fatal(err)
		}
		want := added
		want.Status, want.Error = store.StatusFailed, "boom"
		if got, err := s.Revision(ctx, added.Number); err != nil || !sameRevision(got, want) {
			t.Errorf("Revision = %+v, %v, want %+v", got, err, want)
		}
		// A later success clears the error.
		if err := s.UpdateRevision(ctx, added.Number, store.StatusApplied, ""); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Revision(ctx, added.Number); got.Status != store.StatusApplied || got.Error != "" {
			t.Errorf("Revision = %+v, want applied with no error", got)
		}
	})

	t.Run("UpdateRevision refuses an unknown revision and an unknown status", func(t *testing.T) {
		s := fresh(t)
		added := mustAddRevision(t, s, revision("a", base))
		if err := s.UpdateRevision(ctx, 9, store.StatusApplied, ""); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("unknown revision: error = %v, want ErrNotFound", err)
		}
		if err := s.UpdateRevision(ctx, added.Number, "done", ""); !errors.Is(err, store.ErrInvalidRevision) {
			t.Errorf("unknown status: error = %v, want ErrInvalidRevision", err)
		}
		if got, _ := s.Revision(ctx, added.Number); got.Status != store.StatusPending {
			t.Errorf("status after refused updates = %q, want pending", got.Status)
		}
	})

	t.Run("LatestApplied is the highest numbered applied revision", func(t *testing.T) {
		s := fresh(t)
		if _, ok, err := s.LatestApplied(ctx); err != nil || ok {
			t.Fatalf("LatestApplied on an empty store = %v, %v, want none", ok, err)
		}
		for i, status := range []store.RevisionStatus{store.StatusApplied, store.StatusApplied, store.StatusFailed, store.StatusPending} {
			added := mustAddRevision(t, s, revision("a", base.Add(time.Duration(i)*time.Second)))
			if err := s.UpdateRevision(ctx, added.Number, status, ""); err != nil {
				t.Fatal(err)
			}
		}
		got, ok, err := s.LatestApplied(ctx)
		if err != nil || !ok || got.Number != 2 {
			t.Errorf("LatestApplied = %+v, %v, %v, want revision 2", got, ok, err)
		}
	})

	t.Run("Revisions lists newest first and pages backwards", func(t *testing.T) {
		s := fresh(t)
		for i := range 5 {
			mustAddRevision(t, s, revision("a", base.Add(time.Duration(i)*time.Second)))
		}
		list := func(q store.RevisionQuery) []int64 {
			t.Helper()
			got, err := s.Revisions(ctx, q)
			if err != nil {
				t.Fatalf("Revisions(%+v): %v", q, err)
			}
			return numbers(got)
		}
		if got := list(store.RevisionQuery{}); !sameNumbers(got, []int64{5, 4, 3, 2, 1}) {
			t.Errorf("all = %v, want [5 4 3 2 1]", got)
		}
		if got := list(store.RevisionQuery{Limit: 2}); !sameNumbers(got, []int64{5, 4}) {
			t.Errorf("first page = %v, want [5 4]", got)
		}
		if got := list(store.RevisionQuery{Limit: 2, Before: 4}); !sameNumbers(got, []int64{3, 2}) {
			t.Errorf("second page = %v, want [3 2]", got)
		}
		if got := list(store.RevisionQuery{Limit: 2, Before: 2}); !sameNumbers(got, []int64{1}) {
			t.Errorf("last page = %v, want [1]", got)
		}
		if got := list(store.RevisionQuery{Before: 1}); len(got) != 0 {
			t.Errorf("before the first = %v, want none", got)
		}
	})

	t.Run("Revisions keeps the content and caps the limit", func(t *testing.T) {
		s := fresh(t)
		mustAddRevision(t, s, revision("first", base))
		got, err := s.Revisions(ctx, store.RevisionQuery{Limit: store.MaxRevisionLimit + 1})
		if err != nil || len(got) != 1 || got[0].Content != "first" {
			t.Errorf("Revisions = %+v, %v, want the one revision with its content", got, err)
		}
		if _, err := s.Revisions(ctx, store.RevisionQuery{Limit: -1}); !errors.Is(err, store.ErrInvalidQuery) {
			t.Errorf("negative limit: error = %v, want ErrInvalidQuery", err)
		}
	})

	t.Run("a returned revision is a copy", func(t *testing.T) {
		s := fresh(t)
		mustAddRevision(t, s, revision("a", base))
		got, _ := s.Revisions(ctx, store.RevisionQuery{})
		got[0].Content = "changed"
		if again, _ := s.Revision(ctx, 1); again.Content != "a" {
			t.Errorf("the stored content became %q", again.Content)
		}
	})

	t.Run("concurrent AddRevision never repeats a number", func(t *testing.T) {
		s := fresh(t)
		const n = 20
		got := make([]int64, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := s.AddRevision(ctx, revision("a", base.Add(time.Duration(i)*time.Second)))
				if err != nil {
					t.Errorf("AddRevision: %v", err)
				}
				got[i] = r.Number
			}()
		}
		wg.Wait()
		seen := map[int64]bool{}
		for _, num := range got {
			if num < 1 || num > n || seen[num] {
				t.Fatalf("numbers = %v, want 1..%d each once", got, n)
			}
			seen[num] = true
		}
	})

	t.Run("Unfinished lists the pending and loading revisions, oldest first", func(t *testing.T) {
		s := fresh(t)
		if got, err := s.Unfinished(ctx); err != nil || len(got) != 0 {
			t.Fatalf("Unfinished on an empty store = %v, %v, want none", numbers(got), err)
		}
		statuses := []store.RevisionStatus{
			store.StatusPending, store.StatusApplied, store.StatusLoading, store.StatusFailed, store.StatusRecoveryRequired, store.StatusLoading,
		}
		for i, status := range statuses {
			added := mustAddRevision(t, s, revision("content", base.Add(time.Duration(i)*time.Second)))
			if err := s.UpdateRevision(ctx, added.Number, status, ""); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.Unfinished(ctx)
		if err != nil || !sameNumbers(numbers(got), []int64{1, 3, 6}) {
			t.Fatalf("Unfinished = %v, %v, want [1 3 6]", numbers(got), err)
		}
		if got[0].Content != "content" || got[0].Status != store.StatusPending {
			t.Errorf("Unfinished[0] = %+v, want the whole revision", got[0])
		}
		if err := s.UpdateRevision(ctx, 3, store.StatusFailed, "boom"); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Unfinished(ctx); !sameNumbers(numbers(got), []int64{1, 6}) {
			t.Errorf("Unfinished after resolving 3 = %v, want [1 6]", numbers(got))
		}
	})

	t.Run("a closed store refuses everything", func(t *testing.T) {
		s := newStore(t)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddRevision(ctx, revision("a", base)); !errors.Is(err, store.ErrClosed) {
			t.Errorf("AddRevision error = %v, want ErrClosed", err)
		}
		if _, err := s.Revisions(ctx, store.RevisionQuery{}); !errors.Is(err, store.ErrClosed) {
			t.Errorf("Revisions error = %v, want ErrClosed", err)
		}
		if _, err := s.Unfinished(ctx); !errors.Is(err, store.ErrClosed) {
			t.Errorf("Unfinished error = %v, want ErrClosed", err)
		}
	})
}
