package configmgr

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// seed stores a revision of content with the given status, as a crashed apply would have left it.
func (e *env) seed(t *testing.T, content string, status store.RevisionStatus) store.Revision {
	t.Helper()
	rev, err := e.store.AddRevision(context.Background(), store.Revision{
		Time: e.now, Trigger: store.TriggerManualEdit, Status: status, Content: content, SHA256: sum(content),
	})
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func (e *env) status(t *testing.T, number int64) store.Revision {
	t.Helper()
	rev, err := e.store.Revision(context.Background(), number)
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

// ---- B3: what Caddy runs

func TestApplyFailsWhenCaddyAcceptsAConfigurationButDoesNotRunIt(t *testing.T) {
	e := newEnv(t, original)
	e.caddy.candidateConfig = map[string]any{"apps": map[string]any{"http": "the candidate"}}
	e.caddy.loadIgnored = true

	rev, err := e.mgr.Apply(context.Background(), ApplyRequest{Content: candidate, BaseSHA256: sum(original)})
	if !errors.Is(err, ErrApplyFailed) || !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Apply() error = %v, want ErrApplyFailed wrapping ErrNotRunning", err)
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
}

func TestApplyFailsWhenTheRunningConfigurationCannotBeReadBack(t *testing.T) {
	e := newEnv(t, original)
	e.caddy.loadFn = func(_ context.Context, n int, _ string) error {
		if n == 1 {
			e.caddy.mu.Lock()
			e.caddy.runningErr = errBoom // Caddy stops answering right after it accepted the load
			e.caddy.mu.Unlock()
		}
		return nil
	}

	rev, err := e.mgr.Apply(context.Background(), ApplyRequest{Content: candidate, BaseSHA256: sum(original)})
	if !errors.Is(err, ErrNotRunning) || !errors.Is(err, errBoom) {
		t.Fatalf("Apply() error = %v, want ErrNotRunning wrapping the cause", err)
	}
	// Restoring needs Caddy too, and it does not answer: that is the case that needs a person.
	if !errors.Is(err, ErrRecoveryRequired) && !errors.Is(err, ErrApplyFailed) {
		t.Errorf("Apply() error = %v, want it to say what state things are in", err)
	}
	if rev.Status == store.StatusApplied {
		t.Errorf("revision status = %q, want it not applied", rev.Status)
	}
}

func TestReconcileSaysWhetherCaddyRunsTheFile(t *testing.T) {
	ctx := context.Background()
	running := map[string]any{"apps": map[string]any{"http": "running"}}
	other := map[string]any{"apps": map[string]any{"http": "something else"}}

	t.Run("it does", func(t *testing.T) {
		e := newEnv(t, original)
		e.caddy.running, e.caddy.candidateConfig = running, map[string]any{"apps": map[string]any{"http": "running"}}
		if d, err := e.mgr.Reconcile(ctx); err != nil || d.Running != RunMatches {
			t.Errorf("Reconcile() = %+v, %v, want Running = RunMatches", d, err)
		}
	})
	t.Run("it does not", func(t *testing.T) {
		e := newEnv(t, original)
		e.caddy.running, e.caddy.candidateConfig = other, running
		if d, err := e.mgr.Reconcile(ctx); err != nil || d.Running != RunDiffers {
			t.Errorf("Reconcile() = %+v, %v, want Running = RunDiffers", d, err)
		}
	})
	t.Run("caddy cannot be reached", func(t *testing.T) {
		e := newEnv(t, original)
		e.caddy.runningErr = caddy.ErrUnreachable
		if d, err := e.mgr.Reconcile(ctx); err != nil || d.Running != RunUnknown {
			t.Errorf("Reconcile() = %+v, %v, want Running = RunUnknown and no error", d, err)
		}
	})
	t.Run("caddy cannot adapt the file", func(t *testing.T) {
		e := newEnv(t, original)
		e.caddy.adaptErr = &caddy.AdaptError{Message: "bad", Line: 1}
		if d, err := e.mgr.Reconcile(ctx); err != nil || d.Running != RunUnknown {
			t.Errorf("Reconcile() = %+v, %v, want Running = RunUnknown and no error", d, err)
		}
	})
	t.Run("a relative import makes the comparison meaningless", func(t *testing.T) {
		e := newEnv(t, relativeImport)
		if d, err := e.mgr.Reconcile(ctx); err != nil || d.Running != RunUnknown {
			t.Errorf("Reconcile() = %+v, %v, want Running = RunUnknown", d, err)
		}
		if len(e.caddy.adapted) != 0 {
			t.Errorf("caddy adapted %q: text with a relative import must not be adapted from here", e.caddy.adapted)
		}
	})
}

// ---- B2: revisions a crash left unfinished

func TestRecoverResolvesWhatACrashLeftUnfinished(t *testing.T) {
	ctx := context.Background()
	runningConfig := map[string]any{"apps": map[string]any{"http": "candidate"}}
	same := map[string]any{"apps": map[string]any{"http": "candidate"}}
	other := map[string]any{"apps": map[string]any{"http": "old"}}

	tests := []struct {
		name       string
		status     store.RevisionStatus
		onDisk     string // what the Caddyfile holds after the crash
		setup      func(e *env)
		wantStatus store.RevisionStatus
	}{
		{"pending never touched the disk", store.StatusPending, original, nil, store.StatusFailed},
		{"loading, the file is not the candidate", store.StatusLoading, original, nil, store.StatusFailed},
		{
			"loading, the file is the candidate and Caddy runs it", store.StatusLoading, candidate,
			func(e *env) { e.caddy.running, e.caddy.candidateConfig = runningConfig, same }, store.StatusApplied,
		},
		{
			"loading, the file is the candidate and Caddy runs something else", store.StatusLoading, candidate,
			func(e *env) { e.caddy.running, e.caddy.candidateConfig = other, same }, store.StatusRecoveryRequired,
		},
		{
			"loading, the file is the candidate and Caddy cannot be reached", store.StatusLoading, candidate,
			func(e *env) { e.caddy.runningErr = caddy.ErrUnreachable }, store.StatusRecoveryRequired,
		},
		{
			"loading, the file is the candidate and Caddy cannot adapt it", store.StatusLoading, candidate,
			func(e *env) { e.caddy.adaptErr = &caddy.AdaptError{Message: "bad", Line: 1} }, store.StatusRecoveryRequired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.onDisk)
			if tt.setup != nil {
				tt.setup(e)
			}
			rev := e.seed(t, candidate, tt.status)

			got, err := e.mgr.Recover(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Revision != rev.Number || got[0].Status != tt.wantStatus || got[0].Reason == "" {
				t.Fatalf("Recover() = %+v, want revision %d resolved as %s with a reason", got, rev.Number, tt.wantStatus)
			}
			stored := e.status(t, rev.Number)
			if stored.Status != tt.wantStatus {
				t.Errorf("stored status = %q, want %q", stored.Status, tt.wantStatus)
			}
			if tt.wantStatus == store.StatusApplied && stored.Error != "" {
				t.Errorf("an applied revision keeps the error %q", stored.Error)
			}
			if tt.wantStatus != store.StatusApplied && stored.Error == "" {
				t.Errorf("a %s revision has no reason stored", tt.wantStatus)
			}
			if len(e.caddy.loads) != 0 {
				t.Errorf("Recover loaded %d configurations: it must only record what is true", len(e.caddy.loads))
			}
			if got := e.disk(t); got != tt.onDisk {
				t.Errorf("Recover changed the file to %q", got)
			}
		})
	}
}

func TestRecoverWithAMissingFileNeedsARecovery(t *testing.T) {
	e := newEnv(t, original)
	rev := e.seed(t, candidate, store.StatusLoading)
	if err := os.Remove(e.path); err != nil {
		t.Fatal(err)
	}
	got, err := e.mgr.Recover(context.Background())
	if err != nil || len(got) != 1 || got[0].Status != store.StatusRecoveryRequired {
		t.Fatalf("Recover() = %+v, %v, want the revision to need a recovery", got, err)
	}
	if e.status(t, rev.Number).Status != store.StatusRecoveryRequired {
		t.Errorf("stored status = %q, want recovery-required", e.status(t, rev.Number).Status)
	}
}

func TestRecoverResolvesEveryUnfinishedRevisionAndNothingElse(t *testing.T) {
	e := newEnv(t, original)
	pending := e.seed(t, candidate, store.StatusPending)
	applied := e.seed(t, original, store.StatusApplied)
	loading := e.seed(t, another, store.StatusLoading)
	failed := e.seed(t, candidate, store.StatusFailed)

	got, err := e.mgr.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Revision != pending.Number || got[1].Revision != loading.Number {
		t.Fatalf("Recover() = %+v, want revisions %d and %d, oldest first", got, pending.Number, loading.Number)
	}
	for _, untouched := range []struct {
		rev  store.Revision
		want store.RevisionStatus
	}{{applied, store.StatusApplied}, {failed, store.StatusFailed}} {
		if s := e.status(t, untouched.rev.Number).Status; s != untouched.want {
			t.Errorf("revision %d is %q after Recover, want %q untouched", untouched.rev.Number, s, untouched.want)
		}
	}
	// Run again: nothing is left to resolve.
	if again, err := e.mgr.Recover(context.Background()); err != nil || len(again) != 0 {
		t.Errorf("a second Recover() = %+v, %v, want nothing", again, err)
	}
}

func TestRecoverWithNothingUnfinishedDoesNothing(t *testing.T) {
	e := newEnv(t, original)
	e.seed(t, original, store.StatusApplied)
	got, err := e.mgr.Recover(context.Background())
	if err != nil || len(got) != 0 {
		t.Errorf("Recover() = %+v, %v, want nothing", got, err)
	}
	if len(e.caddy.adapted) != 0 || len(e.caddy.loads) != 0 {
		t.Errorf("caddy was called (adapt %d, load %d) with nothing to resolve", len(e.caddy.adapted), len(e.caddy.loads))
	}
}

func TestRecoverReportsAStoreFailure(t *testing.T) {
	e := newEnv(t, original)
	e.store.unfinishedErr = errBoom
	if _, err := e.mgr.Recover(context.Background()); !errors.Is(err, errBoom) {
		t.Errorf("Recover() error = %v, want the store error", err)
	}
}
