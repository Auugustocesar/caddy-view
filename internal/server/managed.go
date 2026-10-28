package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/Auugustocesar/caddy-view/internal/configmgr"
	"github.com/Auugustocesar/caddy-view/internal/sites"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// SiteManager is what the managed sites routes need: the sites, a preview, and the apply that saves them.
type SiteManager interface {
	Sites(ctx context.Context) ([]store.Site, error)
	// PreviewSites validates the desired sites and returns the managed file they would produce, touching nothing.
	PreviewSites(ctx context.Context, desired []store.Site) (string, error)
	// ApplySites makes the desired sites the managed ones, or changes nothing and says why.
	ApplySites(ctx context.Context, desired []store.Site) (store.Revision, error)
	// ManagedState says where the managed file is and whether the Caddyfile imports it.
	ManagedState(ctx context.Context) (configmgr.ManagedState, error)
	// AddImport adds the import of the managed file to the Caddyfile, or changes nothing and says why.
	AddImport(ctx context.Context) (store.Revision, error)
}

// siteSaid is what a refused change of sites says first: that nothing happened.
var siteSaid = phrases{
	notApplied: "Nothing was applied and the sites are unchanged: ",
	notCleanly: "The change was not applied cleanly and caddy-view could not put everything back: ",
}

var siteID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ---- the JSON of a site

type managedSiteJSON struct {
	ID             string   `json:"id"`
	Hostname       string   `json:"hostname"`
	Kind           string   `json:"kind"`
	Upstreams      []string `json:"upstreams"`
	SkipTLSVerify  bool     `json:"skipTlsVerify"`
	TLS            string   `json:"tls"`
	RedirectTarget string   `json:"redirectTarget"`
	RedirectCode   int      `json:"redirectCode"`
	AccessLog      bool     `json:"accessLog"`
	// BasicAuth says that the site asks for a password, and for whom. It never has the hash, nor anything of the password.
	BasicAuth *basicAuthJSON `json:"basicAuth"`
	Enabled   bool           `json:"enabled"`
	CreatedAt string         `json:"createdAt"`
	UpdatedAt string         `json:"updatedAt"`
}

type basicAuthJSON struct {
	User string `json:"user"`
}

func managedSiteAnswer(s store.Site) managedSiteJSON {
	ups := s.Upstreams
	if ups == nil {
		ups = []string{}
	}
	var auth *basicAuthJSON
	if s.BasicAuthUser != "" {
		auth = &basicAuthJSON{User: s.BasicAuthUser}
	}
	return managedSiteJSON{
		BasicAuth: auth,
		ID:        s.ID, Hostname: s.Hostname, Kind: string(s.Kind), Upstreams: ups, SkipTLSVerify: s.SkipTLSVerify, TLS: string(s.TLS),
		RedirectTarget: s.RedirectTarget, RedirectCode: s.RedirectCode, AccessLog: s.AccessLog, Enabled: s.Enabled,
		CreatedAt: utc(s.CreatedAt), UpdatedAt: utc(s.UpdatedAt),
	}
}

type siteProblemJSON struct {
	Site    string `json:"site"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

type siteRefJSON struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
}

func siteProblems(e *configmgr.InvalidSitesError) []siteProblemJSON {
	out := make([]siteProblemJSON, len(e.Errors))
	for i, p := range e.Errors {
		out[i] = siteProblemJSON{Site: p.SiteID, Field: p.Field, Message: p.Message}
	}
	return out
}

// siteInput is what a request may set of a site: nothing that the server decides (the ID and the times).
type siteInput struct {
	Hostname       string         `json:"hostname"`
	Kind           store.SiteKind `json:"kind"`
	Upstreams      []string       `json:"upstreams"`
	SkipTLSVerify  bool           `json:"skipTlsVerify"`
	TLS            store.TLSMode  `json:"tls"`
	RedirectTarget string         `json:"redirectTarget"`
	RedirectCode   int            `json:"redirectCode"`
	AccessLog      bool           `json:"accessLog"`
	Enabled        *bool          `json:"enabled"`
	// BasicAuth is the username and the password, in clear. Absent or null, or without a user, is no password; with a user and
	// no password the site keeps the password it has. The password lives here, in the request, and nowhere else.
	BasicAuth *basicAuthInput `json:"basicAuth"`
}

type basicAuthInput struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// setBasicAuth sets the password of site from the request, by the rules of sites.SetBasicAuth. previous is the site as it
// is now, when it exists, so that a password that is not sent is kept.
func (in siteInput) setBasicAuth(site *store.Site, previous *store.Site, cost int) []sites.FieldError {
	var user, password string
	if in.BasicAuth != nil {
		user, password = in.BasicAuth.User, in.BasicAuth.Password
	}
	return sites.SetBasicAuth(site, user, password, previous, cost)
}

// fieldProblems puts the problems of the fields of one site in the shape the answers have.
func fieldProblems(id string, errs []sites.FieldError) []siteProblemJSON {
	out := make([]siteProblemJSON, len(errs))
	for i, e := range errs {
		out[i] = siteProblemJSON{Site: id, Field: e.Field, Message: e.Message}
	}
	return out
}

// invalidSite answers a request whose password is not acceptable: nothing was applied.
func (a *api) invalidSite(w http.ResponseWriter, id string, errs []sites.FieldError) {
	writeJSON(w, http.StatusUnprocessableEntity, failureJSON{
		Error: siteSaid.notApplied + "the site is not valid", Code: "invalid-site", Errors: fieldProblems(id, errs),
	})
}

// previewInput is a site to preview, which may name the site it would replace.
type previewInput struct {
	ID string `json:"id"`
	siteInput
}

// site makes the site a request describes; a site is enabled unless it says otherwise.
func (in siteInput) site(id string) store.Site {
	enabled := in.Enabled == nil || *in.Enabled
	return store.Site{
		ID: id, Hostname: in.Hostname, Kind: in.Kind, Upstreams: in.Upstreams, SkipTLSVerify: in.SkipTLSVerify, TLS: in.TLS,
		RedirectTarget: in.RedirectTarget, RedirectCode: in.RedirectCode, AccessLog: in.AccessLog, Enabled: enabled,
	}
}

// ---- routes

// managedRoute routes /api/managed-sites/{id}.
func (a *api) managedRoute(path string) (route, bool) {
	id, found := strings.CutPrefix(path, "/api/managed-sites/")
	if !found || id == "" {
		return route{}, false
	}
	return newRoute(http.MethodPut, a.sitesUpdate).with(http.MethodDelete, a.sitesDelete).wrap(func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", id)
			h(w, r)
		}
	}), true
}

func (a *api) siteNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, failureJSON{Error: "no managed site has that ID", Code: "not-found"})
}

func (a *api) sitesList(w http.ResponseWriter, r *http.Request) {
	all, err := a.opts.Sites.Sites(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	state, err := a.opts.Sites.ManagedState(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	out := make([]managedSiteJSON, len(all))
	for i, s := range all {
		out[i] = managedSiteAnswer(s)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sites": out, "managedFile": state.File, "imported": state.Imported, "accessLog": state.AccessLog,
		"backupMode": a.backupMode(),
	})
}

// importSaid is what a refused import says first: that the Caddyfile was left as it was.
var importSaid = phrases{
	notApplied: "Nothing was changed: ",
	notCleanly: "The import was not added cleanly and caddy-view could not put everything back: ",
}

// sitesImport adds the import of the managed file to the Caddyfile. The path is the server's, never the request's.
func (a *api) sitesImport(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !readBody(w, r, &body, true) {
		return
	}
	rev, err := a.opts.Sites.AddImport(r.Context())
	if err != nil {
		a.changeFailedWith(w, r, rev, err, importSaid)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": revisionAnswer(rev, false)})
}

// changed answers a change of sites that went through, with the site as it was saved when there is one.
func (a *api) changed(w http.ResponseWriter, r *http.Request, rev store.Revision, id string) {
	answer := map[string]any{"revision": revisionAnswer(rev, false)}
	if id != "" {
		saved, err := a.opts.Sites.Sites(r.Context())
		if err != nil {
			a.internal(w, r, err)
			return
		}
		for _, s := range saved {
			if s.ID == id {
				answer["site"] = managedSiteAnswer(s)
			}
		}
	}
	writeJSON(w, http.StatusOK, answer)
}

func (a *api) sitesCreate(w http.ResponseWriter, r *http.Request) {
	var in siteInput
	if !readBody(w, r, &in, false) {
		return
	}
	current, err := a.opts.Sites.Sites(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	added := in.site(sites.NewID())
	if errs := in.setBasicAuth(&added, nil, a.opts.BasicAuthCost); errs != nil {
		a.invalidSite(w, added.ID, errs)
		return
	}
	rev, err := a.opts.Sites.ApplySites(r.Context(), append(current, added))
	if err != nil {
		a.changeFailedWith(w, r, rev, err, siteSaid)
		return
	}
	a.changed(w, r, rev, added.ID)
}

func (a *api) sitesUpdate(w http.ResponseWriter, r *http.Request) {
	var in siteInput
	if !readBody(w, r, &in, false) {
		return
	}
	id := r.PathValue("id")
	current, err := a.opts.Sites.Sites(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	i := slices.IndexFunc(current, func(s store.Site) bool { return s.ID == id })
	if !siteID.MatchString(id) || i < 0 {
		a.siteNotFound(w)
		return
	}
	updated := in.site(id)
	updated.CreatedAt, updated.UpdatedAt = current[i].CreatedAt, current[i].UpdatedAt
	if errs := in.setBasicAuth(&updated, &current[i], a.opts.BasicAuthCost); errs != nil {
		a.invalidSite(w, id, errs)
		return
	}
	desired := slices.Clone(current)
	desired[i] = updated
	rev, err := a.opts.Sites.ApplySites(r.Context(), desired)
	if err != nil {
		a.changeFailedWith(w, r, rev, err, siteSaid)
		return
	}
	a.changed(w, r, rev, id)
}

func (a *api) sitesDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	current, err := a.opts.Sites.Sites(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	i := slices.IndexFunc(current, func(s store.Site) bool { return s.ID == id })
	if !siteID.MatchString(id) || i < 0 {
		a.siteNotFound(w)
		return
	}
	rev, err := a.opts.Sites.ApplySites(r.Context(), slices.Delete(slices.Clone(current), i, i+1))
	if err != nil {
		a.changeFailedWith(w, r, rev, err, siteSaid)
		return
	}
	a.changed(w, r, rev, "")
}

func (a *api) sitesPreview(w http.ResponseWriter, r *http.Request) {
	var in previewInput
	if !readBody(w, r, &in, false) {
		return
	}
	current, err := a.opts.Sites.Sites(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	desired := slices.Clone(current)
	i := slices.IndexFunc(current, func(s store.Site) bool { return in.ID != "" && s.ID == in.ID })
	id := sites.NewID()
	var previous *store.Site
	if i >= 0 {
		id, previous = in.ID, &current[i]
	}
	site := in.siteInput.site(id)
	// A preview checks the password by the same rules but with the cheapest hash, and then puts a placeholder where the
	// hash goes: a preview is asked at every pause in typing, and the hash of a site that keeps its password must not
	// be given to the browser.
	if errs := in.siteInput.setBasicAuth(&site, previous, bcrypt.MinCost); errs != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "errors": fieldProblems(id, errs)})
		return
	}
	if site.BasicAuthUser != "" {
		site.BasicAuthHash = sites.PlaceholderHash
	}
	if i >= 0 {
		desired[i] = site
	} else {
		desired = append(desired, site)
	}
	text, err := a.opts.Sites.PreviewSites(r.Context(), desired)
	var invalid *configmgr.InvalidSitesError
	switch {
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "errors": siteProblems(invalid)})
	case err != nil:
		a.changeFailedWith(w, r, store.Revision{}, err, siteSaid)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"valid": true, "text": text})
	}
}
