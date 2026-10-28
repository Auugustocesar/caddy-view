//go:build integration

package configmgr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/sites"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// A real Caddy whose Caddyfile has one hand-written site and imports the managed file by its absolute path. The sites
// use plain http on http_port, so that nothing needs a privileged port or a certificate; a request picks its site with
// the Host header.
type managedCaddy struct {
	*realCaddy
	httpPort int
	managed  string
	mainText string
}

func startManagedCaddy(t *testing.T) *managedCaddy {
	t.Helper()
	r := newRealCaddy(t)
	m := &managedCaddy{realCaddy: r, httpPort: freePort(t), managed: filepath.Join(r.dir, "conf.d", "caddy-view-sites.caddy")}
	m.mainText = fmt.Sprintf("{\n\tadmin %s\n\tauto_https off\n\thttp_port %d\n}\nimport %s/conf.d/*.caddy\nhttp://hand.localhost {\n\trespond \"hand-written\"\n}\n",
		r.adminAddr, m.httpPort, filepath.ToSlash(r.dir))
	if err := os.WriteFile(r.path, []byte(m.mainText), 0o644); err != nil {
		t.Fatal(err)
	}
	r.start(t, r.dir)
	r.mgr = New(r.client, r.store, Options{Path: r.path, ManagedPath: m.managed})
	return m
}

// get asks the site that answers to host and returns what it said.
func (m *managedCaddy) get(t *testing.T, host string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", m.httpPort), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", host, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func backend(t *testing.T, answer string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, answer) }))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// sitesText is the managed file for the sites, as a test that does not care about options expects it.
func sitesText(all ...store.Site) string { return generated(sites.Options{}, all...) }

func httpSite(id, host, upstream string) store.Site {
	return store.Site{ID: id, Hostname: host, Kind: store.SiteProxy, Upstreams: []string{upstream}, TLS: store.TLSOff, Enabled: true}
}

func TestRealCaddyServesASiteCreatedThroughTheManager(t *testing.T) {
	ctx := context.Background()
	m := startManagedCaddy(t)
	if got := m.get(t, "hand.localhost"); got != "hand-written" {
		t.Fatalf("the hand-written site answers %q before anything, want hand-written", got)
	}
	site := httpSite("a", "app.localhost", backend(t, "from the backend"))

	rev, err := m.mgr.ApplySites(ctx, []store.Site{site})
	if err != nil || rev.Status != store.StatusApplied {
		t.Fatalf("ApplySites() = %+v, %v, want it applied", rev, err)
	}
	if got := m.get(t, "app.localhost"); got != "from the backend" {
		t.Errorf("the new site answers %q, want what the backend said", got)
	}
	if got := m.get(t, "hand.localhost"); got != "hand-written" {
		t.Errorf("the hand-written site answers %q after the change, want hand-written", got)
	}
	if rows, _ := m.store.Sites(ctx); len(rows) != 1 || rows[0].Hostname != "app.localhost" {
		t.Errorf("stored sites = %+v, want app.localhost", rows)
	}
	if d, err := m.mgr.Reconcile(ctx); err != nil || d.Running != RunMatches {
		t.Errorf("Reconcile() = %+v, %v, want Caddy to run the file", d, err)
	}

	// Taking the site away stops it being served.
	if _, err := m.mgr.ApplySites(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := m.get(t, "app.localhost"); got == "from the backend" {
		t.Error("the site is still served after it was removed")
	}
	if got := m.get(t, "hand.localhost"); got != "hand-written" {
		t.Errorf("the hand-written site answers %q, want hand-written", got)
	}
}

func TestRealCaddyRefusesAHostnameTheCaddyfileAlreadyServes(t *testing.T) {
	ctx := context.Background()
	m := startManagedCaddy(t)
	clash := httpSite("a", "hand.localhost", backend(t, "from the backend"))

	rev, err := m.mgr.ApplySites(ctx, []store.Site{clash})
	var siteErr *SiteError
	if !errors.Is(err, ErrSiteRejected) || !errors.As(err, &siteErr) || siteErr.SiteID != "a" {
		t.Fatalf("ApplySites() error = %v, want ErrSiteRejected naming the site a", err)
	}
	t.Logf("Caddy said (line %d): %s", siteErr.Line, siteErr.Message)
	if rev.Status != store.StatusFailed || rev.Error == "" {
		t.Errorf("revision = %+v, want failed with the reason", rev)
	}
	if got := m.get(t, "hand.localhost"); got != "hand-written" {
		t.Errorf("the hand-written site answers %q, want it untouched", got)
	}
	if _, err := os.Stat(m.managed); err == nil {
		t.Error("the managed file is still there")
	}
	if rows, _ := m.store.Sites(ctx); len(rows) != 0 {
		t.Errorf("stored sites = %+v, want none", rows)
	}
	m.reachable(t)
}

func TestRealCaddyRollsBackBothFiles(t *testing.T) {
	ctx := context.Background()
	m := startManagedCaddy(t)
	one := httpSite("a", "one.localhost", backend(t, "one"))
	two := httpSite("b", "two.localhost", backend(t, "two"))

	first, err := m.mgr.ApplySites(ctx, []store.Site{one})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := m.store.Sites(ctx)
	if _, err := m.mgr.ApplySites(ctx, append(rows, two)); err != nil {
		t.Fatal(err)
	}
	if m.get(t, "one.localhost") != "one" || m.get(t, "two.localhost") != "two" {
		t.Fatal("both sites should be served before the rollback")
	}

	if _, err := m.mgr.Rollback(ctx, first.Number); err != nil {
		t.Fatal(err)
	}
	if got := m.get(t, "one.localhost"); got != "one" {
		t.Errorf("one.localhost answers %q after the rollback, want one", got)
	}
	if got := m.get(t, "two.localhost"); got == "two" {
		t.Error("two.localhost is still served after the rollback")
	}
	if text, _ := os.ReadFile(m.managed); string(text) != first.Sites.Content {
		t.Errorf("the managed file = %q, want the one of the first revision", text)
	}
	if rows, _ := m.store.Sites(ctx); len(rows) != 1 || rows[0].Hostname != "one.localhost" {
		t.Errorf("stored sites = %+v, want only one.localhost", rows)
	}
}

func TestRealCaddyRecoversASitesApplyThatWasCutShort(t *testing.T) {
	ctx := context.Background()
	m := startManagedCaddy(t)
	site := httpSite("a", "app.localhost", backend(t, "from the backend"))
	text := sitesText(site)

	// The process died after it wrote the file and Caddy loaded it, before it could say so.
	if err := os.MkdirAll(filepath.Dir(m.managed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.managed, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.client.Load(ctx, m.mainText); err != nil {
		t.Fatal(err)
	}
	rev, err := m.store.AddRevision(ctx, store.Revision{
		Time: time.Now(), Trigger: store.TriggerSiteChange, Status: store.StatusLoading, Content: m.mainText, SHA256: sum(m.mainText),
		Sites: &store.RevisionSites{Content: text, SHA256: sum(text), Snapshot: []store.Site{site}},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := m.mgr.Recover(ctx)
	if err != nil || len(got) != 1 || got[0].Revision != rev.Number || got[0].Status != store.StatusApplied {
		t.Fatalf("Recover() = %+v, %v, want the revision applied", got, err)
	}
	if rows, _ := m.store.Sites(ctx); len(rows) != 1 || rows[0].Hostname != "app.localhost" {
		t.Errorf("stored sites = %+v, want app.localhost, set from the revision", rows)
	}
}
