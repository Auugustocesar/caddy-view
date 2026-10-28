package server

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/configmgr"
)

func sitesOnlyHandler(t *testing.T, mode string) (http.Handler, *fakeEditor, *fakeSites) {
	t.Helper()
	e, s := newEditor(), newFakeSites()
	s.state = configmgr.ManagedState{File: "/etc/caddy/conf.d/caddy-view-sites.caddy", Imported: true}
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{
		Endpoint: testEndpoint, Editor: e, Sites: s, BackupMode: mode, CaddyfilePath: "/etc/caddy/Caddyfile",
		Now: func() time.Time { return clock },
	})
	return h, e, s
}

func TestSitesOnlyHasNoRouteThatWritesTheCaddyfile(t *testing.T) {
	h, e, s := sitesOnlyHandler(t, BackupSitesOnly)
	for _, path := range []string{"/api/caddyfile/validate", "/api/caddyfile/format", "/api/caddyfile/apply", "/api/managed-sites/import"} {
		// The import path reads as the ID of a managed site, which answers 405 to a POST: no route either way.
		if rec := postJSON(t, h, path, `{"content":"x","baseSha256":"y"}`); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 404 or 405", path, rec.Code)
		}
	}
	if len(e.validated) != 0 || len(e.applied) != 0 || s.imports != 0 {
		t.Errorf("something was called: validated %v, applied %v, imports %d", e.validated, e.applied, s.imports)
	}
}

func TestSitesOnlyKeepsTheReadingAndTheRollbackRoutes(t *testing.T) {
	h, e, _ := sitesOnlyHandler(t, BackupSitesOnly)
	if rec := do(t, h, http.MethodGet, "/api/caddyfile"); rec.Code != http.StatusOK {
		t.Errorf("GET /api/caddyfile = %d, want 200", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/api/revisions"); rec.Code != http.StatusOK {
		t.Errorf("GET /api/revisions = %d, want 200", rec.Code)
	}
	if rec := postJSON(t, h, "/api/revisions/3/rollback", `{}`); rec.Code != http.StatusOK {
		t.Errorf("POST rollback = %d, want 200: %s", rec.Code, rec.Body)
	}
	if len(e.rolledBack) != 1 || e.rolledBack[0] != 3 {
		t.Errorf("rolled back %v, want [3]", e.rolledBack)
	}
}

func TestFullModeStillHasEveryRoute(t *testing.T) {
	for _, mode := range []string{"", BackupFull} {
		h, _, _ := sitesOnlyHandler(t, mode)
		for _, path := range []string{"/api/caddyfile/validate", "/api/caddyfile/format", "/api/caddyfile/apply", "/api/managed-sites/import"} {
			if rec := postJSON(t, h, path, `{"content":"x","baseSha256":"y"}`); rec.Code == http.StatusNotFound {
				t.Errorf("mode %q: POST %s = 404, want the route", mode, path)
			}
		}
	}
}

func TestSystemEditingSaysTheBackupModeAndTheManagedFile(t *testing.T) {
	for mode, want := range map[string]string{"": "full", BackupFull: "full", BackupSitesOnly: "sites-only"} {
		h, _, _ := sitesOnlyHandler(t, mode)
		ed := section(t, system(t, h), "editing")
		if ed["backupMode"] != want || ed["managedFile"] != "/etc/caddy/conf.d/caddy-view-sites.caddy" || ed["imported"] != true {
			t.Errorf("mode %q: editing = %v, want backupMode %q and the managed file, imported", mode, ed, want)
		}
	}
}

func TestSystemEditingWithoutManagedSitesHasNoManagedFile(t *testing.T) {
	e := newEditor()
	got := system(t, systemHandlerWith(t, &fakeCaddy{cfg: parse(t, `{}`)}, Options{Editor: e}))
	ed := section(t, got, "editing")
	if _, has := ed["managedFile"]; has || ed["backupMode"] != "full" {
		t.Errorf("editing = %v, want the mode and no managed file", ed)
	}
}

func TestSystemEditingKeepsTheRestWhenTheManagedStateCannotBeRead(t *testing.T) {
	h, _, s := sitesOnlyHandler(t, BackupFull)
	s.stateErr = errors.New("the managed file cannot be read")
	ed := section(t, system(t, h), "editing")
	if ed["enabled"] != true || ed["backupMode"] != "full" {
		t.Errorf("editing = %v, want it enabled with its mode", ed)
	}
	if _, has := ed["managedFile"]; has {
		t.Errorf("editing = %v, want no managed file", ed)
	}
}

func TestManagedSitesListSaysTheBackupMode(t *testing.T) {
	for mode, want := range map[string]string{"": "full", BackupSitesOnly: "sites-only"} {
		h, _, _ := sitesOnlyHandler(t, mode)
		var got map[string]any
		decode(t, do(t, h, http.MethodGet, "/api/managed-sites"), &got)
		if got["backupMode"] != want {
			t.Errorf("mode %q: backupMode = %v, want %q", mode, got["backupMode"], want)
		}
	}
}
