package configmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/sites"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// newSitesOnlyEnv is a managed env whose Manager never writes the Caddyfile and never keeps a copy of it.
func newSitesOnlyEnv(t *testing.T) *menv {
	t.Helper()
	m := newManagedEnv(t)
	m.mgr = New(m.caddy, m.store, Options{
		Path: m.path, ManagedPath: m.managed, SitesOnly: true, Now: func() time.Time { return m.now },
	})
	return m
}

const secretCaddyfile = "example.com {\n\trespond \"original\"\n\theader X-Token \"s3cr3t-token-value\"\n}\n"

func (m *menv) writeCaddyfile(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(m.path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m.main = content
}

func TestSitesOnlyRefusesToChangeTheCaddyfile(t *testing.T) {
	ctx := context.Background()
	m := newSitesOnlyEnv(t)
	cur, _ := m.mgr.Current(ctx)

	rev, err := m.mgr.Apply(ctx, ApplyRequest{Content: candidate, BaseSHA256: cur.SHA256})
	if !errors.Is(err, ErrSitesOnly) {
		t.Fatalf("Apply() error = %v, want ErrSitesOnly", err)
	}
	if rev.Number != 0 {
		t.Errorf("a revision was recorded: %+v", rev)
	}
	if got := m.disk(t); got != m.main {
		t.Errorf("the Caddyfile became %q", got)
	}
	if len(m.caddy.loaded()) != 0 {
		t.Error("Caddy was asked to load something")
	}
	m.noRevision(t)
}

func TestSitesOnlyRefusesToAddTheImport(t *testing.T) {
	ctx := context.Background()
	m := newSitesOnlyEnv(t)
	m.writeCaddyfile(t, original) // no import
	if _, err := m.mgr.AddImport(ctx); !errors.Is(err, ErrSitesOnly) {
		t.Fatalf("AddImport() error = %v, want ErrSitesOnly", err)
	}
	if got := m.disk(t); got != original {
		t.Errorf("the Caddyfile became %q", got)
	}
	if _, ok := m.managedText(t); ok {
		t.Error("a managed file was created")
	}
}

func TestSitesOnlyAppliesSitesAndLeavesTheCaddyfileAlone(t *testing.T) {
	ctx := context.Background()
	m := newSitesOnlyEnv(t)
	m.writeCaddyfile(t, "import "+filepath.ToSlash(m.dir)+"/conf.d/*.caddy\n"+secretCaddyfile)
	a := mkSite("a", "a.example.com", "10.0.0.5:8080")

	rev, err := m.mgr.ApplySites(ctx, []store.Site{a})
	if err != nil || rev.Status != store.StatusApplied {
		t.Fatalf("ApplySites() = %+v, %v, want it applied", rev, err)
	}
	if got, ok := m.managedText(t); !ok || got != generated(sites.Options{}, a) {
		t.Errorf("managed file = %q, %v", got, ok)
	}
	if got := m.disk(t); got != m.main {
		t.Errorf("the Caddyfile became %q", got)
	}
	if got := m.caddy.loaded(); !slices.Equal(got, []string{m.main}) {
		t.Errorf("loads = %q, want the Caddyfile as it is on disk, once", got)
	}
	if rows, _ := m.store.Sites(ctx); !slices.Equal(hostsOf(rows), []string{"a.example.com"}) {
		t.Errorf("rows = %v, want a.example.com", hostsOf(rows))
	}
}

func TestSitesOnlyKeepsNoCopyOfTheCaddyfile(t *testing.T) {
	ctx := context.Background()
	m := newSitesOnlyEnv(t)
	m.writeCaddyfile(t, "import "+filepath.ToSlash(m.dir)+"/conf.d/*.caddy\n"+secretCaddyfile)
	a, b := mkSite("a", "a.example.com", "10.0.0.5"), mkSite("b", "b.example.com", "10.0.0.6")

	first, err := m.mgr.ApplySites(ctx, []store.Site{a})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.mgr.ApplySites(ctx, []store.Site{a, b}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.mgr.Rollback(ctx, first.Number); err != nil {
		t.Fatal(err)
	}

	all := m.store.all()
	if len(all) != 3 {
		t.Fatalf("revisions = %d, want 3", len(all))
	}
	for _, r := range all {
		if r.Content != "" {
			t.Errorf("revision %d has Content %q, want none", r.Number, r.Content)
		}
		if r.SHA256 != sum("") {
			t.Errorf("revision %d has SHA256 %s, want the digest of nothing", r.Number, r.SHA256)
		}
		if strings.Contains(r.Content, "s3cr3t") || r.Sites == nil || strings.Contains(r.Sites.Content, "s3cr3t") {
			t.Errorf("revision %d holds the secret, or has no managed state: %+v", r.Number, r)
		}
	}
}

func TestSitesOnlyRollbackRestoresTheManagedStateAndNotTheCaddyfile(t *testing.T) {
	ctx := context.Background()
	m := newSitesOnlyEnv(t)
	a, b := mkSite("a", "a.example.com", "10.0.0.5"), mkSite("b", "b.example.com", "10.0.0.6")
	first, err := m.mgr.ApplySites(ctx, []store.Site{a})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := m.store.Sites(ctx)
	if _, err := m.mgr.ApplySites(ctx, append(rows, b)); err != nil {
		t.Fatal(err)
	}
	// The person edits the Caddyfile by hand in the meantime.
	m.writeCaddyfile(t, m.main+"# edited by hand\n")

	rev, err := m.mgr.Rollback(ctx, first.Number)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.disk(t); got != m.main {
		t.Errorf("the Caddyfile = %q, want it left as the person wrote it", got)
	}
	if got, _ := m.managedText(t); got != first.Sites.Content {
		t.Errorf("the managed file = %q, want the one of revision %d", got, first.Number)
	}
	if now, _ := m.store.Sites(ctx); !slices.Equal(hostsOf(now), []string{"a.example.com"}) {
		t.Errorf("rows = %v, want only a.example.com", hostsOf(now))
	}
	if rev.Trigger != store.TriggerRollback || rev.Status != store.StatusApplied || rev.Content != "" {
		t.Errorf("revision = %+v, want an applied rollback with no Caddyfile", rev)
	}
	if got := m.caddy.loaded(); got[len(got)-1] != m.main {
		t.Errorf("the last load was %q, want the Caddyfile as it is now", got[len(got)-1])
	}
}

func TestSitesOnlyRollbackToARevisionFromFullModeRestoresItsManagedPart(t *testing.T) {
	ctx := context.Background()
	m := newManagedEnv(t) // full mode first
	a := mkSite("a", "a.example.com", "10.0.0.5")
	full, err := m.mgr.ApplySites(ctx, []store.Site{a})
	if err != nil || full.Content == "" {
		t.Fatalf("ApplySites() = %+v, %v, want a revision that has the Caddyfile", full, err)
	}
	rows, _ := m.store.Sites(ctx)
	if _, err := m.mgr.ApplySites(ctx, append(rows, mkSite("b", "b.example.com", "10.0.0.6"))); err != nil {
		t.Fatal(err)
	}
	// Then the same store is used in sites-only mode.
	m.mgr = New(m.caddy, m.store, Options{Path: m.path, ManagedPath: m.managed, SitesOnly: true, Now: func() time.Time { return m.now }})
	m.writeCaddyfile(t, m.main+"# edited by hand\n")
	before := m.disk(t)

	if _, err := m.mgr.Rollback(ctx, full.Number); err != nil {
		t.Fatal(err)
	}
	if got := m.disk(t); got != before {
		t.Errorf("the Caddyfile changed to %q: the copy in a full revision must not be written back", got)
	}
	if now, _ := m.store.Sites(ctx); !slices.Equal(hostsOf(now), []string{"a.example.com"}) {
		t.Errorf("rows = %v, want only a.example.com", hostsOf(now))
	}
}

func TestSitesOnlyRollbackToARevisionWithNothingManagedHasNothingToRestore(t *testing.T) {
	ctx := context.Background()
	m := newSitesOnlyEnv(t)
	plain, err := m.store.AddRevision(ctx, store.Revision{
		Time: m.now, Trigger: store.TriggerManualEdit, Status: store.StatusApplied, Content: original, SHA256: sum(original),
	})
	if err != nil {
		t.Fatal(err)
	}
	loads := len(m.caddy.loaded())
	if _, err := m.mgr.Rollback(ctx, plain.Number); !errors.Is(err, ErrNothingToRestore) {
		t.Fatalf("Rollback() error = %v, want ErrNothingToRestore", err)
	}
	if len(m.caddy.loaded()) != loads {
		t.Error("Caddy was asked to load something")
	}
	if got := m.disk(t); got != m.main {
		t.Errorf("the Caddyfile became %q", got)
	}
}

func TestSitesOnlyRecoverDoesNotLookForTheCaddyfileInTheRevision(t *testing.T) {
	ctx := context.Background()
	a := mkSite("a", "a.example.com", "10.0.0.5")
	managedText := generated(sites.Options{}, a)
	same := map[string]any{"apps": map[string]any{"http": "candidate"}}

	m := newSitesOnlyEnv(t)
	m.writeManaged(t, managedText)
	m.caddy.running, m.caddy.candidateConfig = same, map[string]any{"apps": map[string]any{"http": "candidate"}}
	rev := m.seedWithSites(t, "", store.StatusLoading, managedText, []store.Site{a})

	got, err := m.mgr.Recover(ctx)
	if err != nil || len(got) != 1 || got[0].Status != store.StatusApplied {
		t.Fatalf("Recover() = %+v, %v, want the revision applied", got, err)
	}
	if m.status(t, rev.Number).Status != store.StatusApplied {
		t.Errorf("stored status = %q, want applied", m.status(t, rev.Number).Status)
	}
	if rows, _ := m.store.Sites(ctx); !slices.Equal(hostsOf(rows), []string{"a.example.com"}) {
		t.Errorf("rows = %v, want them set from the snapshot", hostsOf(rows))
	}
}

func TestSitesOnlyRecoverStillChecksTheManagedFile(t *testing.T) {
	ctx := context.Background()
	a := mkSite("a", "a.example.com", "10.0.0.5")
	m := newSitesOnlyEnv(t)
	m.writeManaged(t, "# something else\n")
	m.seedWithSites(t, "", store.StatusLoading, generated(sites.Options{}, a), []store.Site{a})
	got, err := m.mgr.Recover(ctx)
	if err != nil || len(got) != 1 || got[0].Status != store.StatusFailed {
		t.Errorf("Recover() = %+v, %v, want the revision failed", got, err)
	}
}

func TestSitesOnlyHasNoDriftToReport(t *testing.T) {
	ctx := context.Background()
	m := newSitesOnlyEnv(t)
	if _, err := m.mgr.ApplySites(ctx, []store.Site{mkSite("a", "a.example.com", "10.0.0.5")}); err != nil {
		t.Fatal(err)
	}
	m.writeCaddyfile(t, m.main+"# edited by hand\n")
	d, err := m.mgr.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.Known || d.Drifted {
		t.Errorf("Reconcile() = %+v, want no drift known: the Caddyfile is not versioned in this mode", d)
	}
	if d.DiskSHA256 != sum(m.main) {
		t.Errorf("DiskSHA256 = %s, want the digest of the file", d.DiskSHA256)
	}
}

func TestSitesOnlyStillGuardsTheCaddyfileItReads(t *testing.T) {
	ctx := context.Background()
	m := newSitesOnlyEnv(t)
	m.writeCaddyfile(t, original) // does not import the managed file
	if _, err := m.mgr.ApplySites(ctx, []store.Site{mkSite("a", "a.example.com", "10.0.0.5")}); !errors.Is(err, ErrNotImported) {
		t.Errorf("ApplySites() error = %v, want ErrNotImported", err)
	}
	m.writeCaddyfile(t, "import conf.d/*.caddy\n"+original)
	if _, err := m.mgr.ApplySites(ctx, []store.Site{mkSite("a", "a.example.com", "10.0.0.5")}); !errors.Is(err, ErrRelativeImport) {
		t.Errorf("ApplySites() error = %v, want ErrRelativeImport", err)
	}
}

func TestTheModeIsNotSitesOnlyByDefault(t *testing.T) {
	ctx := context.Background()
	m := newManagedEnv(t)
	cur, _ := m.mgr.Current(ctx)
	if _, err := m.mgr.Apply(ctx, ApplyRequest{Content: m.main + "# edit\n", BaseSHA256: cur.SHA256}); err != nil {
		t.Errorf("Apply() in the default mode: %v", err)
	}
}
