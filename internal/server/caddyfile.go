package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/configmgr"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// Editor is what the Caddyfile and revision routes need: the manager that applies changes and the revision history.
type Editor interface {
	Current(ctx context.Context) (configmgr.File, error)
	Validate(ctx context.Context, content string) (*caddy.AdaptResult, error)
	Apply(ctx context.Context, req configmgr.ApplyRequest) (store.Revision, error)
	Rollback(ctx context.Context, number int64) (store.Revision, error)
	Revision(ctx context.Context, number int64) (store.Revision, error)
	Revisions(ctx context.Context, q store.RevisionQuery) ([]store.Revision, error)
	Reconcile(ctx context.Context) (configmgr.Drift, error)
}

// maxBody is the largest request body the editing routes read.
const maxBody = 1 << 20

// ---- guards

// sameSiteJSON refuses a POST that a page on another site could have sent. Without a login, the Host check does not
// stop that: the request goes to 127.0.0.1 with the right Host. A browser cannot send application/json cross-site
// without a preflight, which is never answered, and it names its page in Origin. It reports whether the request may
// go on; when not, the answer is written.
func (a *api) sameSiteJSON(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if origin != scheme+"://"+r.Host {
			a.log.Warn("request rejected: foreign origin", "origin", origin, "host", r.Host, "path", r.URL.Path)
			writeError(w, http.StatusForbidden, "cross-site request refused")
			return false
		}
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "send the body as application/json")
		return false
	}
	return true
}

// readBody decodes a JSON object body into v, which must not have unknown fields. An empty body is accepted only
// with allowEmpty. It reports whether the request may go on; when not, the answer is written.
func readBody(w http.ResponseWriter, r *http.Request, v any, allowEmpty bool) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if errors.Is(err, io.EOF) && allowEmpty {
		return true
	}
	if err == nil {
		// Nothing may follow the object.
		if _, next := dec.Token(); !errors.Is(next, io.EOF) {
			err = errors.New("unexpected data after the JSON body")
		}
	}
	var tooLarge *http.MaxBytesError
	switch {
	case err == nil:
		return true
	case errors.As(err, &tooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the request body is larger than %d bytes", maxBody))
	default:
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
	}
	return false
}

// ---- answers

type revisionJSON struct {
	Number  int64   `json:"number"`
	Time    string  `json:"time"`
	Trigger string  `json:"trigger"`
	Status  string  `json:"status"`
	SHA256  string  `json:"sha256"`
	Error   string  `json:"error,omitempty"`
	Content *string `json:"content,omitempty"`
}

func revisionAnswer(r store.Revision, withContent bool) *revisionJSON {
	out := &revisionJSON{
		Number: r.Number, Time: r.Time.UTC().Format(time.RFC3339Nano), Trigger: string(r.Trigger),
		Status: string(r.Status), SHA256: r.SHA256, Error: r.Error,
	}
	if withContent {
		out.Content = &r.Content
	}
	return out
}

// failureJSON is the body of a failed apply or rollback.
type failureJSON struct {
	Error    string        `json:"error"`
	Code     string        `json:"code"`
	Line     int           `json:"line,omitempty"`
	Revision *revisionJSON `json:"revision,omitempty"`
	// Errors and Site belong to the managed sites: the problems found, or the site Caddy rejected.
	Errors []siteProblemJSON `json:"errors,omitempty"`
	Site   *siteRefJSON      `json:"site,omitempty"`
}

// internal logs the cause of a failure that is not the client's and answers a generic 500.
func (a *api) internal(w http.ResponseWriter, r *http.Request, err error) {
	a.log.Error("editing request failed", "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

// changeFailed answers an apply or a rollback that did not succeed.
func (a *api) changeFailed(w http.ResponseWriter, r *http.Request, rev store.Revision, err error) {
	a.changeFailedWith(w, r, rev, err, phrases{})
}

// phrases are what a failure says about the change, for routes whose change is not a whole file: that nothing was
// applied, and the table of sites is as it was. The zero value adds nothing.
type phrases struct {
	notApplied string // put before the message of a failure that left everything as it was
	notCleanly string // put before the message of a failure that did not
}

func (a *api) changeFailedWith(w http.ResponseWriter, r *http.Request, rev store.Revision, err error, say phrases) {
	var adaptErr *caddy.AdaptError
	switch {
	case errors.Is(err, configmgr.ErrInvalidSite):
		f := failureJSON{Error: say.notApplied + err.Error(), Code: "invalid-site"}
		var invalid *configmgr.InvalidSitesError
		if errors.As(err, &invalid) {
			f.Errors = siteProblems(invalid)
		}
		writeJSON(w, http.StatusUnprocessableEntity, f)
	case errors.Is(err, configmgr.ErrNotImported):
		writeJSON(w, http.StatusUnprocessableEntity, failureJSON{Error: say.notApplied + err.Error(), Code: "not-imported"})
	case errors.Is(err, configmgr.ErrManagedEdited):
		writeJSON(w, http.StatusConflict, failureJSON{Error: say.notApplied + err.Error(), Code: "managed-file-edited"})
	case errors.Is(err, configmgr.ErrNoManagedFile):
		writeJSON(w, http.StatusUnprocessableEntity, failureJSON{Error: say.notApplied + err.Error(), Code: "no-managed-file"})
	case errors.Is(err, configmgr.ErrSiteRejected):
		f := failureJSON{Error: say.notApplied + err.Error(), Code: "site-rejected"}
		var rejected *configmgr.SiteError
		if errors.As(err, &rejected) {
			f.Site, f.Line = &siteRefJSON{ID: rejected.SiteID, Hostname: rejected.Hostname}, rejected.Line
			f.Error = say.notApplied + rejected.Error()
		}
		writeJSON(w, http.StatusUnprocessableEntity, f)
	case errors.Is(err, configmgr.ErrConflict):
		writeJSON(w, http.StatusConflict, failureJSON{
			Error: "the Caddyfile changed since it was read: reload it and apply again", Code: "conflict",
		})
	case errors.Is(err, configmgr.ErrInvalid):
		f := failureJSON{Error: err.Error(), Code: "invalid"}
		if errors.As(err, &adaptErr) {
			f.Error, f.Line = adaptErr.Message, adaptErr.Line
		}
		writeJSON(w, http.StatusUnprocessableEntity, f)
	case errors.Is(err, configmgr.ErrRelativeImport):
		f := failureJSON{Error: say.notApplied + err.Error(), Code: "relative-import"}
		var relative *configmgr.RelativeImportError
		if errors.As(err, &relative) {
			f.Line = relative.Line
		}
		writeJSON(w, http.StatusUnprocessableEntity, f)
	case errors.Is(err, configmgr.ErrAdminMoved):
		writeJSON(w, http.StatusUnprocessableEntity, failureJSON{Error: say.notApplied + err.Error(), Code: "admin-moved"})
	case errors.Is(err, configmgr.ErrApplyFailed):
		writeJSON(w, http.StatusBadGateway, failureJSON{Error: say.notApplied + err.Error(), Code: "apply-failed", Revision: revisionAnswer(rev, false)})
	case errors.Is(err, configmgr.ErrRecoveryRequired):
		a.log.Error("the Caddyfile needs manual recovery", "revision", rev.Number, "error", err)
		writeJSON(w, http.StatusInternalServerError, failureJSON{Error: say.notCleanly + err.Error(), Code: "recovery-required", Revision: revisionAnswer(rev, false)})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, failureJSON{Error: "revision not found", Code: "not-found"})
	default:
		a.internal(w, r, err)
	}
}

// ---- handlers

func (a *api) caddyfile(w http.ResponseWriter, r *http.Request) {
	f, err := a.opts.Editor.Current(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": f.Content, "sha256": f.SHA256})
}

func (a *api) validate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content *string `json:"content"`
	}
	if !readBody(w, r, &body, false) {
		return
	}
	if body.Content == nil {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	res, err := a.opts.Editor.Validate(r.Context(), *body.Content)
	var relative *configmgr.RelativeImportError
	if errors.As(err, &relative) {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": relative.Error(), "line": relative.Line})
		return
	}
	if errors.Is(err, configmgr.ErrInvalid) {
		answer := map[string]any{"valid": false, "error": err.Error()}
		var adaptErr *caddy.AdaptError
		if errors.As(err, &adaptErr) {
			answer["error"] = adaptErr.Message
			if adaptErr.Line > 0 {
				answer["line"] = adaptErr.Line
			}
		}
		writeJSON(w, http.StatusOK, answer)
		return
	}
	if err != nil {
		a.badGateway(w, r, err)
		return
	}
	warnings := []caddy.Warning{}
	if res != nil && res.Warnings != nil {
		warnings = res.Warnings
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "warnings": warnings})
}

func (a *api) apply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content    *string `json:"content"`
		BaseSHA256 string  `json:"baseSha256"`
	}
	if !readBody(w, r, &body, false) {
		return
	}
	switch {
	case body.Content == nil:
		writeError(w, http.StatusBadRequest, "content is required")
		return
	case body.BaseSHA256 == "":
		writeError(w, http.StatusBadRequest, "baseSha256 is required: the digest of the Caddyfile the change was made from")
		return
	}
	rev, err := a.opts.Editor.Apply(r.Context(), configmgr.ApplyRequest{
		Content: *body.Content, BaseSHA256: body.BaseSHA256, Trigger: store.TriggerManualEdit,
	})
	if err != nil {
		a.changeFailed(w, r, rev, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": revisionAnswer(rev, false)})
}

func (a *api) revisions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := intParam(q, "limit")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	before, err := intParam(q, "before")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	revs, err := a.opts.Editor.Revisions(r.Context(), store.RevisionQuery{Limit: int(limit), Before: before})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// A full page may have more behind it; the last number is where the next page starts.
	page := int(limit)
	if page == 0 {
		page = store.DefaultRevisionLimit
	}
	page = min(page, store.MaxRevisionLimit)
	var next any
	if len(revs) == page {
		next = revs[len(revs)-1].Number
	}
	out := make([]*revisionJSON, len(revs))
	for i, rev := range revs {
		out[i] = revisionAnswer(rev, false)
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": out, "next": next})
}

// revisionRoute routes /api/revisions/{n} and /api/revisions/{n}/rollback.
func (a *api) revisionRoute(path string) (route, bool) {
	rest, found := strings.CutPrefix(path, "/api/revisions/")
	if !found {
		return route{}, false
	}
	num, action, _ := strings.Cut(rest, "/")
	var rt route
	switch action {
	case "":
		rt = onGet(a.revision)
	case "rollback":
		rt = onPost(a.rollback)
	default:
		return route{}, false
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n < 1 {
		return rt.wrap(func(http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid revision number %q: use a positive whole number", num))
			}
		}), true
	}
	return rt.wrap(func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("number", num)
			h(w, r)
		}
	}), true
}

// revisionNumber is the number in the path; revisionRoute has already checked it.
func revisionNumber(r *http.Request) int64 {
	n, _ := strconv.ParseInt(r.PathValue("number"), 10, 64)
	return n
}

func (a *api) revision(w http.ResponseWriter, r *http.Request) {
	rev, err := a.opts.Editor.Revision(r.Context(), revisionNumber(r))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, failureJSON{Error: "revision not found", Code: "not-found"})
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, revisionAnswer(rev, true))
}

func (a *api) rollback(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !readBody(w, r, &body, true) {
		return
	}
	rev, err := a.opts.Editor.Rollback(r.Context(), revisionNumber(r))
	if err != nil {
		a.changeFailed(w, r, rev, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": revisionAnswer(rev, false)})
}
