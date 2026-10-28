package configmgr

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/Auugustocesar/caddy-view/internal/sites"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// editMain applies a new Caddyfile text, keeping its import of the managed file.
func (m *menv) editMain(t *testing.T, note string) store.Revision {
	t.Helper()
	cur, err := m.mgr.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rev, err := m.mgr.Apply(context.Background(), ApplyRequest{Content: m.main + note, BaseSHA256: cur.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func (m *menv) applySites(t *testing.T, all ...store.Site) store.Revision {
	t.Helper()
	rev, err := m.mgr.ApplySites(context.Background(), all)
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func TestApplyOfTheCaddyfileCarriesTheManagedStateAlong(t *testing.T) {
	m := newManagedEnv(t)
	// Nothing was ever managed: the revision does not mention it.
	if rev := m.editMain(t, "# first\n"); rev.Sites != nil {
		t.Errorf("a revision made before any managed site has Sites %+v, want nil", rev.Sites)
	}

	first := m.applySites(t, mkSite("a", "a.example.com", "10.0.0.5"))
	rev := m.editMain(t, "# second\n")
	if rev.Sites == nil || rev.Sites.Content != first.Sites.Content || rev.Sites.SHA256 != first.Sites.SHA256 ||
		!slices.Equal(hostsOf(rev.Sites.Snapshot), []string{"a.example.com"}) {
		t.Errorf("revision sites = %+v, want the managed state of the sites applied before: %+v", rev.Sites, first.Sites)
	}
	if got, _ := m.managedText(t); got != first.Sites.Content {
		t.Errorf("an edit of the Caddyfile changed the managed file to %q", got)
	}
}

func TestRollbackRestoresTheCaddyfileTheManagedFileAndTheRows(t *testing.T) {
	ctx := context.Background()
	m := newManagedEnv(t)
	a, b := mkSite("a", "a.example.com", "10.0.0.5"), mkSite("b", "b.example.com", "10.0.0.6")

	r1 := m.applySites(t, a)  // the Caddyfile as it is, with a
	m.editMain(t, "# edited") // r2: another Caddyfile, still with a
	rows, _ := m.store.Sites(ctx)
	m.applySites(t, append(rows, b)...) // r3: with a and b
	m.caddy.mu.Lock()
	loadsBefore := len(m.caddy.loads)
	m.caddy.mu.Unlock()

	rev, err := m.mgr.Rollback(ctx, r1.Number)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.disk(t); got != r1.Content {
		t.Errorf("the Caddyfile = %q, want the one of revision %d", got, r1.Number)
	}
	if got, _ := m.managedText(t); got != r1.Sites.Content {
		t.Errorf("the managed file = %q, want the one of revision %d", got, r1.Number)
	}
	if now, _ := m.store.Sites(ctx); !slices.Equal(hostsOf(now), []string{"a.example.com"}) {
		t.Errorf("the rows = %v, want only a.example.com, as in revision %d", hostsOf(now), r1.Number)
	}
	if rev.Trigger != store.TriggerRollback || rev.Status != store.StatusApplied || rev.Sites == nil || !slices.Equal(hostsOf(rev.Sites.Snapshot), []string{"a.example.com"}) {
		t.Errorf("revision = %+v, want an applied rollback that holds the restored managed state", rev)
	}
	if got := m.caddy.loaded(); len(got) != loadsBefore+1 || got[len(got)-1] != r1.Content {
		t.Errorf("loads = %d, want one more, of the restored Caddyfile", len(got))
	}
}

func TestRollbackToARevisionWithoutManagedStateLeavesTheManagedPartAlone(t *testing.T) {
	ctx := context.Background()
	m := newManagedEnv(t)
	before := m.editMain(t, "# before any site") // no managed state
	withSite := m.applySites(t, mkSite("a", "a.example.com", "10.0.0.5"))

	rev, err := m.mgr.Rollback(ctx, before.Number)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.disk(t); got != before.Content {
		t.Errorf("the Caddyfile = %q, want the one of revision %d", got, before.Number)
	}
	if got, _ := m.managedText(t); got != withSite.Sites.Content {
		t.Errorf("the managed file = %q, want it left as it is", got)
	}
	if rows, _ := m.store.Sites(ctx); !slices.Equal(hostsOf(rows), []string{"a.example.com"}) {
		t.Errorf("the rows = %v, want them left as they are", hostsOf(rows))
	}
	if rev.Sites == nil || !slices.Equal(hostsOf(rev.Sites.Snapshot), []string{"a.example.com"}) {
		t.Errorf("revision sites = %+v, want the managed state that is in force carried along", rev.Sites)
	}
}

func TestRollbackPutsEverythingBackWhenCaddyRefusesIt(t *testing.T) {
	ctx := context.Background()
	m := newManagedEnv(t)
	r1 := m.applySites(t, mkSite("a", "a.example.com", "10.0.0.5"))
	rows, _ := m.store.Sites(ctx)
	r2 := m.applySites(t, append(rows, mkSite("b", "b.example.com", "10.0.0.6"))...)
	m.caddy.mu.Lock()
	n := len(m.caddy.loads)
	m.caddy.mu.Unlock()
	m.caddy.loadFn = func(_ context.Context, call int, _ string) error {
		if call == n+1 {
			return errBoom
		}
		return nil
	}

	if _, err := m.mgr.Rollback(ctx, r1.Number); err == nil {
		t.Fatal("expected the rollback to fail")
	}
	if got, _ := m.managedText(t); got != r2.Sites.Content {
		t.Errorf("the managed file = %q, want the one of revision %d put back", got, r2.Number)
	}
	if now, _ := m.store.Sites(ctx); !slices.Equal(hostsOf(now), []string{"a.example.com", "b.example.com"}) {
		t.Errorf("the rows = %v, want both sites as before", hostsOf(now))
	}
}

// ---- recovery of a revision that has managed state

func (m *menv) seedWithSites(t *testing.T, main string, status store.RevisionStatus, managed string, snapshot []store.Site) store.Revision {
	t.Helper()
	rev, err := m.store.AddRevision(context.Background(), store.Revision{
		Time: m.now, Trigger: store.TriggerSiteChange, Status: status, Content: main, SHA256: sum(main),
		Sites: &store.RevisionSites{Content: managed, SHA256: sum(managed), Snapshot: snapshot},
	})
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func TestRecoverOfARevisionWithManagedState(t *testing.T) {
	ctx := context.Background()
	a := mkSite("a", "a.example.com", "10.0.0.5")
	managedText := generated(sites.Options{}, a)
	same := map[string]any{"apps": map[string]any{"http": "candidate"}}

	t.Run("both files are the revision and Caddy runs them: applied, and the rows follow", func(t *testing.T) {
		m := newManagedEnv(t)
		m.writeManaged(t, managedText)
		m.caddy.running, m.caddy.candidateConfig = same, map[string]any{"apps": map[string]any{"http": "candidate"}}
		rev := m.seedWithSites(t, m.main, store.StatusLoading, managedText, []store.Site{a})

		got, err := m.mgr.Recover(ctx)
		if err != nil || len(got) != 1 || got[0].Status != store.StatusApplied {
			t.Fatalf("Recover() = %+v, %v, want the revision applied", got, err)
		}
		if m.status(t, rev.Number).Status != store.StatusApplied {
			t.Errorf("stored status = %q, want applied", m.status(t, rev.Number).Status)
		}
		if rows, _ := m.store.Sites(ctx); !slices.Equal(hostsOf(rows), []string{"a.example.com"}) {
			t.Errorf("the rows = %v, want them set from the snapshot", hostsOf(rows))
		}
	})

	t.Run("the managed file is not the revision's: failed, and the rows stay", func(t *testing.T) {
		m := newManagedEnv(t)
		m.writeManaged(t, "# something else\n")
		rev := m.seedWithSites(t, m.main, store.StatusLoading, managedText, []store.Site{a})

		got, err := m.mgr.Recover(ctx)
		if err != nil || len(got) != 1 || got[0].Status != store.StatusFailed {
			t.Fatalf("Recover() = %+v, %v, want the revision failed", got, err)
		}
		if m.status(t, rev.Number).Status != store.StatusFailed {
			t.Errorf("stored status = %q, want failed", m.status(t, rev.Number).Status)
		}
		if rows, _ := m.store.Sites(ctx); len(rows) != 0 {
			t.Errorf("the rows = %v, want none: the revision did not take effect", hostsOf(rows))
		}
	})

	t.Run("the managed file is missing: failed", func(t *testing.T) {
		m := newManagedEnv(t)
		m.seedWithSites(t, m.main, store.StatusLoading, managedText, []store.Site{a})
		got, err := m.mgr.Recover(ctx)
		if err != nil || len(got) != 1 || got[0].Status != store.StatusFailed {
			t.Errorf("Recover() = %+v, %v, want the revision failed", got, err)
		}
	})

	t.Run("both files are the revision but Caddy runs something else: a recovery is needed, the rows stay", func(t *testing.T) {
		m := newManagedEnv(t)
		m.writeManaged(t, managedText)
		m.caddy.running, m.caddy.candidateConfig = map[string]any{"apps": "old"}, same
		m.seedWithSites(t, m.main, store.StatusLoading, managedText, []store.Site{a})

		got, err := m.mgr.Recover(ctx)
		if err != nil || len(got) != 1 || got[0].Status != store.StatusRecoveryRequired {
			t.Fatalf("Recover() = %+v, %v, want a recovery to be needed", got, err)
		}
		if rows, _ := m.store.Sites(ctx); len(rows) != 0 {
			t.Errorf("the rows = %v, want none until somebody decides", hostsOf(rows))
		}
		if len(m.caddy.loads) != 0 {
			t.Errorf("Recover loaded %d configurations, want none", len(m.caddy.loads))
		}
	})
}

func TestApplyOfTheCaddyfileThatStopsImportingTheManagedFileIsRefusedWhileThereAreSites(t *testing.T) {
	ctx := context.Background()
	m := newManagedEnv(t)

	// With no site there is nothing to lose, so the import may go.
	cur, _ := m.mgr.Current(ctx)
	if _, err := m.mgr.Apply(ctx, ApplyRequest{Content: original, BaseSHA256: cur.SHA256}); err != nil {
		t.Fatalf("Apply() without any site: error = %v, want it applied", err)
	}
	if err := os.WriteFile(m.path, []byte(m.main), 0o644); err != nil {
		t.Fatal(err)
	}

	m.applySites(t, mkSite("a", "a.example.com", "10.0.0.5"))
	cur, _ = m.mgr.Current(ctx)
	before := len(m.store.all())
	_, err := m.mgr.Apply(ctx, ApplyRequest{Content: original, BaseSHA256: cur.SHA256})
	if !errors.Is(err, ErrNotImported) {
		t.Fatalf("Apply() error = %v, want ErrNotImported: the managed sites would stop being served", err)
	}
	if got := m.disk(t); got != m.main {
		t.Errorf("the Caddyfile became %q, want it untouched", got)
	}
	if len(m.store.all()) != before {
		t.Error("a revision was recorded for a change that was refused")
	}
}
