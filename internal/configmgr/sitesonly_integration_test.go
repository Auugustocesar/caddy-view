//go:build integration

package configmgr

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// TestRealCaddyInSitesOnlyModeServesSitesAndNeverTouchesTheCaddyfile runs a real Caddy through a Manager that is not
// allowed to write the Caddyfile: sites are served, the file stays byte for byte as it was, a rollback goes back to the
// earlier sites, and no revision holds the Caddyfile.
func TestRealCaddyInSitesOnlyModeServesSitesAndNeverTouchesTheCaddyfile(t *testing.T) {
	ctx := context.Background()
	m := startManagedCaddy(t)
	m.mgr = New(m.client, m.store, Options{Path: m.path, ManagedPath: m.managed, SitesOnly: true})
	before, err := os.ReadFile(m.path)
	if err != nil {
		t.Fatal(err)
	}

	first, err := m.mgr.ApplySites(ctx, []store.Site{httpSite("a", "app.localhost", backend(t, "from a"))})
	if err != nil || first.Status != store.StatusApplied {
		t.Fatalf("ApplySites() = %+v, %v, want it applied", first, err)
	}
	rows, _ := m.store.Sites(ctx)
	if _, err := m.mgr.ApplySites(ctx, append(rows, httpSite("b", "other.localhost", backend(t, "from b")))); err != nil {
		t.Fatal(err)
	}
	if got := m.get(t, "other.localhost"); got != "from b" {
		t.Errorf("the second site answers %q, want from b", got)
	}
	if got := m.get(t, "hand.localhost"); got != "hand-written" {
		t.Errorf("the hand-written site answers %q, want hand-written", got)
	}

	if _, err := m.mgr.Apply(ctx, ApplyRequest{Content: string(before) + "# x\n", BaseSHA256: digest(string(before))}); err == nil {
		t.Error("Apply() succeeded in the sites-only mode")
	}

	if _, err := m.mgr.Rollback(ctx, first.Number); err != nil {
		t.Fatal(err)
	}
	if got := m.get(t, "app.localhost"); got != "from a" {
		t.Errorf("app.localhost answers %q after the rollback, want from a", got)
	}
	if got := m.get(t, "hand.localhost"); got != "hand-written" {
		t.Errorf("the hand-written site answers %q after the rollback, want hand-written", got)
	}

	after, err := os.ReadFile(m.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the Caddyfile changed:\n%s", after)
	}
	all, err := m.store.Revisions(ctx, store.RevisionQuery{Limit: 100})
	if err != nil || len(all) == 0 {
		t.Fatalf("Revisions() = %v, %v, want the revisions", all, err)
	}
	for _, r := range all {
		if r.Content != "" || strings.Contains(r.Sites.Content, "# x") {
			t.Errorf("revision %d keeps the Caddyfile: %+v", r.Number, r)
		}
	}
}
