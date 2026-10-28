package configmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

const (
	original  = "example.com {\n\trespond \"original\"\n}\n"
	candidate = "example.com {\n\trespond \"candidate\"\n}\n"
	another   = "example.com {\n\trespond \"another\"\n}\n"
)

var errBoom = errors.New("boom")

// Both real stores can back a Manager.
var (
	_ RevisionStore = (*store.Memory)(nil)
	_ RevisionStore = (*store.SQLite)(nil)
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

type loadCall struct {
	content string
	// ctxErr is the state of the context the Load received, at the time of the call.
	ctxErr error
}

// fakeCaddy answers Adapt with adaptErr and Load with loadFn, and remembers what it was asked.
type fakeCaddy struct {
	mu       sync.Mutex
	adaptErr error
	// loadFn gets the 1-based number of the call. Nil means every load succeeds.
	loadFn  func(ctx context.Context, n int, content string) error
	adapted []string
	loads   []loadCall
}

func (f *fakeCaddy) Adapt(_ context.Context, content string) (*caddy.AdaptResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adapted = append(f.adapted, content)
	if f.adaptErr != nil {
		return nil, f.adaptErr
	}
	return &caddy.AdaptResult{Config: map[string]any{"adapted": true}}, nil
}

func (f *fakeCaddy) Load(ctx context.Context, content string) error {
	f.mu.Lock()
	f.loads = append(f.loads, loadCall{content: content, ctxErr: ctx.Err()})
	n := len(f.loads)
	f.mu.Unlock()
	if f.loadFn == nil {
		return nil
	}
	return f.loadFn(ctx, n, content)
}

func (f *fakeCaddy) loaded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, l := range f.loads {
		out = append(out, l.content)
	}
	return out
}

// fakeStore is an in-memory RevisionStore that also remembers every status change.
type fakeStore struct {
	mu      sync.Mutex
	revs    []store.Revision
	updates map[int64][]store.RevisionStatus
	addErr  error
}

func (f *fakeStore) AddRevision(_ context.Context, r store.Revision) (store.Revision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addErr != nil {
		return store.Revision{}, f.addErr
	}
	r.Number = int64(len(f.revs) + 1)
	f.revs = append(f.revs, r)
	return r, nil
}

func (f *fakeStore) UpdateRevision(_ context.Context, number int64, status store.RevisionStatus, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if number < 1 || number > int64(len(f.revs)) {
		return store.ErrNotFound
	}
	f.revs[number-1].Status = status
	f.revs[number-1].Error = errMsg
	if f.updates == nil {
		f.updates = map[int64][]store.RevisionStatus{}
	}
	f.updates[number] = append(f.updates[number], status)
	return nil
}

func (f *fakeStore) LatestApplied(context.Context) (store.Revision, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.revs) - 1; i >= 0; i-- {
		if f.revs[i].Status == store.StatusApplied {
			return f.revs[i], true, nil
		}
	}
	return store.Revision{}, false, nil
}

func (f *fakeStore) Revision(_ context.Context, number int64) (store.Revision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if number < 1 || number > int64(len(f.revs)) {
		return store.Revision{}, store.ErrNotFound
	}
	return f.revs[number-1], nil
}

func (f *fakeStore) all() []store.Revision {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.revs)
}

type env struct {
	mgr   *Manager
	caddy *fakeCaddy
	store *fakeStore
	path  string
	now   time.Time
}

// newEnv puts content in a Caddyfile in a temporary directory and returns a Manager over it.
func newEnv(t *testing.T, content string) *env {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &env{caddy: &fakeCaddy{}, store: &fakeStore{}, path: path, now: time.UnixMilli(1_700_000_000_000)}
	e.mgr = New(e.caddy, e.store, Options{Path: path, Now: func() time.Time { return e.now }})
	return e
}

func (e *env) disk(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCurrentReadsTheFileAndItsDigest(t *testing.T) {
	e := newEnv(t, original)
	f, err := e.mgr.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.Content != original || f.SHA256 != sum(original) {
		t.Errorf("Current() = %q, %s; want the file and its digest", f.Content, f.SHA256)
	}
}

func TestCurrentReportsAMissingFile(t *testing.T) {
	e := newEnv(t, original)
	if err := os.Remove(e.path); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.Current(context.Background()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Current() error = %v, want os.ErrNotExist", err)
	}
}

func TestValidate(t *testing.T) {
	rejected := &caddy.AdaptError{Message: "Caddyfile:3: unrecognized directive", Line: 3}
	tests := []struct {
		name        string
		adaptErr    error
		wantInvalid bool
		wantErr     error
	}{
		{name: "accepted"},
		{name: "rejected by caddy", adaptErr: rejected, wantInvalid: true, wantErr: rejected},
		{name: "caddy unreachable", adaptErr: caddy.ErrUnreachable, wantErr: caddy.ErrUnreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, original)
			e.caddy.adaptErr = tt.adaptErr
			res, err := e.mgr.Validate(context.Background(), candidate)
			if tt.wantErr == nil {
				if err != nil || res == nil {
					t.Fatalf("Validate() = %v, %v; want a result", res, err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want the cause %v", err, tt.wantErr)
			}
			if got := errors.Is(err, ErrInvalid); got != tt.wantInvalid {
				t.Errorf("errors.Is(err, ErrInvalid) = %v, want %v", got, tt.wantInvalid)
			}
			if tt.wantInvalid {
				var adaptErr *caddy.AdaptError
				if !errors.As(err, &adaptErr) || adaptErr.Line != 3 {
					t.Errorf("the line of the rejection did not survive: %v", err)
				}
			}
			if got := e.disk(t); got != original {
				t.Errorf("Validate changed the file to %q", got)
			}
		})
	}
}

func TestApplyWritesLoadsAndRecordsTheRevision(t *testing.T) {
	e := newEnv(t, original)
	rev, err := e.mgr.Apply(context.Background(), ApplyRequest{Content: candidate, BaseSHA256: sum(original)})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.disk(t); got != candidate {
		t.Errorf("file = %q, want the candidate", got)
	}
	if got := e.caddy.loaded(); !slices.Equal(got, []string{candidate}) {
		t.Errorf("loads = %q, want only the candidate", got)
	}
	want := store.Revision{
		Number: 1, Time: e.now, Trigger: store.TriggerManualEdit, Status: store.StatusApplied,
		Content: candidate, SHA256: sum(candidate),
	}
	if rev != want {
		t.Errorf("revision = %+v, want %+v", rev, want)
	}
	if got := e.store.all(); len(got) != 1 || got[0] != want {
		t.Errorf("stored revisions = %+v, want [%+v]", got, want)
	}
	if got := e.store.updates[1]; !slices.Equal(got, []store.RevisionStatus{store.StatusLoading, store.StatusApplied}) {
		t.Errorf("status changes = %v, want loading then applied", got)
	}
	// The candidate is validated before it is written.
	if !slices.Equal(e.caddy.adapted, []string{candidate}) {
		t.Errorf("adapted = %q, want the candidate", e.caddy.adapted)
	}
}

func TestApplyKeepsTheTrigger(t *testing.T) {
	e := newEnv(t, original)
	rev, err := e.mgr.Apply(context.Background(), ApplyRequest{Content: candidate, BaseSHA256: sum(original), Trigger: store.TriggerSiteChange})
	if err != nil {
		t.Fatal(err)
	}
	if rev.Trigger != store.TriggerSiteChange {
		t.Errorf("trigger = %q, want %q", rev.Trigger, store.TriggerSiteChange)
	}
}

func TestApplyRefusesAStaleBase(t *testing.T) {
	e := newEnv(t, original)
	// Someone else edits the file after the editor read it.
	if err := os.WriteFile(e.path, []byte(another), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := e.mgr.Apply(context.Background(), ApplyRequest{Content: candidate, BaseSHA256: sum(original)})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Apply() error = %v, want ErrConflict", err)
	}
	if got := e.disk(t); got != another {
		t.Errorf("file = %q, want the other edit kept", got)
	}
	if len(e.caddy.adapted) != 0 || len(e.caddy.loads) != 0 {
		t.Errorf("caddy was called (adapt %d, load %d) for a stale base", len(e.caddy.adapted), len(e.caddy.loads))
	}
	if got := e.store.all(); len(got) != 0 {
		t.Errorf("revisions = %+v, want none for a refused base", got)
	}
}

func TestApplyKeepsARejectedCandidateOutOfTheFile(t *testing.T) {
	rejected := &caddy.AdaptError{Message: "Caddyfile:2: unrecognized directive", Line: 2}
	e := newEnv(t, original)
	e.caddy.adaptErr = rejected

	rev, err := e.mgr.Apply(context.Background(), ApplyRequest{Content: candidate, BaseSHA256: sum(original)})
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, rejected) {
		t.Fatalf("Apply() error = %v, want ErrInvalid wrapping the rejection", err)
	}
	if got := e.disk(t); got != original {
		t.Errorf("file = %q, want the original untouched", got)
	}
	if len(e.caddy.loads) != 0 {
		t.Errorf("caddy loaded %d times, want none", len(e.caddy.loads))
	}
	if rev.Status != store.StatusFailed || rev.Error == "" {
		t.Errorf("revision = %+v, want failed with the reason", rev)
	}
	stored := e.store.all()
	if len(stored) != 1 || stored[0].Status != store.StatusFailed || stored[0].Error == "" {
		t.Errorf("stored revisions = %+v, want one failed with the reason", stored)
	}
}

func TestApplyRestoresThePreviousFileWhenLoadFails(t *testing.T) {
	e := newEnv(t, original)
	e.caddy.loadFn = func(_ context.Context, n int, _ string) error {
		if n == 1 {
			return errBoom
		}
		return nil
	}

	rev, err := e.mgr.Apply(context.Background(), ApplyRequest{Content: candidate, BaseSHA256: sum(original)})
	if !errors.Is(err, ErrApplyFailed) || !errors.Is(err, errBoom) {
		t.Fatalf("Apply() error = %v, want ErrApplyFailed wrapping the cause", err)
	}
	if got := e.disk(t); got != original {
		t.Errorf("file = %q, want the original restored", got)
	}
	if got := e.caddy.loaded(); !slices.Equal(got, []string{candidate, original}) {
		t.Errorf("loads = %q, want the candidate and then the original again", got)
	}
	if rev.Status != store.StatusFailed || rev.Error == "" {
		t.Errorf("revision = %+v, want failed with the reason", rev)
	}
	if got := e.store.updates[1]; !slices.Equal(got, []store.RevisionStatus{store.StatusLoading, store.StatusFailed}) {
		t.Errorf("status changes = %v, want loading then failed", got)
	}
}

func TestApplyNeedsRecoveryWhenThePreviousFileCannotBeReloaded(t *testing.T) {
	e := newEnv(t, original)
	e.caddy.loadFn = func(context.Context, int, string) error { return errBoom }

	rev, err := e.mgr.Apply(context.Background(), ApplyRequest{Content: candidate, BaseSHA256: sum(original)})
	if !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("Apply() error = %v, want ErrRecoveryRequired", err)
	}
	if errors.Is(err, ErrApplyFailed) {
		t.Errorf("error %v claims the previous configuration was restored", err)
	}
	if rev.Status != store.StatusRecoveryRequired || rev.Error == "" {
		t.Errorf("revision = %+v, want recovery-required with the reason", rev)
	}
	// The file is back to the previous content even though Caddy could not reload it.
	if got := e.disk(t); got != original {
		t.Errorf("file = %q, want the original restored on disk", got)
	}
}

func TestApplyNeedsRecoveryWhenThePreviousFileCannotBeWritten(t *testing.T) {
	e := newEnv(t, original)
	e.caddy.loadFn = func(context.Context, int, string) error {
		// Replace the Caddyfile by a directory, so that nothing can be renamed over it.
		if err := os.Remove(e.path); err != nil {
			t.Error(err)
		}
		if err := os.Mkdir(e.path, 0o755); err != nil {
			t.Error(err)
		}
		return errBoom
	}

	rev, err := e.mgr.Apply(context.Background(), ApplyRequest{Content: candidate, BaseSHA256: sum(original)})
	if !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("Apply() error = %v, want ErrRecoveryRequired", err)
	}
	if rev.Status != store.StatusRecoveryRequired {
		t.Errorf("revision status = %q, want recovery-required", rev.Status)
	}
}

func TestApplyRestoresEvenWhenTheCallerGaveUp(t *testing.T) {
	e := newEnv(t, original)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.caddy.loadFn = func(ctx context.Context, n int, _ string) error {
		if n == 1 {
			cancel() // the browser tab was closed while Caddy was loading
			return ctx.Err()
		}
		return nil
	}

	_, err := e.mgr.Apply(ctx, ApplyRequest{Content: candidate, BaseSHA256: sum(original)})
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("Apply() error = %v, want ErrApplyFailed", err)
	}
	if got := e.disk(t); got != original {
		t.Errorf("file = %q, want the original restored", got)
	}
	e.caddy.mu.Lock()
	defer e.caddy.mu.Unlock()
	if len(e.caddy.loads) != 2 || e.caddy.loads[1].ctxErr != nil {
		t.Errorf("loads = %+v, want a second load on a context that is still alive", e.caddy.loads)
	}
}

func TestApplyDoesNothingWhenTheRevisionCannotBeStored(t *testing.T) {
	e := newEnv(t, original)
	e.store.addErr = errBoom

	_, err := e.mgr.Apply(context.Background(), ApplyRequest{Content: candidate, BaseSHA256: sum(original)})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Apply() error = %v, want the store error", err)
	}
	if got := e.disk(t); got != original {
		t.Errorf("file = %q, want the original untouched", got)
	}
	if len(e.caddy.loads) != 0 {
		t.Errorf("caddy loaded %d times, want none", len(e.caddy.loads))
	}
}

func TestApplyRunsOneChangeAtATime(t *testing.T) {
	e := newEnv(t, original)
	contents := []string{candidate, another}
	errs := make([]error, len(contents))
	var wg sync.WaitGroup
	for i, c := range contents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.mgr.Apply(context.Background(), ApplyRequest{Content: c, BaseSHA256: sum(original)})
		}()
	}
	wg.Wait()

	var applied, conflicts int
	for _, err := range errs {
		switch {
		case err == nil:
			applied++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if applied != 1 || conflicts != 1 {
		t.Fatalf("applied %d, conflicts %d; want exactly one of each", applied, conflicts)
	}
	if got := e.disk(t); got != candidate && got != another {
		t.Errorf("file = %q, want one of the two candidates", got)
	}
	if got := e.store.all(); len(got) != 1 {
		t.Errorf("revisions = %d, want only the one that was applied", len(got))
	}
}

func TestRollbackAppliesAnEarlierRevisionAsANewOne(t *testing.T) {
	e := newEnv(t, original)
	ctx := context.Background()
	if _, err := e.mgr.Apply(ctx, ApplyRequest{Content: candidate, BaseSHA256: sum(original)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.Apply(ctx, ApplyRequest{Content: another, BaseSHA256: sum(candidate)}); err != nil {
		t.Fatal(err)
	}

	rev, err := e.mgr.Rollback(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.disk(t); got != candidate {
		t.Errorf("file = %q, want the content of revision 1", got)
	}
	if rev.Number != 3 || rev.Trigger != store.TriggerRollback || rev.Status != store.StatusApplied || rev.Content != candidate {
		t.Errorf("revision = %+v, want a new applied rollback revision 3 with the old content", rev)
	}
	if got := e.store.all(); len(got) != 3 || got[0].Status != store.StatusApplied {
		t.Errorf("history was rewritten: %+v", got)
	}
}

func TestRollbackToAnUnknownRevisionChangesNothing(t *testing.T) {
	e := newEnv(t, original)
	_, err := e.mgr.Rollback(context.Background(), 7)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Rollback() error = %v, want store.ErrNotFound", err)
	}
	if got := e.disk(t); got != original || len(e.caddy.loads) != 0 {
		t.Errorf("file = %q with %d loads, want nothing changed", got, len(e.caddy.loads))
	}
}

func TestRollbackRestoresThePreviousFileWhenLoadFails(t *testing.T) {
	e := newEnv(t, original)
	ctx := context.Background()
	if _, err := e.mgr.Apply(ctx, ApplyRequest{Content: candidate, BaseSHA256: sum(original)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.Apply(ctx, ApplyRequest{Content: another, BaseSHA256: sum(candidate)}); err != nil {
		t.Fatal(err)
	}
	e.caddy.mu.Lock()
	before := len(e.caddy.loads)
	e.caddy.mu.Unlock()
	e.caddy.loadFn = func(_ context.Context, n int, _ string) error {
		if n == before+1 {
			return errBoom
		}
		return nil
	}

	if _, err := e.mgr.Rollback(ctx, 1); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("Rollback() error = %v, want ErrApplyFailed", err)
	}
	if got := e.disk(t); got != another {
		t.Errorf("file = %q, want the content before the rollback restored", got)
	}
}

func TestReconcile(t *testing.T) {
	ctx := context.Background()

	t.Run("no history is not drift", func(t *testing.T) {
		e := newEnv(t, original)
		d, err := e.mgr.Reconcile(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if d.Known || d.Drifted || d.DiskSHA256 != sum(original) {
			t.Errorf("Reconcile() = %+v, want unknown, not drifted, with the digest of the file", d)
		}
	})

	t.Run("matches the newest applied revision", func(t *testing.T) {
		e := newEnv(t, original)
		if _, err := e.mgr.Apply(ctx, ApplyRequest{Content: candidate, BaseSHA256: sum(original)}); err != nil {
			t.Fatal(err)
		}
		d, err := e.mgr.Reconcile(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !d.Known || d.Drifted || d.Revision != 1 {
			t.Errorf("Reconcile() = %+v, want known revision 1, not drifted", d)
		}
	})

	t.Run("an edit outside caddy-view is drift", func(t *testing.T) {
		e := newEnv(t, original)
		if _, err := e.mgr.Apply(ctx, ApplyRequest{Content: candidate, BaseSHA256: sum(original)}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.path, []byte(another), 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := e.mgr.Reconcile(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !d.Known || !d.Drifted || d.Revision != 1 || d.DiskSHA256 != sum(another) {
			t.Errorf("Reconcile() = %+v, want drift from revision 1", d)
		}
	})

	t.Run("a failed revision does not count", func(t *testing.T) {
		e := newEnv(t, original)
		e.caddy.adaptErr = &caddy.AdaptError{Message: "bad", Line: 1}
		if _, err := e.mgr.Apply(ctx, ApplyRequest{Content: candidate, BaseSHA256: sum(original)}); err == nil {
			t.Fatal("expected the candidate to be rejected")
		}
		d, err := e.mgr.Reconcile(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if d.Known || d.Drifted {
			t.Errorf("Reconcile() = %+v, want unknown (nothing was ever applied)", d)
		}
	})

	t.Run("a missing file is an error", func(t *testing.T) {
		e := newEnv(t, original)
		if err := os.Remove(e.path); err != nil {
			t.Fatal(err)
		}
		if _, err := e.mgr.Reconcile(ctx); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Reconcile() error = %v, want os.ErrNotExist", err)
		}
	})
}
