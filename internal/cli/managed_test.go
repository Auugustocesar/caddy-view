package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// managedCaddyfile writes a Caddyfile that imports the managed file at <dir>/<sub>/*.caddy by its absolute path.
func managedCaddyfile(t *testing.T, sub string) (caddyfile, dir string) {
	t.Helper()
	dir = t.TempDir()
	caddyfile = filepath.Join(dir, "Caddyfile")
	text := "import " + filepath.ToSlash(dir) + "/" + sub + "/*.caddy\n" + siteOne
	if err := os.WriteFile(caddyfile, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return caddyfile, dir
}

const appSite = `{"hostname":"app.example.com","kind":"proxy","upstreams":["10.0.0.5:8080"],"tls":"off"}`

func readFile(t *testing.T, path string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

func sitesOf(t *testing.T, a api) []any {
	t.Helper()
	status, got := a.call(http.MethodGet, "/api/managed-sites", "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/managed-sites = %d %v", status, got)
	}
	list, _ := got["sites"].([]any)
	return list
}

func TestServeManagesSitesThroughTheAPI(t *testing.T) {
	admin := newEditAdmin(t)
	file, dir := managedCaddyfile(t, "conf.d")
	managed := filepath.Join(dir, "conf.d", "caddy-view-sites.caddy") // the default
	args := []string{"--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir()}

	base, stop, _, _ := startServe(t, args...)
	a := api{t, base}

	status, got := a.call(http.MethodPost, "/api/managed-sites", appSite)
	if status != http.StatusOK {
		t.Fatalf("create = %d %v, want 200", status, got)
	}
	site, _ := got["site"].(map[string]any)
	if id, _ := site["id"].(string); site["hostname"] != "app.example.com" || len(id) != 32 {
		t.Errorf("site = %v, want the new site with an ID", site)
	}
	text, ok := readFile(t, managed)
	if !ok || !strings.Contains(text, "http://app.example.com {") || !strings.Contains(text, "reverse_proxy 10.0.0.5:8080") {
		t.Errorf("managed file = %q (exists: %v), want the site written", text, ok)
	}
	if loads := admin.loaded(); len(loads) != 1 || !strings.HasPrefix(loads[0], "import") {
		t.Errorf("caddy loaded %d configurations, want the Caddyfile once", len(loads))
	}
	if list := sitesOf(t, a); len(list) != 1 {
		t.Errorf("GET /api/managed-sites lists %d sites, want 1", len(list))
	}

	// Another process sees it too: the sites are in the database, not in memory.
	if code := stop(); code != 0 {
		t.Fatalf("serve exited with %d", code)
	}
	base, stop, _, _ = startServe(t, args...)
	defer stop()
	a = api{t, base}
	list := sitesOf(t, a)
	if len(list) != 1 {
		t.Fatalf("after a restart GET /api/managed-sites lists %d sites, want 1", len(list))
	}

	first, _ := list[0].(map[string]any)
	id, _ := first["id"].(string)
	if status, got := a.call(http.MethodDelete, "/api/managed-sites/"+id, ""); status != http.StatusOK {
		t.Fatalf("delete = %d %v, want 200", status, got)
	}
	if text, _ := readFile(t, managed); strings.Contains(text, "app.example.com") {
		t.Errorf("managed file = %q, want the site gone", text)
	}
	if list := sitesOf(t, a); len(list) != 0 {
		t.Errorf("GET /api/managed-sites lists %d sites after the delete, want none", len(list))
	}
}

func TestServeUpdatesAManagedSite(t *testing.T) {
	admin := newEditAdmin(t)
	file, dir := managedCaddyfile(t, "conf.d")
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir())
	defer stop()
	a := api{t, base}
	_, got := a.call(http.MethodPost, "/api/managed-sites", appSite)
	id := siteID(t, got)

	status, got := a.call(http.MethodPut, "/api/managed-sites/"+id,
		`{"hostname":"app.example.com","kind":"proxy","upstreams":["10.0.0.9:9090"],"tls":"off"}`)
	if status != http.StatusOK {
		t.Fatalf("update = %d %v, want 200", status, got)
	}
	text, _ := readFile(t, filepath.Join(dir, "conf.d", "caddy-view-sites.caddy"))
	if !strings.Contains(text, "10.0.0.9:9090") || strings.Contains(text, "10.0.0.5:8080") {
		t.Errorf("managed file = %q, want the new upstream only", text)
	}
}

func TestServeNeedsTheCaddyfileToImportTheManagedFile(t *testing.T) {
	admin := newEditAdmin(t)
	file := writeCaddyfile(t, siteOne) // no import
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir())
	defer stop()

	status, got := api{t, base}.call(http.MethodPost, "/api/managed-sites", appSite)
	if status != http.StatusUnprocessableEntity || got["code"] != "not-imported" {
		t.Errorf("create = %d %v, want 422 not-imported", status, got)
	}
	if loads := admin.loaded(); len(loads) != 0 {
		t.Errorf("caddy loaded %d configurations, want none", len(loads))
	}
	if _, ok := readFile(t, filepath.Join(filepath.Dir(file), "conf.d", "caddy-view-sites.caddy")); ok {
		t.Error("a managed file was written")
	}
}

func TestServeManagedFileFlag(t *testing.T) {
	t.Run("relative to the directory of the Caddyfile", func(t *testing.T) {
		admin := newEditAdmin(t)
		file, dir := managedCaddyfile(t, "sites")
		base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir(),
			"--managed-file", "sites/mine.caddy")
		defer stop()
		if status, got := (api{t, base}).call(http.MethodPost, "/api/managed-sites", appSite); status != http.StatusOK {
			t.Fatalf("create = %d %v", status, got)
		}
		if text, ok := readFile(t, filepath.Join(dir, "sites", "mine.caddy")); !ok || !strings.Contains(text, "app.example.com") {
			t.Errorf("sites/mine.caddy = %q (exists: %v), want the site", text, ok)
		}
	})
	t.Run("an absolute path", func(t *testing.T) {
		admin := newEditAdmin(t)
		file, dir := managedCaddyfile(t, "elsewhere")
		abs := filepath.Join(dir, "elsewhere", "all.caddy")
		base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir(),
			"--managed-file", abs)
		defer stop()
		if status, got := (api{t, base}).call(http.MethodPost, "/api/managed-sites", appSite); status != http.StatusOK {
			t.Fatalf("create = %d %v", status, got)
		}
		if _, ok := readFile(t, abs); !ok {
			t.Errorf("%s was not written", abs)
		}
	})
}

func TestServeAccessLogOfASite(t *testing.T) {
	withLog := `{"hostname":"app.example.com","kind":"proxy","upstreams":["10.0.0.5:8080"],"tls":"off","accessLog":true}`

	t.Run("found in the running Caddy", func(t *testing.T) {
		admin := newEditAdmin(t) // its configuration logs to /var/log/caddy/access.log
		file, dir := managedCaddyfile(t, "conf.d")
		base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir())
		defer stop()
		if status, got := (api{t, base}).call(http.MethodPost, "/api/managed-sites", withLog); status != http.StatusOK {
			t.Fatalf("create = %d %v", status, got)
		}
		text, _ := readFile(t, filepath.Join(dir, "conf.d", "caddy-view-sites.caddy"))
		if !strings.Contains(text, "output file /var/log/caddy/access.log") || !strings.Contains(text, "import caddy-view-access-log") {
			t.Errorf("managed file = %q, want the access log of the running Caddy", text)
		}
	})

	t.Run("given by hand", func(t *testing.T) {
		admin := newEditAdmin(t)
		file, dir := managedCaddyfile(t, "conf.d")
		base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir(),
			"--access-log", "/srv/logs/mine.log")
		defer stop()
		if status, got := (api{t, base}).call(http.MethodPost, "/api/managed-sites", withLog); status != http.StatusOK {
			t.Fatalf("create = %d %v", status, got)
		}
		if text, _ := readFile(t, filepath.Join(dir, "conf.d", "caddy-view-sites.caddy")); !strings.Contains(text, "output file /srv/logs/mine.log") {
			t.Errorf("managed file = %q, want the access log that was given", text)
		}
	})

	t.Run("nobody knows it", func(t *testing.T) {
		admin := newEditAdmin(t)
		admin.running = `{}` // a Caddy with no logging at all
		file, _ := managedCaddyfile(t, "conf.d")
		base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir())
		defer stop()
		status, got := (api{t, base}).call(http.MethodPost, "/api/managed-sites", withLog)
		if status != http.StatusUnprocessableEntity || got["code"] != "invalid-site" {
			t.Errorf("create = %d %v, want 422 invalid-site: there is no access log to write to", status, got)
		}
	})
}

func TestServeLeavesManagedSitesAloneWithoutAllowEdit(t *testing.T) {
	admin := newEditAdmin(t)
	file, _ := managedCaddyfile(t, "conf.d")
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file)
	defer stop()
	if status, _ := (api{t, base}).call(http.MethodGet, "/api/managed-sites", ""); status != http.StatusNotFound {
		t.Errorf("GET /api/managed-sites = %d, want 404 without --allow-edit", status)
	}
}

// siteID returns the ID of the site in an answer, or stops the test when there is none.
func siteID(t *testing.T, answer map[string]any) string {
	t.Helper()
	site, _ := answer["site"].(map[string]any)
	id, _ := site["id"].(string)
	if id == "" {
		t.Fatalf("the answer %v has no site with an ID", answer)
	}
	return id
}
