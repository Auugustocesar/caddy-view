package cli

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestServeRefusesABadBackupMode(t *testing.T) {
	admin := newEditAdmin(t)
	file, _ := managedCaddyfile(t, "conf.d")
	base := []string{"serve", "--listen", "127.0.0.1:0", "--admin", admin.URL, "--data-dir", t.TempDir()}
	tests := []struct {
		name string
		args []string
	}{
		{"an unknown mode", []string{"--allow-edit", "--caddyfile", file, "--backup-mode", "partial"}},
		{"sites-only without editing", []string{"--backup-mode", "sites-only"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stderr := runBriefly(t, append(append([]string(nil), base...), tt.args...)...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2\nstderr: %s", code, stderr)
			}
			if !strings.Contains(stderr, "--backup-mode") {
				t.Errorf("stderr %q does not name --backup-mode", stderr)
			}
		})
	}
}

func TestServeInSitesOnlyModeNeverWritesNorKeepsTheCaddyfile(t *testing.T) {
	admin := newEditAdmin(t)
	file, dir := managedCaddyfile(t, "conf.d")
	before, _ := readFile(t, file)
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir(),
		"--backup-mode", "sites-only")
	defer stop()
	a := api{t, base}

	if status, got := a.call(http.MethodPost, "/api/managed-sites", appSite); status != http.StatusOK {
		t.Fatalf("create = %d %v", status, got)
	}
	if text, ok := readFile(t, dir+"/conf.d/caddy-view-sites.caddy"); !ok || !strings.Contains(text, "app.example.com") {
		t.Errorf("the managed file = %q (exists: %v), want the site", text, ok)
	}
	if after, _ := readFile(t, file); after != before {
		t.Errorf("the Caddyfile became %q", after)
	}
	if got := admin.loaded(); len(got) != 1 || got[0] != before {
		t.Errorf("Caddy was given %q, want the Caddyfile as it is, once", got)
	}

	if status, _ := a.call(http.MethodPost, "/api/caddyfile/apply", `{"content":"x","baseSha256":"y"}`); status != http.StatusNotFound {
		t.Errorf("POST /api/caddyfile/apply = %d, want 404", status)
	}
	_, rev := a.call(http.MethodGet, "/api/revisions/1", "")
	if c, has := rev["content"]; has && c != "" {
		t.Errorf("revision 1 holds the Caddyfile: %v", c)
	}
	_, sys := a.call(http.MethodGet, "/api/system", "")
	if ed, _ := sys["editing"].(map[string]any); ed["backupMode"] != "sites-only" {
		t.Errorf("editing = %v, want backupMode sites-only", ed)
	}
	if _, err := os.Stat(file); err != nil {
		t.Error(err)
	}
}

func TestServeDefaultsToTheFullBackupMode(t *testing.T) {
	admin := newEditAdmin(t)
	file, _ := managedCaddyfile(t, "conf.d")
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir())
	defer stop()
	_, sys := (api{t, base}).call(http.MethodGet, "/api/system", "")
	if ed, _ := sys["editing"].(map[string]any); ed["backupMode"] != "full" {
		t.Errorf("editing = %v, want backupMode full", ed)
	}
}
