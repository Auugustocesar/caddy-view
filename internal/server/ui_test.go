package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// uiStub stands in for the embedded UI: it answers every path it receives with a recognisable page.
var uiStub = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte("ui:" + r.URL.Path))
})

func uiHandler() http.Handler {
	return New(&fakeCaddy{cfg: map[string]any{}}, Options{Endpoint: testEndpoint, UI: uiStub})
}

func doTo(h http.Handler, method, host, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://"+host+path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPathsOutsideTheAPIGoToTheUI(t *testing.T) {
	for _, path := range []string{"/", "/config", "/logs", "/assets/app.js"} {
		t.Run(path, func(t *testing.T) {
			rec := doTo(uiHandler(), http.MethodGet, "localhost:8484", path)
			if rec.Code != http.StatusOK || rec.Body.String() != "ui:"+path {
				t.Errorf("status %d, body %q, want the UI to answer %s", rec.Code, rec.Body.String(), path)
			}
		})
	}
}

func TestTheUIKeepsItsOwnHeaders(t *testing.T) {
	h := doTo(uiHandler(), http.MethodGet, "localhost:8484", "/").Header()
	if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want the UI's text/html and not the API's JSON", ct)
	}
	if cc := h.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want the UI's no-cache and not the API's no-store", cc)
	}
}

func TestAnUnknownAPIPathIsNeverTheUI(t *testing.T) {
	for _, path := range []string{"/api/nope", "/api/", "/api"} {
		t.Run(path, func(t *testing.T) {
			rec := doTo(uiHandler(), http.MethodGet, "localhost:8484", path)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want a JSON error", ct)
			}
		})
	}
}

func TestTheAPIStillWinsOverTheUI(t *testing.T) {
	rec := doTo(uiHandler(), http.MethodGet, "localhost:8484", "/api/status")
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want the API answer", ct)
	}
}

func TestTheUIIsOnlyServedToLoopbackHosts(t *testing.T) {
	rec := doTo(uiHandler(), http.MethodGet, "evil.example.com", "/")
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a foreign Host", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "ui:") {
		t.Errorf("body %q leaks the UI to a foreign Host", rec.Body.String())
	}
}

func TestWithoutAUIOtherPathsAre404(t *testing.T) {
	rec := doTo(newHandler(&fakeCaddy{}), http.MethodGet, "localhost:8484", "/")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no UI is configured", rec.Code)
	}
}
