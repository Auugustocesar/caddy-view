package configmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/sites"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// menv is an env whose Caddyfile imports a managed file by its absolute path, and whose Manager knows about it.
type menv struct {
	*env
	dir       string
	managed   string
	main      string // the Caddyfile text, which no operation on sites changes
	accessLog string // what the Manager is told the access log is
}

func newManagedEnv(t *testing.T) *menv {
	t.Helper()
	dir := t.TempDir()
	m := &menv{dir: dir, managed: filepath.Join(dir, "conf.d", "caddy-view-sites.caddy")}
	m.main = "import " + filepath.ToSlash(dir) + "/conf.d/*.caddy\n" + original
	path := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(path, []byte(m.main), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &env{caddy: &fakeCaddy{}, store: &fakeStore{}, path: path, now: time.UnixMilli(1_700_000_000_000)}
	e.mgr = New(e.caddy, e.store, Options{
		Path: path, ManagedPath: m.managed, Now: func() time.Time { return e.now },
		AccessLog: func(context.Context) string { return m.accessLog },
	})
	m.env = e
	return m
}

func (m *menv) managedText(t *testing.T) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(m.managed)
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

func (m *menv) writeManaged(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(m.managed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.managed, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkSite(id, host string, upstreams ...string) store.Site {
	return store.Site{ID: id, Hostname: host, Kind: store.SiteProxy, Upstreams: upstreams, TLS: store.TLSAutomatic, Enabled: true}
}

func generated(opts sites.Options, all ...store.Site) string { return sites.Generate(all, opts).Text }

func hostsOf(all []store.Site) []string {
	var out []string
	for _, s := range all {
		out = append(out, s.Hostname)
	}
	return out
}

func (m *menv) noRevision(t *testing.T) {
	t.Helper()
	if got := m.store.all(); len(got) != 0 {
		t.Errorf("revisions = %+v, want none", got)
	}
}

// ---- what ApplySites does when all goes well

func TestApplySitesWritesTheManagedFileAndLoadsTheCaddyfile(t *testing.T) {
	m := newManagedEnv(t)
	a := mkSite("a", "a.example.com", "10.0.0.5:8080")

	rev, err := m.mgr.ApplySites(context.Background(), []store.Site{a})
	if err != nil {
		t.Fatal(err)
	}
	want := generated(sites.Options{}, a)
	if got, ok := m.managedText(t); !ok || got != want {
		t.Errorf("managed file = %q, %v, want\n%s", got, ok, want)
	}
	if got := m.disk(t); got != m.main {
		t.Errorf("the Caddyfile changed to %q", got)
	}
	if got := m.caddy.loaded(); !slices.Equal(got, []string{m.main}) {
		t.Errorf("loads = %q, want the Caddyfile text once", got)
	}
	if rev.Status != store.StatusApplied || rev.Trigger != store.TriggerSiteChange || rev.Content != m.main {
		t.Errorf("revision = %+v, want applied, a site change, with the Caddyfile as it is", rev)
	}
	if rev.Sites == nil || rev.Sites.Content != want || rev.Sites.SHA256 != sum(want) || !slices.Equal(hostsOf(rev.Sites.Snapshot), []string{"a.example.com"}) {
		t.Errorf("revision sites = %+v, want the managed text, its digest and the snapshot", rev.Sites)
	}
	if rows, _ := m.store.Sites(context.Background()); !slices.Equal(hostsOf(rows), []string{"a.example.com"}) {
		t.Errorf("stored sites = %v, want a.example.com", hostsOf(rows))
	}
	if got := m.store.updates[rev.Number]; !slices.Equal(got, []store.RevisionStatus{store.StatusLoading, store.StatusApplied}) {
		t.Errorf("status changes = %v, want loading then applied", got)
	}
}

func TestApplySitesStampsTheTimes(t *testing.T) {
	m := newManagedEnv(t)
	ctx := context.Background()
	if _, err := m.mgr.ApplySites(ctx, []store.Site{mkSite("a", "a.example.com", "10.0.0.5")}); err != nil {
		t.Fatal(err)
	}
	created := m.now
	m.now = m.now.Add(time.Hour)
	rows, _ := m.store.Sites(ctx)
	changed := rows[0]
	changed.Upstreams = []string{"10.0.0.6"}
	added := mkSite("b", "b.example.com", "10.0.0.7")
	if _, err := m.mgr.ApplySites(ctx, []store.Site{changed, added}); err != nil {
		t.Fatal(err)
	}
	rows, _ = m.store.Sites(ctx)
	if len(rows) != 2 {
		t.Fatalf("stored sites = %v, want two", hostsOf(rows))
	}
	byID := map[string]store.Site{rows[0].ID: rows[0], rows[1].ID: rows[1]}
	if a := byID["a"]; !a.CreatedAt.Equal(created) || !a.UpdatedAt.Equal(m.now) {
		t.Errorf("a: created %v, updated %v; want created %v (kept) and updated %v", a.CreatedAt, a.UpdatedAt, created, m.now)
	}
	if b := byID["b"]; !b.CreatedAt.Equal(m.now) || !b.UpdatedAt.Equal(m.now) {
		t.Errorf("b: created %v, updated %v; want both %v", b.CreatedAt, b.UpdatedAt, m.now)
	}
}

func TestApplySitesLeavesAnUntouchedSiteWithItsUpdateTime(t *testing.T) {
	m := newManagedEnv(t)
	ctx := context.Background()
	if _, err := m.mgr.ApplySites(ctx, []store.Site{mkSite("a", "a.example.com", "10.0.0.5")}); err != nil {
		t.Fatal(err)
	}
	first := m.now
	m.now = m.now.Add(time.Hour)
	rows, _ := m.store.Sites(ctx)
	if _, err := m.mgr.ApplySites(ctx, []store.Site{rows[0], mkSite("b", "b.example.com", "10.0.0.7")}); err != nil {
		t.Fatal(err)
	}
	rows, _ = m.store.Sites(ctx)
	for _, r := range rows {
		if r.ID == "a" && !r.UpdatedAt.Equal(first) {
			t.Errorf("a was not changed, but its update time moved to %v", r.UpdatedAt)
		}
	}
}

func TestApplySitesCreatesTheDirectoryOfTheManagedFile(t *testing.T) {
	m := newManagedEnv(t)
	if _, err := os.Stat(filepath.Dir(m.managed)); err == nil {
		t.Fatal("the directory of the managed file exists before the test")
	}
	if _, err := m.mgr.ApplySites(context.Background(), []store.Site{mkSite("a", "a.example.com", "10.0.0.5")}); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.managedText(t); !ok {
		t.Error("the managed file was not written")
	}
}

func TestApplySitesWithNoSitesWritesTheHeaderOnly(t *testing.T) {
	m := newManagedEnv(t)
	if _, err := m.mgr.ApplySites(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.managedText(t); got != generated(sites.Options{}) {
		t.Errorf("managed file = %q, want only the header", got)
	}
}

func TestApplySitesWritesTheAccessLogSnippetWhenItsFileIsKnown(t *testing.T) {
	m := newManagedEnv(t)
	m.accessLog = "/var/log/caddy/access.log"
	a := mkSite("a", "a.example.com", "10.0.0.5")
	a.AccessLog = true
	if _, err := m.mgr.ApplySites(context.Background(), []store.Site{a}); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.managedText(t); got != generated(sites.Options{AccessLog: m.accessLog}, a) || !strings.Contains(got, "output file /var/log/caddy/access.log") {
		t.Errorf("managed file = %q, want the snippet for /var/log/caddy/access.log", got)
	}
}

// ---- what it refuses before it records anything

func TestApplySitesRefusesInvalidSites(t *testing.T) {
	ctx := context.Background()
	t.Run("a bad field, with the site and the field named", func(t *testing.T) {
		m := newManagedEnv(t)
		bad := mkSite("a", "app example.com", "10.0.0.5:0")
		_, err := m.mgr.ApplySites(ctx, []store.Site{bad})
		var invalid *InvalidSitesError
		if !errors.Is(err, ErrInvalidSite) || !errors.As(err, &invalid) {
			t.Fatalf("ApplySites() error = %v, want ErrInvalidSite with the problems", err)
		}
		fields := map[string]bool{}
		for _, e := range invalid.Errors {
			if e.SiteID != "a" || e.Hostname == "" || e.Message == "" {
				t.Errorf("problem %+v does not say which site and why", e)
			}
			fields[e.Field] = true
		}
		if !fields["hostname"] || !fields["upstreams"] {
			t.Errorf("fields = %v, want hostname and upstreams", fields)
		}
	})
	t.Run("the same hostname twice", func(t *testing.T) {
		m := newManagedEnv(t)
		_, err := m.mgr.ApplySites(ctx, []store.Site{mkSite("a", "same.example.com", "10.0.0.5"), mkSite("b", "SAME.example.com", "10.0.0.6")})
		if !errors.Is(err, ErrInvalidSite) {
			t.Fatalf("ApplySites() error = %v, want ErrInvalidSite", err)
		}
	})
	t.Run("an access log nobody knows the file of", func(t *testing.T) {
		m := newManagedEnv(t) // accessLog is empty
		a := mkSite("a", "a.example.com", "10.0.0.5")
		a.AccessLog = true
		if _, err := m.mgr.ApplySites(ctx, []store.Site{a}); !errors.Is(err, ErrInvalidSite) {
			t.Fatalf("ApplySites() error = %v, want ErrInvalidSite", err)
		}
	})
}

func TestApplySitesChangesNothingForInvalidSites(t *testing.T) {
	m := newManagedEnv(t)
	if _, err := m.mgr.ApplySites(context.Background(), []store.Site{mkSite("a", "", "10.0.0.5")}); err == nil {
		t.Fatal("expected a refusal")
	}
	m.noRevision(t)
	if _, ok := m.managedText(t); ok {
		t.Error("the managed file was written")
	}
	if len(m.caddy.adapted) != 0 || len(m.caddy.loads) != 0 {
		t.Errorf("caddy was called (adapt %d, load %d)", len(m.caddy.adapted), len(m.caddy.loads))
	}
}

func TestApplySitesNeedsTheCaddyfileToImportTheManagedFile(t *testing.T) {
	m := newManagedEnv(t)
	if err := os.WriteFile(m.path, []byte(original), 0o644); err != nil { // no import
		t.Fatal(err)
	}
	_, err := m.mgr.ApplySites(context.Background(), []store.Site{mkSite("a", "a.example.com", "10.0.0.5")})
	if !errors.Is(err, ErrNotImported) {
		t.Fatalf("ApplySites() error = %v, want ErrNotImported", err)
	}
	m.noRevision(t)
	if _, ok := m.managedText(t); ok || len(m.caddy.loads) != 0 {
		t.Errorf("something was written or loaded (managed file exists: %v, loads: %d)", ok, len(m.caddy.loads))
	}
}

func TestApplySitesRefusesACaddyfileWithARelativeImport(t *testing.T) {
	m := newManagedEnv(t)
	if err := os.WriteFile(m.path, []byte("import conf.d/*.caddy\n"+original), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := m.mgr.ApplySites(context.Background(), []store.Site{mkSite("a", "a.example.com", "10.0.0.5")})
	if !errors.Is(err, ErrRelativeImport) {
		t.Fatalf("ApplySites() error = %v, want ErrRelativeImport", err)
	}
	m.noRevision(t)
	if len(m.caddy.loads) != 0 {
		t.Errorf("caddy loaded %d times, want none", len(m.caddy.loads))
	}
}

func TestApplySitesWithoutAManagedFileIsRefused(t *testing.T) {
	e := newEnv(t, original) // a Manager with no ManagedPath
	if _, err := e.mgr.ApplySites(context.Background(), []store.Site{mkSite("a", "a.example.com", "10.0.0.5")}); !errors.Is(err, ErrNoManagedFile) {
		t.Errorf("ApplySites() error = %v, want ErrNoManagedFile", err)
	}
}

func TestApplySitesOverwritesOnlyWhatItWroteOrWhatIsEmpty(t *testing.T) {
	ctx := context.Background()
	a := mkSite("a", "a.example.com", "10.0.0.5")

	for name, content := range map[string]string{"an empty file": "", "a file of blanks": "\n  \n"} {
		t.Run(name, func(t *testing.T) {
			m := newManagedEnv(t)
			m.writeManaged(t, content)
			if _, err := m.mgr.ApplySites(ctx, []store.Site{a}); err != nil {
				t.Errorf("ApplySites() error = %v, want it applied over %s", err, name)
			}
		})
	}

	t.Run("a file somebody wrote", func(t *testing.T) {
		m := newManagedEnv(t)
		m.writeManaged(t, "hand.example.com {\n\trespond \"mine\"\n}\n")
		_, err := m.mgr.ApplySites(ctx, []store.Site{a})
		if !errors.Is(err, ErrManagedEdited) {
			t.Fatalf("ApplySites() error = %v, want ErrManagedEdited", err)
		}
		if got, _ := m.managedText(t); !strings.Contains(got, "hand.example.com") {
			t.Errorf("the managed file became %q: the hand-written content was lost", got)
		}
		m.noRevision(t)
		if len(m.caddy.loads) != 0 {
			t.Errorf("caddy loaded %d times, want none", len(m.caddy.loads))
		}
	})

	t.Run("a file that was edited after caddy-view wrote it", func(t *testing.T) {
		m := newManagedEnv(t)
		if _, err := m.mgr.ApplySites(ctx, []store.Site{a}); err != nil {
			t.Fatal(err)
		}
		text, _ := m.managedText(t)
		m.writeManaged(t, text+"\nextra.example.com {\n\trespond \"added by hand\"\n}\n")
		if _, err := m.mgr.ApplySites(ctx, []store.Site{a, mkSite("b", "b.example.com", "10.0.0.6")}); !errors.Is(err, ErrManagedEdited) {
			t.Fatalf("ApplySites() error = %v, want ErrManagedEdited", err)
		}
	})

	t.Run("a file it wrote, so the next change goes through", func(t *testing.T) {
		m := newManagedEnv(t)
		if _, err := m.mgr.ApplySites(ctx, []store.Site{a}); err != nil {
			t.Fatal(err)
		}
		rows, _ := m.store.Sites(ctx)
		if _, err := m.mgr.ApplySites(ctx, append(rows, mkSite("b", "b.example.com", "10.0.0.6"))); err != nil {
			t.Errorf("the second ApplySites() error = %v, want it applied", err)
		}
	})
}

// ---- what it does when Caddy says no

func (m *menv) refuseManagedFragment(line int) {
	m.caddy.adaptFn = func(content string) error {
		if strings.HasPrefix(content, "# Managed by caddy-view") {
			return &caddy.AdaptError{Message: fmt.Sprintf("Caddyfile:%d - Error during parsing: unrecognized directive", line), Line: line}
		}
		return nil
	}
}

func TestApplySitesNamesTheSiteCaddyRejectsInTheManagedText(t *testing.T) {
	m := newManagedEnv(t)
	a, b := mkSite("a", "a.example.com", "10.0.0.5"), mkSite("b", "b.example.com", "10.0.0.6")
	// The text is: 1 header, 2 blank, 3-5 a.example.com, 6 blank, 7-9 b.example.com.
	m.refuseManagedFragment(8)

	rev, err := m.mgr.ApplySites(context.Background(), []store.Site{a, b})
	var siteErr *SiteError
	if !errors.Is(err, ErrSiteRejected) || !errors.As(err, &siteErr) {
		t.Fatalf("ApplySites() error = %v, want ErrSiteRejected naming a site", err)
	}
	if siteErr.SiteID != "b" || siteErr.Hostname != "b.example.com" || siteErr.Line != 8 || siteErr.Message == "" {
		t.Errorf("SiteError = %+v, want site b at line 8 with Caddy's message", siteErr)
	}
	if rev.Status != store.StatusFailed || rev.Error == "" {
		t.Errorf("revision = %+v, want failed with the reason", rev)
	}
	if _, ok := m.managedText(t); ok || len(m.caddy.loads) != 0 {
		t.Errorf("the managed file was written or Caddy loaded (file exists: %v, loads: %d)", ok, len(m.caddy.loads))
	}
	if rows, _ := m.store.Sites(context.Background()); len(rows) != 0 {
		t.Errorf("the rows changed to %v", hostsOf(rows))
	}
}

func TestApplySitesWithAProblemOutsideTheBlocksIsAnInvalidCaddyfileNotASite(t *testing.T) {
	m := newManagedEnv(t)
	m.refuseManagedFragment(1) // the header
	_, err := m.mgr.ApplySites(context.Background(), []store.Site{mkSite("a", "a.example.com", "10.0.0.5")})
	if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrSiteRejected) {
		t.Errorf("ApplySites() error = %v, want ErrInvalid and not ErrSiteRejected", err)
	}
}

func TestApplySitesPutsTheManagedFileBackWhenTheCaddyfileIsRejectedWithIt(t *testing.T) {
	a, b := mkSite("a", "a.example.com", "10.0.0.5"), mkSite("b", "b.example.com", "10.0.0.6")

	tests := []struct {
		name    string
		message func(m *menv) string
		check   func(t *testing.T, err error)
	}{
		{
			"Caddy names the managed file and a line",
			func(m *menv) string {
				return fmt.Sprintf("adapting config using caddyfile: ambiguous site definition at %s:7", m.managed)
			},
			func(t *testing.T, err error) {
				var siteErr *SiteError
				if !errors.Is(err, ErrSiteRejected) || !errors.As(err, &siteErr) || siteErr.SiteID != "b" || siteErr.Line != 7 {
					t.Errorf("error = %v, want site b at line 7", err)
				}
			},
		},
		{
			"Caddy names only a managed hostname",
			func(*menv) string { return "adapting config using caddyfile: ambiguous site definition: b.example.com" },
			func(t *testing.T, err error) {
				var siteErr *SiteError
				if !errors.Is(err, ErrSiteRejected) || !errors.As(err, &siteErr) || siteErr.SiteID != "b" || siteErr.Line != 0 {
					t.Errorf("error = %v, want site b, found by its hostname", err)
				}
			},
		},
		{
			"Caddy blames the Caddyfile itself",
			func(*menv) string { return "Caddyfile:2 - Error during parsing: unrecognized directive: nope" },
			func(t *testing.T, err error) {
				if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrSiteRejected) {
					t.Errorf("error = %v, want ErrInvalid and not ErrSiteRejected", err)
				}
			},
		},
	}
	for _, tt := range tests {
		for _, existed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, managed file existed: %v", tt.name, existed), func(t *testing.T) {
				m := newManagedEnv(t)
				ctx := context.Background()
				var before string
				if existed {
					if _, err := m.mgr.ApplySites(ctx, []store.Site{a}); err != nil {
						t.Fatal(err)
					}
					before, _ = m.managedText(t)
					m.caddy.mu.Lock()
					m.caddy.loads = nil
					m.caddy.mu.Unlock()
				}
				rows, _ := m.store.Sites(ctx)
				m.caddy.adaptFn = func(content string) error {
					// The Caddyfile is refused once the new managed file is on disk.
					if strings.HasPrefix(content, "import") {
						if text, _ := m.managedText(t); strings.Contains(text, "b.example.com") {
							return &caddy.AdaptError{Message: tt.message(m)}
						}
					}
					return nil
				}

				rev, err := m.mgr.ApplySites(ctx, []store.Site{a, b})
				tt.check(t, err)
				if rev.Status != store.StatusFailed || rev.Error == "" {
					t.Errorf("revision = %+v, want failed with the reason", rev)
				}
				after, ok := m.managedText(t)
				if ok != existed || after != before {
					t.Errorf("managed file = %q (exists: %v), want it as before: %q (exists: %v)", after, ok, before, existed)
				}
				if len(m.caddy.loads) != 0 {
					t.Errorf("caddy loaded %d times, want none", len(m.caddy.loads))
				}
				if now, _ := m.store.Sites(ctx); !slices.Equal(hostsOf(now), hostsOf(rows)) {
					t.Errorf("the rows changed from %v to %v", hostsOf(rows), hostsOf(now))
				}
			})
		}
	}
}

func TestApplySitesUndoesWhatCaddyCannotLoad(t *testing.T) {
	ctx := context.Background()
	a, b := mkSite("a", "a.example.com", "10.0.0.5"), mkSite("b", "b.example.com", "10.0.0.6")

	t.Run("a load that is refused", func(t *testing.T) {
		m := newManagedEnv(t)
		if _, err := m.mgr.ApplySites(ctx, []store.Site{a}); err != nil {
			t.Fatal(err)
		}
		before, _ := m.managedText(t)
		m.caddy.mu.Lock()
		m.caddy.loads = nil
		m.caddy.mu.Unlock()
		m.caddy.loadFn = func(_ context.Context, n int, _ string) error {
			if n == 1 {
				return errBoom
			}
			return nil
		}

		rev, err := m.mgr.ApplySites(ctx, []store.Site{a, b})
		if !errors.Is(err, ErrApplyFailed) || !errors.Is(err, errBoom) {
			t.Fatalf("ApplySites() error = %v, want ErrApplyFailed wrapping the cause", err)
		}
		if after, _ := m.managedText(t); after != before {
			t.Errorf("managed file = %q, want it put back to %q", after, before)
		}
		if got := m.caddy.loaded(); !slices.Equal(got, []string{m.main, m.main}) {
			t.Errorf("loads = %d, want the candidate and then the previous state again", len(got))
		}
		if rev.Status != store.StatusFailed || rev.Error == "" {
			t.Errorf("revision = %+v, want failed with the reason", rev)
		}
		if rows, _ := m.store.Sites(ctx); !slices.Equal(hostsOf(rows), []string{"a.example.com"}) {
			t.Errorf("the rows = %v, want only a.example.com: the failed change is not saved", hostsOf(rows))
		}
	})

	t.Run("a first site whose load is refused leaves no managed file", func(t *testing.T) {
		m := newManagedEnv(t)
		m.caddy.loadFn = func(_ context.Context, n int, _ string) error {
			if n == 1 {
				return errBoom
			}
			return nil
		}
		if _, err := m.mgr.ApplySites(ctx, []store.Site{a}); !errors.Is(err, ErrApplyFailed) {
			t.Fatalf("ApplySites() error = %v, want ErrApplyFailed", err)
		}
		if _, ok := m.managedText(t); ok {
			t.Error("the managed file is still there")
		}
	})

	t.Run("a load that Caddy accepts and does not run", func(t *testing.T) {
		m := newManagedEnv(t)
		m.caddy.candidateConfig = map[string]any{"apps": map[string]any{"http": "candidate"}}
		m.caddy.loadIgnored = true
		_, err := m.mgr.ApplySites(ctx, []store.Site{a})
		if !errors.Is(err, ErrApplyFailed) || !errors.Is(err, ErrNotRunning) {
			t.Fatalf("ApplySites() error = %v, want ErrApplyFailed wrapping ErrNotRunning", err)
		}
		if _, ok := m.managedText(t); ok {
			t.Error("the managed file is still there")
		}
		if rows, _ := m.store.Sites(ctx); len(rows) != 0 {
			t.Errorf("the rows = %v, want none", hostsOf(rows))
		}
	})

	t.Run("a load refused, and the reload of the previous state too", func(t *testing.T) {
		m := newManagedEnv(t)
		m.caddy.loadFn = func(context.Context, int, string) error { return errBoom }
		rev, err := m.mgr.ApplySites(ctx, []store.Site{a})
		if !errors.Is(err, ErrRecoveryRequired) {
			t.Fatalf("ApplySites() error = %v, want ErrRecoveryRequired", err)
		}
		if rev.Status != store.StatusRecoveryRequired {
			t.Errorf("revision status = %q, want recovery-required", rev.Status)
		}
	})

	t.Run("a change that cannot be saved after it was applied needs a recovery", func(t *testing.T) {
		m := newManagedEnv(t)
		m.store.replaceErr = errBoom
		rev, err := m.mgr.ApplySites(ctx, []store.Site{a})
		if !errors.Is(err, ErrRecoveryRequired) || !errors.Is(err, errBoom) {
			t.Fatalf("ApplySites() error = %v, want ErrRecoveryRequired wrapping the store error", err)
		}
		if rev.Status != store.StatusRecoveryRequired || rev.Error == "" {
			t.Errorf("revision = %+v, want recovery-required with the reason", rev)
		}
	})
}

func TestPreviewSitesGeneratesWithoutTouchingAnything(t *testing.T) {
	m := newManagedEnv(t)
	a := mkSite("a", "a.example.com", "10.0.0.5")
	got, err := m.mgr.PreviewSites(context.Background(), []store.Site{a})
	if err != nil || got != generated(sites.Options{}, a) {
		t.Fatalf("PreviewSites() = %q, %v, want the generated text", got, err)
	}
	m.noRevision(t)
	if _, ok := m.managedText(t); ok || len(m.caddy.adapted) != 0 || len(m.caddy.loads) != 0 {
		t.Errorf("PreviewSites touched something (managed file: %v, adapt %d, load %d)", ok, len(m.caddy.adapted), len(m.caddy.loads))
	}
	if _, err := m.mgr.PreviewSites(context.Background(), []store.Site{mkSite("a", "", "10.0.0.5")}); !errors.Is(err, ErrInvalidSite) {
		t.Errorf("PreviewSites() of a bad site: error = %v, want ErrInvalidSite", err)
	}
}

func TestSitesAreTheStoresRows(t *testing.T) {
	m := newManagedEnv(t)
	if _, err := m.mgr.ApplySites(context.Background(), []store.Site{mkSite("a", "a.example.com", "10.0.0.5")}); err != nil {
		t.Fatal(err)
	}
	got, err := m.mgr.Sites(context.Background())
	if err != nil || !slices.Equal(hostsOf(got), []string{"a.example.com"}) {
		t.Errorf("Sites() = %v, %v, want a.example.com", hostsOf(got), err)
	}
}

// sitesText is the managed file for the sites, as a test that does not care about options expects it.
func sitesText(all ...store.Site) string { return generated(sites.Options{}, all...) }
