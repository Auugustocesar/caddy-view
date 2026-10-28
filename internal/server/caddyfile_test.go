package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/configmgr"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

const digestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// fakeEditor answers the editing calls with what the test set, and keeps its revisions in a real memory store.
type fakeEditor struct {
	*store.Memory

	mu          sync.Mutex
	file        configmgr.File
	currentErr  error
	adapted     *caddy.AdaptResult
	validateErr error
	applyRev    store.Revision
	applyErr    error
	rollbackRev store.Revision
	rollbackErr error

	validated  []string
	applied    []configmgr.ApplyRequest
	rolledBack []int64
}

func newEditor() *fakeEditor {
	return &fakeEditor{
		Memory:   store.NewMemory(),
		file:     configmgr.File{Content: "example.com {\n\trespond \"hi\"\n}\n", SHA256: digestA},
		adapted:  &caddy.AdaptResult{},
		applyRev: store.Revision{Number: 7, Time: clock, Trigger: store.TriggerManualEdit, Status: store.StatusApplied, SHA256: digestA},
	}
}

func (f *fakeEditor) Current(context.Context) (configmgr.File, error) { return f.file, f.currentErr }

func (f *fakeEditor) Validate(_ context.Context, content string) (*caddy.AdaptResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.validated = append(f.validated, content)
	return f.adapted, f.validateErr
}

func (f *fakeEditor) Apply(_ context.Context, req configmgr.ApplyRequest) (store.Revision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, req)
	return f.applyRev, f.applyErr
}

func (f *fakeEditor) Rollback(_ context.Context, n int64) (store.Revision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rolledBack = append(f.rolledBack, n)
	return f.rollbackRev, f.rollbackErr
}

func editHandler(t *testing.T, e Editor) http.Handler {
	t.Helper()
	return New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint, Editor: e, Now: func() time.Time { return clock }})
}

// send makes a request as a browser page of caddy-view would, and returns the recorder.
func send(t *testing.T, h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://localhost:8484"+path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var jsonType = map[string]string{"Content-Type": "application/json"}

func postJSON(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, h, http.MethodPost, path, body, jsonType)
}

func fields(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	decode(t, rec, &m)
	return m
}

func addRevisions(t *testing.T, e *fakeEditor, n int) {
	t.Helper()
	for i := range n {
		_, err := e.AddRevision(context.Background(), store.Revision{
			Time: clock.Add(time.Duration(i) * time.Minute), Trigger: store.TriggerManualEdit, Status: store.StatusApplied,
			Content: fmt.Sprintf("content %d", i+1), SHA256: digestA,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestEditingRoutesAreAbsentWithoutAnEditor(t *testing.T) {
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint})
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/caddyfile"},
		{http.MethodPost, "/api/caddyfile/validate"},
		{http.MethodPost, "/api/caddyfile/apply"},
		{http.MethodGet, "/api/revisions"},
		{http.MethodGet, "/api/revisions/1"},
		{http.MethodPost, "/api/revisions/1/rollback"},
	} {
		if rec := send(t, h, c.method, c.path, "{}", jsonType); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, rec.Code)
		}
	}
}

func TestEditingRoutesOnlyAnswerTheirMethod(t *testing.T) {
	h := editHandler(t, newEditor())
	for _, c := range []struct{ method, path, allow string }{
		{http.MethodPost, "/api/caddyfile", http.MethodGet},
		{http.MethodGet, "/api/caddyfile/validate", http.MethodPost},
		{http.MethodGet, "/api/caddyfile/apply", http.MethodPost},
		{http.MethodPut, "/api/revisions", http.MethodGet},
		{http.MethodPost, "/api/revisions/1", http.MethodGet},
		{http.MethodGet, "/api/revisions/1/rollback", http.MethodPost},
		{http.MethodDelete, "/api/revisions/1/rollback", http.MethodPost},
	} {
		rec := send(t, h, c.method, c.path, "{}", jsonType)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != c.allow {
			t.Errorf("%s %s = %d, Allow %q; want 405, Allow %q", c.method, c.path, rec.Code, rec.Header().Get("Allow"), c.allow)
		}
	}
}

func TestEditingRoutesKeepTheHostCheck(t *testing.T) {
	h := editHandler(t, newEditor())
	req := httptest.NewRequest(http.MethodGet, "http://evil.example.com/api/caddyfile", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a foreign Host", rec.Code)
	}
}

func TestGetCaddyfile(t *testing.T) {
	e := newEditor()
	rec := do(t, editHandler(t, e), http.MethodGet, "/api/caddyfile")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	got := fields(t, rec)
	if got["content"] != e.file.Content || got["sha256"] != digestA {
		t.Errorf("body = %v, want the file and its digest", got)
	}
}

func TestGetCaddyfileFailureHidesTheCause(t *testing.T) {
	e := newEditor()
	e.currentErr = errors.New("open /etc/caddy/Caddyfile: permission denied")
	rec := do(t, editHandler(t, e), http.MethodGet, "/api/caddyfile")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "permission denied") {
		t.Errorf("status = %d, body %s; want a 500 without the cause", rec.Code, rec.Body)
	}
}

func TestValidate(t *testing.T) {
	t.Run("accepted, with warnings", func(t *testing.T) {
		e := newEditor()
		e.adapted = &caddy.AdaptResult{Warnings: []caddy.Warning{{File: "Caddyfile", Line: 2, Message: "not formatted"}}}
		rec := postJSON(t, editHandler(t, e), "/api/caddyfile/validate", `{"content":"a.example {\n}\n"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		var got struct {
			Valid    bool `json:"valid"`
			Warnings []struct {
				Line    int    `json:"line"`
				Message string `json:"message"`
			} `json:"warnings"`
		}
		decode(t, rec, &got)
		if !got.Valid || len(got.Warnings) != 1 || got.Warnings[0].Line != 2 || got.Warnings[0].Message != "not formatted" {
			t.Errorf("answer = %+v, want valid with the warning", got)
		}
		if len(e.validated) != 1 || e.validated[0] != "a.example {\n}\n" {
			t.Errorf("validated = %q, want the posted content", e.validated)
		}
	})

	t.Run("no warnings is an empty list", func(t *testing.T) {
		rec := postJSON(t, editHandler(t, newEditor()), "/api/caddyfile/validate", `{"content":"x"}`)
		if w, ok := fields(t, rec)["warnings"].([]any); !ok || len(w) != 0 {
			t.Errorf("warnings = %v, want []", fields(t, rec)["warnings"])
		}
	})

	t.Run("rejected by caddy is an answer, not an error", func(t *testing.T) {
		e := newEditor()
		e.validateErr = fmt.Errorf("%w: %w", configmgr.ErrInvalid, &caddy.AdaptError{Message: "Caddyfile:3: unrecognized directive", Line: 3})
		rec := postJSON(t, editHandler(t, e), "/api/caddyfile/validate", `{"content":"x"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		got := fields(t, rec)
		if got["valid"] != false || got["line"] != float64(3) || got["error"] == "" {
			t.Errorf("answer = %v, want invalid with line 3 and the message", got)
		}
	})

	t.Run("caddy unreachable is a bad gateway", func(t *testing.T) {
		e := newEditor()
		e.validateErr = caddy.ErrUnreachable
		rec := postJSON(t, editHandler(t, e), "/api/caddyfile/validate", `{"content":"x"}`)
		if rec.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", rec.Code)
		}
	})
}

func TestApply(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := newEditor()
		body := fmt.Sprintf(`{"content":"new","baseSha256":%q}`, digestA)
		rec := postJSON(t, editHandler(t, e), "/api/caddyfile/apply", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body)
		}
		want := configmgr.ApplyRequest{Content: "new", BaseSHA256: digestA, Trigger: store.TriggerManualEdit}
		if len(e.applied) != 1 || e.applied[0] != want {
			t.Errorf("applied = %+v, want %+v", e.applied, want)
		}
		rev, _ := fields(t, rec)["revision"].(map[string]any)
		if rev["number"] != float64(7) || rev["status"] != "applied" || rev["trigger"] != "manual-edit" ||
			rev["time"] != "2026-10-27T12:00:00Z" || rev["sha256"] != digestA {
			t.Errorf("revision = %v, want the applied revision", rev)
		}
		if _, has := rev["content"]; has {
			t.Errorf("the apply answer repeats the content: %v", rev)
		}
	})

	failures := []struct {
		name     string
		err      error
		rev      store.Revision
		status   int
		code     string
		line     float64
		revision bool
	}{
		{name: "stale base", err: fmt.Errorf("apply: %w", configmgr.ErrConflict), status: http.StatusConflict, code: "conflict"},
		{
			name:   "rejected by caddy",
			err:    fmt.Errorf("%w: %w", configmgr.ErrInvalid, &caddy.AdaptError{Message: "Caddyfile:5: bad", Line: 5}),
			status: http.StatusUnprocessableEntity, code: "invalid", line: 5,
		},
		{
			name:   "would move the admin endpoint",
			err:    fmt.Errorf("%w: it listens on localhost:2019 now", configmgr.ErrAdminMoved),
			status: http.StatusUnprocessableEntity, code: "admin-moved",
		},
		{
			name: "load refused, previous restored", err: fmt.Errorf("%w: boom", configmgr.ErrApplyFailed),
			rev:    store.Revision{Number: 8, Status: store.StatusFailed},
			status: http.StatusBadGateway, code: "apply-failed", revision: true,
		},
		{
			name: "recovery required", err: fmt.Errorf("%w: boom", configmgr.ErrRecoveryRequired),
			rev:    store.Revision{Number: 9, Status: store.StatusRecoveryRequired},
			status: http.StatusInternalServerError, code: "recovery-required", revision: true,
		},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			e := newEditor()
			e.applyErr, e.applyRev = tt.err, tt.rev
			rec := postJSON(t, editHandler(t, e), "/api/caddyfile/apply", fmt.Sprintf(`{"content":"new","baseSha256":%q}`, digestA))
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.status, rec.Body)
			}
			got := fields(t, rec)
			if got["code"] != tt.code || got["error"] == "" {
				t.Errorf("body = %v, want code %q and a message", got, tt.code)
			}
			if tt.line != 0 && got["line"] != tt.line {
				t.Errorf("line = %v, want %v", got["line"], tt.line)
			}
			if _, has := got["revision"]; has != tt.revision {
				t.Errorf("revision present = %v, want %v", has, tt.revision)
			}
		})
	}

	t.Run("any other failure hides the cause", func(t *testing.T) {
		e := newEditor()
		e.applyErr = errors.New("write /etc/caddy/Caddyfile: disk full")
		rec := postJSON(t, editHandler(t, e), "/api/caddyfile/apply", fmt.Sprintf(`{"content":"new","baseSha256":%q}`, digestA))
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "disk full") {
			t.Errorf("status = %d, body %s; want a 500 without the cause", rec.Code, rec.Body)
		}
	})
}

func TestEditingBodies(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		body    string
		headers map[string]string
		status  int
		names   string // what the error must mention
	}{
		{"apply: not JSON", "/api/caddyfile/apply", `nope`, jsonType, 400, ""},
		{"apply: unknown field", "/api/caddyfile/apply", `{"content":"x","baseSha256":"` + digestA + `","force":true}`, jsonType, 400, "force"},
		{"apply: no content", "/api/caddyfile/apply", `{"baseSha256":"` + digestA + `"}`, jsonType, 400, "content"},
		{"apply: no base", "/api/caddyfile/apply", `{"content":"x"}`, jsonType, 400, "baseSha256"},
		{"apply: trailing data", "/api/caddyfile/apply", `{"content":"x","baseSha256":"` + digestA + `"} {}`, jsonType, 400, ""},
		{"apply: too large", "/api/caddyfile/apply", `{"content":"` + strings.Repeat("a", 1<<20+1) + `","baseSha256":"` + digestA + `"}`, jsonType, 413, ""},
		{"apply: form content type", "/api/caddyfile/apply", `{"content":"x","baseSha256":"` + digestA + `"}`, map[string]string{"Content-Type": "text/plain"}, 415, ""},
		{"apply: no content type", "/api/caddyfile/apply", `{"content":"x","baseSha256":"` + digestA + `"}`, nil, 415, ""},
		{"validate: not JSON", "/api/caddyfile/validate", `nope`, jsonType, 400, ""},
		{"validate: no content", "/api/caddyfile/validate", `{}`, jsonType, 400, "content"},
		{"validate: no content type", "/api/caddyfile/validate", `{"content":"x"}`, nil, 415, ""},
		{"rollback: no content type", "/api/revisions/1/rollback", ``, nil, 415, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEditor()
			rec := send(t, editHandler(t, e), http.MethodPost, tt.path, tt.body, tt.headers)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d: %.200s", rec.Code, tt.status, rec.Body)
			}
			if msg, _ := fields(t, rec)["error"].(string); msg == "" || !strings.Contains(msg, tt.names) {
				t.Errorf("error = %q, want a message mentioning %q", msg, tt.names)
			}
			if len(e.applied) != 0 || len(e.validated) != 0 || len(e.rolledBack) != 0 {
				t.Errorf("the editor was called for a refused request")
			}
		})
	}
}

func TestPostsMustComeFromTheSameOrigin(t *testing.T) {
	body := fmt.Sprintf(`{"content":"new","baseSha256":%q}`, digestA)
	tests := []struct {
		name   string
		origin string // empty means no Origin header
		status int
	}{
		{"no origin, as curl", "", http.StatusOK},
		{"same origin", "http://localhost:8484", http.StatusOK},
		{"another site", "https://evil.example.com", http.StatusForbidden},
		{"another port", "http://localhost:9999", http.StatusForbidden},
		{"another scheme", "https://localhost:8484", http.StatusForbidden},
		{"opaque origin", "null", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEditor()
			headers := map[string]string{"Content-Type": "application/json"}
			if tt.origin != "" {
				headers["Origin"] = tt.origin
			}
			for _, path := range []string{"/api/caddyfile/validate", "/api/caddyfile/apply", "/api/revisions/1/rollback"} {
				b := body
				if strings.HasSuffix(path, "rollback") {
					b = ""
				}
				rec := send(t, editHandler(t, e), http.MethodPost, path, b, headers)
				wantStatus := tt.status
				if path != "/api/caddyfile/apply" && wantStatus == http.StatusOK {
					continue // only the refusal is the point for the others; success is covered elsewhere
				}
				if rec.Code != wantStatus {
					t.Errorf("POST %s with Origin %q = %d, want %d", path, tt.origin, rec.Code, wantStatus)
				}
			}
			if tt.status == http.StatusForbidden && (len(e.applied) != 0 || len(e.validated) != 0 || len(e.rolledBack) != 0) {
				t.Errorf("the editor was called for a request from %q", tt.origin)
			}
		})
	}
}

func TestRevisionsListNewestFirstWithoutContent(t *testing.T) {
	e := newEditor()
	addRevisions(t, e, 5)
	h := editHandler(t, e)

	list := func(query string) (numbers []float64, next any, raw map[string]any) {
		t.Helper()
		rec := do(t, h, http.MethodGet, "/api/revisions"+query)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/revisions%s = %d: %s", query, rec.Code, rec.Body)
		}
		raw = fields(t, rec)
		for _, r := range raw["revisions"].([]any) {
			rev := r.(map[string]any)
			if _, has := rev["content"]; has {
				t.Errorf("the list carries the content: %v", rev)
			}
			numbers = append(numbers, rev["number"].(float64))
		}
		return numbers, raw["next"], raw
	}

	got, next, _ := list("")
	if fmt.Sprint(got) != "[5 4 3 2 1]" || next != nil {
		t.Errorf("all = %v, next %v; want [5 4 3 2 1] and no next", got, next)
	}
	got, next, _ = list("?limit=2")
	if fmt.Sprint(got) != "[5 4]" || next != float64(4) {
		t.Errorf("first page = %v, next %v; want [5 4] and next 4", got, next)
	}
	got, next, _ = list("?limit=2&before=4")
	if fmt.Sprint(got) != "[3 2]" || next != float64(2) {
		t.Errorf("second page = %v, next %v; want [3 2] and next 2", got, next)
	}
	got, next, _ = list("?limit=2&before=2")
	if fmt.Sprint(got) != "[1]" || next != nil {
		t.Errorf("last page = %v, next %v; want [1] and no next", got, next)
	}
	if _, _, raw := list("?before=1"); fmt.Sprint(raw["revisions"]) != "[]" {
		t.Errorf("before the first = %v, want an empty list", raw["revisions"])
	}
}

func TestRevisionsListFields(t *testing.T) {
	e := newEditor()
	if _, err := e.AddRevision(context.Background(), store.Revision{
		Time: clock, Trigger: store.TriggerRollback, Status: store.StatusFailed, Content: "x", SHA256: digestA, Error: "boom",
	}); err != nil {
		t.Fatal(err)
	}
	rec := do(t, editHandler(t, e), http.MethodGet, "/api/revisions")
	list, _ := fields(t, rec)["revisions"].([]any)
	if rec.Code != http.StatusOK || len(list) != 1 {
		t.Fatalf("GET /api/revisions = %d %s, want the one revision", rec.Code, rec.Body)
	}
	rev, _ := list[0].(map[string]any)
	want := map[string]any{
		"number": float64(1), "time": "2026-10-27T12:00:00Z", "trigger": "rollback", "status": "failed",
		"sha256": digestA, "error": "boom",
	}
	for k, v := range want {
		if rev[k] != v {
			t.Errorf("revision[%q] = %v, want %v", k, rev[k], v)
		}
	}
}

func TestRevisionsBadParameters(t *testing.T) {
	h := editHandler(t, newEditor())
	for _, c := range []struct{ query, names string }{
		{"?limit=abc", "limit"}, {"?limit=-1", "limit"}, {"?before=x", "before"}, {"?before=-3", "before"},
	} {
		rec := do(t, h, http.MethodGet, "/api/revisions"+c.query)
		if msg, _ := fields(t, rec)["error"].(string); rec.Code != http.StatusBadRequest || !strings.Contains(msg, c.names) {
			t.Errorf("GET /api/revisions%s = %d %q, want a 400 naming %q", c.query, rec.Code, msg, c.names)
		}
	}
}

func TestOneRevisionCarriesItsContent(t *testing.T) {
	e := newEditor()
	addRevisions(t, e, 2)
	h := editHandler(t, e)

	rec := do(t, h, http.MethodGet, "/api/revisions/2")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	rev := fields(t, rec)
	if rev["number"] != float64(2) || rev["content"] != "content 2" {
		t.Errorf("revision = %v, want revision 2 with its content", rev)
	}

	if rec := do(t, h, http.MethodGet, "/api/revisions/9"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown revision = %d, want 404", rec.Code)
	}
	for _, bad := range []string{"abc", "0", "-1", "1x"} {
		if rec := do(t, h, http.MethodGet, "/api/revisions/"+bad); rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/revisions/%s = %d, want 400", bad, rec.Code)
		}
	}
	if rec := do(t, h, http.MethodGet, "/api/revisions/1/extra"); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown sub-path = %d, want 404", rec.Code)
	}
}

func TestRollback(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		e := newEditor()
		e.rollbackRev = store.Revision{Number: 12, Time: clock, Trigger: store.TriggerRollback, Status: store.StatusApplied, SHA256: digestA}
		for _, body := range []string{"", "{}"} {
			rec := postJSON(t, editHandler(t, e), "/api/revisions/3/rollback", body)
			if rec.Code != http.StatusOK {
				t.Fatalf("body %q: status = %d: %s", body, rec.Code, rec.Body)
			}
			rev, _ := fields(t, rec)["revision"].(map[string]any)
			if rev["number"] != float64(12) || rev["trigger"] != "rollback" {
				t.Errorf("revision = %v, want the new rollback revision", rev)
			}
		}
		if fmt.Sprint(e.rolledBack) != "[3 3]" {
			t.Errorf("rolled back = %v, want revision 3 each time", e.rolledBack)
		}
	})

	failures := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"unknown revision", fmt.Errorf("find revision 3: %w", store.ErrNotFound), http.StatusNotFound, "not-found"},
		{"load refused", fmt.Errorf("%w: boom", configmgr.ErrApplyFailed), http.StatusBadGateway, "apply-failed"},
		{"recovery required", fmt.Errorf("%w: boom", configmgr.ErrRecoveryRequired), http.StatusInternalServerError, "recovery-required"},
		{"the file changed under it", fmt.Errorf("%w", configmgr.ErrConflict), http.StatusConflict, "conflict"},
		{"rejected by caddy", fmt.Errorf("%w: %w", configmgr.ErrInvalid, &caddy.AdaptError{Message: "bad", Line: 1}), http.StatusUnprocessableEntity, "invalid"},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			e := newEditor()
			e.rollbackErr = tt.err
			rec := postJSON(t, editHandler(t, e), "/api/revisions/3/rollback", "")
			if rec.Code != tt.status || fields(t, rec)["code"] != tt.code {
				t.Errorf("status = %d, body %s; want %d with code %q", rec.Code, rec.Body, tt.status, tt.code)
			}
		})
	}
}

func TestRevisionStoreFailureHidesTheCause(t *testing.T) {
	h := editHandler(t, failingRevisions{newEditor()})
	for _, path := range []string{"/api/revisions", "/api/revisions/1"} {
		rec := do(t, h, http.MethodGet, path)
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), errStore.Error()) {
			t.Errorf("GET %s = %d %s, want a 500 without the cause", path, rec.Code, rec.Body)
		}
	}
}

type failingRevisions struct{ *fakeEditor }

func (failingRevisions) Revision(context.Context, int64) (store.Revision, error) {
	return store.Revision{}, errStore
}

func (failingRevisions) Revisions(context.Context, store.RevisionQuery) ([]store.Revision, error) {
	return nil, errStore
}

// The responses are JSON like every other route.
func TestEditingRoutesAnswerJSONWithoutCaching(t *testing.T) {
	h := editHandler(t, newEditor())
	rec := do(t, h, http.MethodGet, "/api/caddyfile")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("headers = %v, want JSON and no-store", rec.Header())
	}
	var anything json.RawMessage
	decode(t, rec, &anything)
}
