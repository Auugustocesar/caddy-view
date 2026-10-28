package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/configmgr"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

func TestListManagedSitesSaysWhereTheFileIsAndWhetherItIsImported(t *testing.T) {
	rec := do(t, sitesHandler(t, newFakeSites()), http.MethodGet, "/api/managed-sites")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	got := fields(t, rec)
	if got["managedFile"] != "/etc/caddy/conf.d/caddy-view-sites.caddy" || got["imported"] != true || got["accessLog"] != true {
		t.Errorf("answer = %v, want the managed file, imported and an access log", got)
	}
}

func TestListManagedSitesWhenTheCaddyfileDoesNotImportTheFile(t *testing.T) {
	f := newFakeSites()
	f.state = configmgr.ManagedState{File: "/etc/caddy/conf.d/caddy-view-sites.caddy"}
	got := fields(t, do(t, sitesHandler(t, f), http.MethodGet, "/api/managed-sites"))
	if got["imported"] != false || got["accessLog"] != false {
		t.Errorf("answer = %v, want not imported and no access log", got)
	}
	if list, _ := got["sites"].([]any); len(list) != 2 {
		t.Errorf("sites = %v, want them listed all the same", got["sites"])
	}
}

func TestListManagedSitesHidesTheCauseWhenTheStateCannotBeRead(t *testing.T) {
	f := newFakeSites()
	f.stateErr = errors.New("open /etc/caddy/Caddyfile: permission denied")
	rec := do(t, sitesHandler(t, f), http.MethodGet, "/api/managed-sites")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "permission denied") || strings.Contains(body, "/etc/caddy") {
		t.Errorf("body %q gives away the cause", body)
	}
}

func TestAddImportRouteIsAbsentWithoutASiteManager(t *testing.T) {
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint})
	if rec := postJSON(t, h, "/api/managed-sites/import", `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestAddImportRouteOnlyAnswersPOST(t *testing.T) {
	rec := do(t, sitesHandler(t, newFakeSites()), http.MethodGet, "/api/managed-sites/import")
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
		t.Errorf("GET = %d, Allow %q, want 405 and POST", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestAddImportNeedsJSONAndTheSameOrigin(t *testing.T) {
	f := newFakeSites()
	h := sitesHandler(t, f)
	if rec := send(t, h, http.MethodPost, "/api/managed-sites/import", `{}`, nil); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("without a content type = %d, want 415", rec.Code)
	}
	foreign := map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example.com"}
	if rec := send(t, h, http.MethodPost, "/api/managed-sites/import", `{}`, foreign); rec.Code != http.StatusForbidden {
		t.Errorf("from another site = %d, want 403", rec.Code)
	}
	if f.imports != 0 {
		t.Errorf("the manager was asked to add the import %d times for a refused request", f.imports)
	}
}

func TestAddImportAnswersWithTheRevision(t *testing.T) {
	f := newFakeSites()
	rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites/import", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	rev, _ := fields(t, rec)["revision"].(map[string]any)
	if rev["number"] != float64(10) || rev["trigger"] != "add-import" || rev["status"] != "applied" {
		t.Errorf("revision = %v, want revision 10, add-import, applied", rev)
	}
	if f.imports != 1 {
		t.Errorf("the manager was asked to add the import %d times, want 1", f.imports)
	}
}

func TestAddImportTakesAnEmptyBodyToo(t *testing.T) {
	if rec := send(t, sitesHandler(t, newFakeSites()), http.MethodPost, "/api/managed-sites/import", "", jsonType); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
}

func TestAddImportRefusesAnythingInTheBody(t *testing.T) {
	f := newFakeSites()
	if rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites/import", `{"path":"/etc/passwd"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: the path is not the client's to choose", rec.Code)
	}
	if f.imports != 0 {
		t.Error("the manager was asked to add the import")
	}
}

func TestAddImportFailuresSayWhatHappened(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: x", configmgr.ErrAlreadyImported), http.StatusConflict, "already-imported"},
		{configmgr.ErrNoManagedFile, http.StatusUnprocessableEntity, "no-managed-file"},
		{fmt.Errorf("%w: %w", configmgr.ErrInvalid, &caddy.AdaptError{Message: "bad", Line: 3}), http.StatusUnprocessableEntity, "invalid"},
		{configmgr.ErrConflict, http.StatusConflict, "conflict"},
		{fmt.Errorf("%w: x", configmgr.ErrRelativeImport), http.StatusUnprocessableEntity, "relative-import"},
		{fmt.Errorf("%w: x", configmgr.ErrAdminMoved), http.StatusUnprocessableEntity, "admin-moved"},
		{fmt.Errorf("%w: x", configmgr.ErrApplyFailed), http.StatusBadGateway, "apply-failed"},
		{fmt.Errorf("%w: x", configmgr.ErrRecoveryRequired), http.StatusInternalServerError, "recovery-required"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			f := newFakeSites()
			f.importErr = tt.err
			rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites/import", `{}`)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.status, rec.Body)
			}
			if got := fields(t, rec)["code"]; got != tt.code {
				t.Errorf("code = %v, want %s", got, tt.code)
			}
			if msg, _ := fields(t, rec)["error"].(string); msg == "" {
				t.Error("the answer has no error message")
			}
		})
	}
}

func TestAddImportHidesTheCauseOfAnUnexpectedFailure(t *testing.T) {
	f := newFakeSites()
	f.importErr = fmt.Errorf("write /etc/caddy/Caddyfile: %w", store.ErrNotFound)
	rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites/import", `{}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d, want a failure", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "/etc/caddy") {
		t.Errorf("body %q gives away the cause", body)
	}
}
