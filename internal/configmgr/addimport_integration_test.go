//go:build integration

package configmgr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// startUnimportedCaddy is startManagedCaddy for a Caddyfile that does not import the managed file yet: a global options
// block and one hand-written site.
func startUnimportedCaddy(t *testing.T) *managedCaddy {
	t.Helper()
	r := newRealCaddy(t)
	m := &managedCaddy{realCaddy: r, httpPort: freePort(t), managed: filepath.Join(r.dir, "conf.d", "caddy-view-sites.caddy")}
	m.mainText = fmt.Sprintf("{\n\tadmin %s\n\tauto_https off\n\thttp_port %d\n}\n\nhttp://hand.localhost {\n\trespond \"hand-written\"\n}\n", r.adminAddr, m.httpPort)
	if err := os.WriteFile(r.path, []byte(m.mainText), 0o644); err != nil {
		t.Fatal(err)
	}
	r.start(t, r.dir)
	r.mgr = New(r.client, r.store, Options{Path: r.path, ManagedPath: m.managed})
	return m
}

func TestRealCaddyAddImportThenServesASite(t *testing.T) {
	ctx := context.Background()
	m := startUnimportedCaddy(t)

	before, err := m.mgr.ManagedState(ctx)
	if err != nil || before.Imported {
		t.Fatalf("ManagedState() = %+v, %v, want the managed file not imported", before, err)
	}
	if _, err := m.mgr.ApplySites(ctx, []store.Site{httpSite("a", "app.localhost", backend(t, "from the backend"))}); err == nil {
		t.Fatal("ApplySites() before the import succeeded: sites would be written where nobody reads them")
	}

	rev, err := m.mgr.AddImport(ctx)
	if err != nil || rev.Status != store.StatusApplied || rev.Trigger != store.TriggerAddImport {
		t.Fatalf("AddImport() = %+v, %v, want it applied as an add-import", rev, err)
	}
	text, _ := os.ReadFile(m.path)
	if !strings.Contains(string(text), "import "+filepath.ToSlash(m.managed)) {
		t.Errorf("the Caddyfile has no import of the managed file:\n%s", text)
	}
	if !strings.HasPrefix(string(text), "{\n\tadmin") {
		t.Errorf("the global options block is no longer first:\n%s", text)
	}
	if got := m.get(t, "hand.localhost"); got != "hand-written" {
		t.Errorf("the hand-written site answers %q after the import, want hand-written", got)
	}
	if after, err := m.mgr.ManagedState(ctx); err != nil || !after.Imported {
		t.Errorf("ManagedState() = %+v, %v, want imported", after, err)
	}

	if rev, err := m.mgr.ApplySites(ctx, []store.Site{httpSite("a", "app.localhost", backend(t, "from the backend"))}); err != nil || rev.Status != store.StatusApplied {
		t.Fatalf("ApplySites() after the import = %+v, %v, want it applied", rev, err)
	}
	if got := m.get(t, "app.localhost"); got != "from the backend" {
		t.Errorf("the managed site answers %q, want what the backend said", got)
	}
	if got := m.get(t, "hand.localhost"); got != "hand-written" {
		t.Errorf("the hand-written site answers %q, want hand-written", got)
	}
}

func TestRealCaddyAddImportTwiceIsRefused(t *testing.T) {
	ctx := context.Background()
	m := startUnimportedCaddy(t)
	if _, err := m.mgr.AddImport(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.mgr.AddImport(ctx); err == nil {
		t.Error("the second AddImport() succeeded, want ErrAlreadyImported")
	}
	text, _ := os.ReadFile(m.path)
	if n := strings.Count(string(text), "import "); n != 1 {
		t.Errorf("the Caddyfile has %d imports, want 1:\n%s", n, text)
	}
}
