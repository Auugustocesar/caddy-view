package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/configmgr"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// fakeSites answers the managed sites calls from rows it keeps, and remembers what it was asked to apply.
type fakeSites struct {
	rows       []store.Site
	listErr    error
	previewErr error
	applyErr   error
	applyRev   store.Revision

	state     configmgr.ManagedState
	stateErr  error
	importRev store.Revision
	importErr error

	previewed [][]store.Site
	applied   [][]store.Site
	imports   int
}

func (f *fakeSites) ManagedState(context.Context) (configmgr.ManagedState, error) {
	return f.state, f.stateErr
}

func (f *fakeSites) AddImport(context.Context) (store.Revision, error) {
	f.imports++
	return f.importRev, f.importErr
}

func (f *fakeSites) Sites(context.Context) ([]store.Site, error) {
	return slices.Clone(f.rows), f.listErr
}

func (f *fakeSites) PreviewSites(_ context.Context, desired []store.Site) (string, error) {
	f.previewed = append(f.previewed, slices.Clone(desired))
	if f.previewErr != nil {
		return "", f.previewErr
	}
	return "# generated for " + fmt.Sprint(len(desired)) + " sites\n", nil
}

func (f *fakeSites) ApplySites(_ context.Context, desired []store.Site) (store.Revision, error) {
	f.applied = append(f.applied, slices.Clone(desired))
	if f.applyErr != nil {
		return f.applyRev, f.applyErr
	}
	f.rows = slices.Clone(desired)
	for i := range f.rows {
		if f.rows[i].CreatedAt.IsZero() {
			f.rows[i].CreatedAt = clock
		}
		f.rows[i].UpdatedAt = clock
	}
	return f.applyRev, nil
}

func newFakeSites() *fakeSites {
	return &fakeSites{
		rows: []store.Site{
			{
				ID: "11111111111111111111111111111111", Hostname: "a.example.com", Kind: store.SiteProxy, Upstreams: []string{"10.0.0.5:8080"},
				TLS: store.TLSAutomatic, Enabled: true, CreatedAt: clock.Add(-time.Hour), UpdatedAt: clock.Add(-time.Minute),
			},
			{
				ID: "22222222222222222222222222222222", Hostname: "b.example.com", Kind: store.SiteRedirect, RedirectTarget: "https://x.example.com{uri}",
				RedirectCode: 308, TLS: store.TLSOff, Enabled: false, CreatedAt: clock.Add(-2 * time.Hour), UpdatedAt: clock.Add(-2 * time.Hour),
			},
		},
		applyRev:  store.Revision{Number: 9, Time: clock, Trigger: store.TriggerSiteChange, Status: store.StatusApplied, SHA256: digestA},
		state:     configmgr.ManagedState{File: "/etc/caddy/conf.d/caddy-view-sites.caddy", Imported: true, AccessLog: true},
		importRev: store.Revision{Number: 10, Time: clock, Trigger: store.TriggerAddImport, Status: store.StatusApplied, SHA256: digestA},
	}
}

func sitesHandler(t *testing.T, f *fakeSites) http.Handler {
	t.Helper()
	return New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint, Sites: f, Now: func() time.Time { return clock }})
}

func put(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, h, http.MethodPut, path, body, jsonType)
}

func del(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, h, http.MethodDelete, path, "", jsonType)
}

const (
	idA     = "11111111111111111111111111111111"
	idB     = "22222222222222222222222222222222"
	newSite = `{"hostname":"c.example.com","kind":"proxy","upstreams":["10.0.0.7:9000"],"tls":"internal","accessLog":true}`
)

func TestManagedSitesRoutesAreAbsentWithoutASiteManager(t *testing.T) {
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint})
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/managed-sites"},
		{http.MethodPost, "/api/managed-sites"},
		{http.MethodPost, "/api/managed-sites/preview"},
		{http.MethodPut, "/api/managed-sites/" + idA},
		{http.MethodDelete, "/api/managed-sites/" + idA},
	} {
		if rec := send(t, h, c.method, c.path, "{}", jsonType); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, rec.Code)
		}
	}
}

func TestManagedSitesRoutesOnlyAnswerTheirMethods(t *testing.T) {
	h := sitesHandler(t, newFakeSites())
	for _, c := range []struct{ method, path, allow string }{
		{http.MethodPut, "/api/managed-sites", "GET, POST"},
		{http.MethodDelete, "/api/managed-sites", "GET, POST"},
		{http.MethodGet, "/api/managed-sites/preview", "POST"},
		{http.MethodGet, "/api/managed-sites/" + idA, "PUT, DELETE"},
		{http.MethodPost, "/api/managed-sites/" + idA, "PUT, DELETE"},
	} {
		rec := send(t, h, c.method, c.path, "{}", jsonType)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != c.allow {
			t.Errorf("%s %s = %d, Allow %q; want 405, Allow %q", c.method, c.path, rec.Code, rec.Header().Get("Allow"), c.allow)
		}
	}
}

func TestManagedSitesChangesNeedJSONAndTheSameOrigin(t *testing.T) {
	f := newFakeSites()
	h := sitesHandler(t, f)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/managed-sites"},
		{http.MethodPost, "/api/managed-sites/preview"},
		{http.MethodPut, "/api/managed-sites/" + idA},
		{http.MethodDelete, "/api/managed-sites/" + idA},
	} {
		if rec := send(t, h, c.method, c.path, newSite, nil); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s %s without a content type = %d, want 415", c.method, c.path, rec.Code)
		}
		foreign := map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example.com"}
		if rec := send(t, h, c.method, c.path, newSite, foreign); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s from another site = %d, want 403", c.method, c.path, rec.Code)
		}
	}
	if len(f.applied) != 0 || len(f.previewed) != 0 {
		t.Errorf("the site manager was called for a refused request (apply %d, preview %d)", len(f.applied), len(f.previewed))
	}
}

func TestListManagedSites(t *testing.T) {
	rec := do(t, sitesHandler(t, newFakeSites()), http.MethodGet, "/api/managed-sites")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	list, _ := fields(t, rec)["sites"].([]any)
	if len(list) != 2 {
		t.Fatalf("sites = %v, want two", list)
	}
	a, _ := list[0].(map[string]any)
	want := map[string]any{
		"id": idA, "hostname": "a.example.com", "kind": "proxy", "tls": "automatic", "enabled": true, "accessLog": false,
		"skipTlsVerify": false, "redirectCode": float64(0), "redirectTarget": "",
		"createdAt": clock.Add(-time.Hour).Format(time.RFC3339Nano), "updatedAt": clock.Add(-time.Minute).Format(time.RFC3339Nano),
	}
	for k, v := range want {
		if a[k] != v {
			t.Errorf("sites[0][%q] = %v, want %v", k, a[k], v)
		}
	}
	if ups, _ := a["upstreams"].([]any); len(ups) != 1 || ups[0] != "10.0.0.5:8080" {
		t.Errorf("sites[0].upstreams = %v, want [10.0.0.5:8080]", a["upstreams"])
	}
	b, _ := list[1].(map[string]any)
	if ups, ok := b["upstreams"].([]any); !ok || len(ups) != 0 {
		t.Errorf("a redirect has upstreams %v, want an empty list", b["upstreams"])
	}
	if b["redirectCode"] != float64(308) || b["enabled"] != false {
		t.Errorf("sites[1] = %v, want redirect code 308 and disabled", b)
	}
}

func TestListManagedSitesWhenThereAreNone(t *testing.T) {
	f := newFakeSites()
	f.rows = nil
	rec := do(t, sitesHandler(t, f), http.MethodGet, "/api/managed-sites")
	if list, ok := fields(t, rec)["sites"].([]any); !ok || len(list) != 0 {
		t.Errorf("sites = %v, want an empty list", fields(t, rec)["sites"])
	}
}

func TestListManagedSitesFailureHidesTheCause(t *testing.T) {
	f := newFakeSites()
	f.listErr = errors.New("disk /dev/sda1 is full")
	rec := do(t, sitesHandler(t, f), http.MethodGet, "/api/managed-sites")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "sda1") {
		t.Errorf("status = %d, body %s; want a 500 without the cause", rec.Code, rec.Body)
	}
}

func TestCreateAManagedSite(t *testing.T) {
	f := newFakeSites()
	rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites", newSite)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if len(f.applied) != 1 || len(f.applied[0]) != 3 {
		t.Fatalf("applied = %+v, want the two sites and the new one", f.applied)
	}
	added := f.applied[0][2]
	if len(added.ID) != 32 || strings.Trim(added.ID, "0123456789abcdef") != "" {
		t.Errorf("the new site got the ID %q, want 32 hexadecimal characters", added.ID)
	}
	if added.Hostname != "c.example.com" || added.Kind != store.SiteProxy || !slices.Equal(added.Upstreams, []string{"10.0.0.7:9000"}) ||
		added.TLS != store.TLSInternal || !added.AccessLog || !added.Enabled {
		t.Errorf("the new site = %+v, want what was asked, and enabled by default", added)
	}
	if !slices.Equal(hostnamesOf(f.applied[0][:2]), []string{"a.example.com", "b.example.com"}) {
		t.Errorf("the other sites were changed: %v", hostnamesOf(f.applied[0][:2]))
	}

	got := fields(t, rec)
	site, _ := got["site"].(map[string]any)
	if site["id"] != added.ID || site["hostname"] != "c.example.com" || site["createdAt"] != clock.Format(time.RFC3339Nano) {
		t.Errorf("site = %v, want the site as saved, with its times", site)
	}
	if rev, _ := got["revision"].(map[string]any); rev["number"] != float64(9) || rev["status"] != "applied" {
		t.Errorf("revision = %v, want the applied revision", rev)
	}
}

func TestCreateAManagedSiteDisabled(t *testing.T) {
	f := newFakeSites()
	body := `{"hostname":"c.example.com","kind":"redirect","redirectTarget":"https://x.example.com","redirectCode":302,"enabled":false}`
	if rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites", body); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	added := f.applied[0][2]
	if added.Enabled || added.Kind != store.SiteRedirect || added.RedirectTarget != "https://x.example.com" || added.RedirectCode != 302 {
		t.Errorf("the new site = %+v, want a disabled redirect", added)
	}
}

func TestManagedSiteBodies(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{"create: not JSON", http.MethodPost, "/api/managed-sites", `nope`, 400},
		{"create: an unknown field", http.MethodPost, "/api/managed-sites", `{"hostname":"c.example.com","kind":"proxy","upstreams":["x"],"cert":"mine"}`, 400},
		{"create: the id cannot be chosen", http.MethodPost, "/api/managed-sites", `{"id":"abc","hostname":"c.example.com","kind":"proxy","upstreams":["x"]}`, 400},
		{"create: a field of the wrong type", http.MethodPost, "/api/managed-sites", `{"hostname":"c.example.com","kind":"proxy","upstreams":"10.0.0.7"}`, 400},
		{"create: too large", http.MethodPost, "/api/managed-sites", `{"hostname":"` + strings.Repeat("a", 1<<20+1) + `"}`, 413},
		{"update: not JSON", http.MethodPut, "/api/managed-sites/" + idA, `nope`, 400},
		{"preview: not JSON", http.MethodPost, "/api/managed-sites/preview", `nope`, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeSites()
			rec := send(t, sitesHandler(t, f), tt.method, tt.path, tt.body, jsonType)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d: %.200s", rec.Code, tt.status, rec.Body)
			}
			if msg, _ := fields(t, rec)["error"].(string); msg == "" {
				t.Error("the answer has no error message")
			}
			if len(f.applied) != 0 || len(f.previewed) != 0 {
				t.Error("the site manager was called for a malformed request")
			}
		})
	}
}

func TestUpdateAManagedSite(t *testing.T) {
	f := newFakeSites()
	body := `{"hostname":"a2.example.com","kind":"proxy","upstreams":["10.0.0.9:80","https://10.0.0.10"],"skipTlsVerify":true,"tls":"off","enabled":false}`
	rec := put(t, sitesHandler(t, f), "/api/managed-sites/"+idA, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if len(f.applied) != 1 || len(f.applied[0]) != 2 {
		t.Fatalf("applied = %+v, want the two sites", f.applied)
	}
	changed, other := f.applied[0][0], f.applied[0][1]
	if changed.ID != idA || changed.Hostname != "a2.example.com" || !slices.Equal(changed.Upstreams, []string{"10.0.0.9:80", "https://10.0.0.10"}) ||
		!changed.SkipTLSVerify || changed.TLS != store.TLSOff || changed.Enabled {
		t.Errorf("the changed site = %+v, want the new fields under the same ID", changed)
	}
	if !changed.CreatedAt.Equal(clock.Add(-time.Hour)) {
		t.Errorf("the creation time became %v, want it kept", changed.CreatedAt)
	}
	if other.ID != idB || other.Hostname != "b.example.com" {
		t.Errorf("the other site = %+v, want it untouched", other)
	}
	site, _ := fields(t, rec)["site"].(map[string]any)
	if site["id"] != idA || site["hostname"] != "a2.example.com" {
		t.Errorf("site = %v, want the changed site", site)
	}
}

func TestUpdateOrDeleteAnUnknownManagedSite(t *testing.T) {
	f := newFakeSites()
	h := sitesHandler(t, f)
	for _, id := range []string{"33333333333333333333333333333333", "xyz", "../etc"} {
		for _, rec := range []*httptest.ResponseRecorder{
			put(t, h, "/api/managed-sites/"+id, newSite),
			del(t, h, "/api/managed-sites/"+id),
		} {
			if rec.Code != http.StatusNotFound || fields(t, rec)["code"] != "not-found" {
				t.Errorf("id %q: status = %d, body %s; want 404 not-found", id, rec.Code, rec.Body)
			}
		}
	}
	if len(f.applied) != 0 {
		t.Errorf("applied = %+v, want nothing for an unknown site", f.applied)
	}
}

func TestDeleteAManagedSite(t *testing.T) {
	f := newFakeSites()
	rec := del(t, sitesHandler(t, f), "/api/managed-sites/"+idA)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if len(f.applied) != 1 || !slices.Equal(hostnamesOf(f.applied[0]), []string{"b.example.com"}) {
		t.Errorf("applied = %+v, want only b.example.com", f.applied)
	}
	if rev, _ := fields(t, rec)["revision"].(map[string]any); rev["number"] != float64(9) {
		t.Errorf("revision = %v, want the applied revision", rev)
	}
}

func TestPreviewAManagedSite(t *testing.T) {
	t.Run("a good site gives the text and applies nothing", func(t *testing.T) {
		f := newFakeSites()
		rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites/preview", newSite)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		got := fields(t, rec)
		if got["valid"] != true || got["text"] != "# generated for 3 sites\n" {
			t.Errorf("answer = %v, want valid, with the generated text", got)
		}
		if len(f.applied) != 0 || len(f.previewed) != 1 || len(f.previewed[0]) != 3 {
			t.Errorf("applied %d, previewed %+v; want nothing applied and the three sites previewed", len(f.applied), f.previewed)
		}
	})

	t.Run("a site that is already there is previewed as the edit", func(t *testing.T) {
		f := newFakeSites()
		body := `{"id":"` + idA + `","hostname":"a2.example.com","kind":"proxy","upstreams":["10.0.0.9"]}`
		rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites/preview", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		if len(f.previewed) != 1 || len(f.previewed[0]) != 2 || f.previewed[0][0].Hostname != "a2.example.com" {
			t.Errorf("previewed = %+v, want the two sites with the first one changed", f.previewed)
		}
	})

	t.Run("problems are an answer, not an error", func(t *testing.T) {
		f := newFakeSites()
		f.previewErr = &configmgr.InvalidSitesError{Errors: []configmgr.SiteFieldError{
			{SiteID: "x", Hostname: "bad host", Field: "hostname", Message: "a hostname has no spaces"},
			{SiteID: "x", Hostname: "bad host", Field: "upstreams", Message: "port 0 is not a port"},
		}}
		rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites/preview", newSite)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		got := fields(t, rec)
		errs, _ := got["errors"].([]any)
		if got["valid"] != false || len(errs) != 2 {
			t.Fatalf("answer = %v, want invalid with the two problems", got)
		}
		first, _ := errs[0].(map[string]any)
		if first["field"] != "hostname" || first["message"] != "a hostname has no spaces" || first["site"] != "x" {
			t.Errorf("errors[0] = %v, want the field, the message and the site", first)
		}
	})
}

func TestManagedSiteFailuresSayNothingWasApplied(t *testing.T) {
	invalid := &configmgr.InvalidSitesError{Errors: []configmgr.SiteFieldError{{SiteID: "x", Hostname: "c.example.com", Field: "upstreams", Message: "no upstream"}}}
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"an invalid site", fmt.Errorf("apply: %w", invalid), http.StatusUnprocessableEntity, "invalid-site"},
		{"the Caddyfile does not import the managed file", fmt.Errorf("apply: %w", configmgr.ErrNotImported), http.StatusUnprocessableEntity, "not-imported"},
		{"the managed file was edited by hand", fmt.Errorf("apply: %w", configmgr.ErrManagedEdited), http.StatusConflict, "managed-file-edited"},
		{"a relative import", fmt.Errorf("apply: %w", &configmgr.RelativeImportError{Pattern: "conf.d/*.caddy", Line: 1}), http.StatusUnprocessableEntity, "relative-import"},
		{"a load that is refused", fmt.Errorf("%w: boom", configmgr.ErrApplyFailed), http.StatusBadGateway, "apply-failed"},
		{"a restore that fails too", fmt.Errorf("%w: boom", configmgr.ErrRecoveryRequired), http.StatusInternalServerError, "recovery-required"},
		{"no managed file", fmt.Errorf("apply: %w", configmgr.ErrNoManagedFile), http.StatusUnprocessableEntity, "no-managed-file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeSites()
			f.applyErr = tt.err
			rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites", newSite)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.status, rec.Body)
			}
			got := fields(t, rec)
			if got["code"] != tt.code {
				t.Errorf("code = %v, want %q", got["code"], tt.code)
			}
			msg, _ := got["error"].(string)
			if msg == "" || !strings.Contains(strings.ToLower(msg), "not applied") && !strings.Contains(strings.ToLower(msg), "nothing was applied") {
				t.Errorf("error = %q, want it to say that nothing was applied", msg)
			}
		})
	}

	t.Run("an invalid site lists its problems", func(t *testing.T) {
		f := newFakeSites()
		f.applyErr = invalid
		rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites", newSite)
		errs, _ := fields(t, rec)["errors"].([]any)
		if len(errs) != 1 {
			t.Fatalf("errors = %v, want the one problem", errs)
		}
		if e, _ := errs[0].(map[string]any); e["field"] != "upstreams" || e["site"] != "x" || e["message"] != "no upstream" {
			t.Errorf("errors[0] = %v, want the field, the site and the message", e)
		}
	})

	t.Run("a site that Caddy rejects is named, with its line", func(t *testing.T) {
		f := newFakeSites()
		f.applyErr = fmt.Errorf("apply: %w", &configmgr.SiteError{SiteID: "x", Hostname: "c.example.com", Line: 7, Message: "ambiguous site definition"})
		rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites", newSite)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		got := fields(t, rec)
		site, _ := got["site"].(map[string]any)
		if got["code"] != "site-rejected" || site["id"] != "x" || site["hostname"] != "c.example.com" || got["line"] != float64(7) {
			t.Errorf("answer = %v, want site-rejected naming x at line 7", got)
		}
		if msg, _ := got["error"].(string); !strings.Contains(msg, "ambiguous site definition") {
			t.Errorf("error = %q, want Caddy's words", msg)
		}
	})

	t.Run("any other failure hides the cause", func(t *testing.T) {
		f := newFakeSites()
		f.applyErr = errors.New("write /etc/caddy/conf.d/caddy-view-sites.caddy: disk full")
		rec := postJSON(t, sitesHandler(t, f), "/api/managed-sites", newSite)
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "disk full") {
			t.Errorf("status = %d, body %s; want a 500 without the cause", rec.Code, rec.Body)
		}
	})
}

func hostnamesOf(all []store.Site) []string {
	out := make([]string, len(all))
	for i, s := range all {
		out[i] = s.Hostname
	}
	return out
}
