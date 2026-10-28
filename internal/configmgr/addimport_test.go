package configmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/caddyfile"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// newUnimportedEnv is a managed env whose Caddyfile does not import the managed file yet.
func newUnimportedEnv(t *testing.T) *menv {
	t.Helper()
	m := newManagedEnv(t)
	m.main = original
	if err := os.WriteFile(m.path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	return m
}

func (m *menv) importLine() string { return "import " + filepath.ToSlash(m.managed) }

func TestManagedStateSaysWhereTheFileIsAndWhetherItIsImported(t *testing.T) {
	ctx := context.Background()

	t.Run("imported", func(t *testing.T) {
		m := newManagedEnv(t)
		got, err := m.mgr.ManagedState(ctx)
		if err != nil || got.File != m.managed || !got.Imported || got.AccessLog {
			t.Errorf("ManagedState() = %+v, %v, want the file, imported, no access log", got, err)
		}
	})
	t.Run("not imported", func(t *testing.T) {
		m := newUnimportedEnv(t)
		got, err := m.mgr.ManagedState(ctx)
		if err != nil || got.File != m.managed || got.Imported {
			t.Errorf("ManagedState() = %+v, %v, want the file, not imported", got, err)
		}
	})
	t.Run("an access log is known", func(t *testing.T) {
		m := newManagedEnv(t)
		m.accessLog = "/var/log/caddy/access.log"
		if got, err := m.mgr.ManagedState(ctx); err != nil || !got.AccessLog {
			t.Errorf("ManagedState() = %+v, %v, want an access log", got, err)
		}
	})
	t.Run("no managed file configured", func(t *testing.T) {
		e := newEnv(t, original)
		if _, err := e.mgr.ManagedState(ctx); !errors.Is(err, ErrNoManagedFile) {
			t.Errorf("ManagedState() error = %v, want ErrNoManagedFile", err)
		}
	})
}

func TestAddImportAddsItToTheCaddyfileAndLoadsIt(t *testing.T) {
	ctx := context.Background()
	m := newUnimportedEnv(t)

	rev, err := m.mgr.AddImport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want, err := caddyfile.AddImport(original, filepath.ToSlash(m.managed))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.disk(t); got != want {
		t.Errorf("the Caddyfile = %q, want %q", got, want)
	}
	if !strings.Contains(m.disk(t), m.importLine()) {
		t.Errorf("the Caddyfile has no %q", m.importLine())
	}
	if got := m.caddy.loaded(); !slices.Equal(got, []string{want}) {
		t.Errorf("loads = %q, want the new Caddyfile once", got)
	}
	if rev.Status != store.StatusApplied || rev.Trigger != store.TriggerAddImport || rev.Content != want {
		t.Errorf("revision = %+v, want applied, with the trigger add-import and the new text", rev)
	}
	if state, err := m.mgr.ManagedState(ctx); err != nil || !state.Imported {
		t.Errorf("ManagedState() = %+v, %v, want imported now", state, err)
	}
}

func TestAddImportCreatesTheManagedFileEmptyBecauseCaddyCannotImportAMissingOne(t *testing.T) {
	m := newUnimportedEnv(t)
	if _, ok := m.managedText(t); ok {
		t.Fatal("the managed file should not exist yet")
	}
	if _, err := m.mgr.AddImport(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, ok := m.managedText(t); !ok || got != "" {
		t.Errorf("managed file = %q, %v, want it to exist and be empty", got, ok)
	}
	// An empty managed file is one the sites may be written to.
	if _, err := m.mgr.ApplySites(context.Background(), []store.Site{mkSite("a", "a.example.com", "10.0.0.5")}); err != nil {
		t.Errorf("ApplySites() after the import: %v", err)
	}
}

func TestAddImportLeavesAnExistingManagedFileAlone(t *testing.T) {
	m := newUnimportedEnv(t)
	m.writeManaged(t, "# written before\n")
	if _, err := m.mgr.AddImport(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.managedText(t); got != "# written before\n" {
		t.Errorf("managed file = %q, want it untouched", got)
	}
}

func TestAddImportRefusesACaddyfileThatImportsAlready(t *testing.T) {
	m := newManagedEnv(t)
	_, err := m.mgr.AddImport(context.Background())
	if !errors.Is(err, ErrAlreadyImported) {
		t.Fatalf("AddImport() error = %v, want ErrAlreadyImported", err)
	}
	if got := m.disk(t); got != m.main {
		t.Errorf("the Caddyfile became %q", got)
	}
	if len(m.caddy.loaded()) != 0 {
		t.Error("Caddy was asked to load something")
	}
	m.noRevision(t)
}

func TestAddImportNeedsAManagedFile(t *testing.T) {
	e := newEnv(t, original)
	if _, err := e.mgr.AddImport(context.Background()); !errors.Is(err, ErrNoManagedFile) {
		t.Errorf("AddImport() error = %v, want ErrNoManagedFile", err)
	}
}

func TestAddImportThatCaddyRefusesChangesNothing(t *testing.T) {
	m := newUnimportedEnv(t)
	m.caddy.adaptFn = func(content string) error {
		if strings.Contains(content, m.importLine()) {
			return &caddy.AdaptError{Message: "unrecognized directive", Line: 1}
		}
		return nil
	}
	_, err := m.mgr.AddImport(context.Background())
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("AddImport() error = %v, want ErrInvalid", err)
	}
	if got := m.disk(t); got != original {
		t.Errorf("the Caddyfile became %q", got)
	}
	if _, ok := m.managedText(t); ok {
		t.Error("the managed file it created is still there")
	}
}

func TestAddImportThatFailsToLoadPutsEverythingBack(t *testing.T) {
	m := newUnimportedEnv(t)
	m.caddy.loadFn = func(_ context.Context, call int, _ string) error {
		if call == 1 {
			return errBoom
		}
		return nil
	}
	_, err := m.mgr.AddImport(context.Background())
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("AddImport() error = %v, want ErrApplyFailed", err)
	}
	if got := m.disk(t); got != original {
		t.Errorf("the Caddyfile = %q, want the original put back", got)
	}
	if _, ok := m.managedText(t); ok {
		t.Error("the managed file it created is still there")
	}
}

func TestAddImportKeepsAManagedFileThatWasThereBeforeWhenItFails(t *testing.T) {
	m := newUnimportedEnv(t)
	m.writeManaged(t, "")
	m.caddy.adaptErr = errBoom
	if _, err := m.mgr.AddImport(context.Background()); err == nil {
		t.Fatal("expected a failure")
	}
	if _, ok := m.managedText(t); !ok {
		t.Error("a managed file that existed before was removed")
	}
}

func TestAddImportThenSitesAreServedFromTheManagedFile(t *testing.T) {
	ctx := context.Background()
	m := newUnimportedEnv(t)
	if _, err := m.mgr.AddImport(ctx); err != nil {
		t.Fatal(err)
	}
	rev, err := m.mgr.ApplySites(ctx, []store.Site{mkSite("a", "a.example.com", "10.0.0.5")})
	if err != nil || rev.Status != store.StatusApplied {
		t.Fatalf("ApplySites() = %+v, %v, want it applied", rev, err)
	}
	if got, _ := m.managedText(t); !strings.Contains(got, "a.example.com") {
		t.Errorf("managed file = %q, want the site in it", got)
	}
}
