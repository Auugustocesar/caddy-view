package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestFormatRouteIsAbsentWithoutAnEditor(t *testing.T) {
	rec := postJSON(t, New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint}), "/api/caddyfile/format", `{"content":"a"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestFormatLaysTheCaddyfileOut(t *testing.T) {
	e := newEditor()
	rec := postJSON(t, editHandler(t, e), "/api/caddyfile/format", `{"content":"example.com{\nrespond   \"hi\"\n}"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	got := fields(t, rec)
	if got["content"] != "example.com {\n\trespond \"hi\"\n}\n" {
		t.Errorf("content = %q, want the text laid out", got["content"])
	}
	if got["changed"] != true {
		t.Errorf("changed = %v, want true", got["changed"])
	}
}

func TestFormatSaysWhenThereWasNothingToChange(t *testing.T) {
	rec := postJSON(t, editHandler(t, newEditor()), "/api/caddyfile/format", `{"content":"example.com {\n\trespond \"hi\"\n}\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if got := fields(t, rec); got["changed"] != false {
		t.Errorf("changed = %v, want false for text that is already laid out", got["changed"])
	}
}

func TestFormatTouchesNothing(t *testing.T) {
	e := newEditor()
	postJSON(t, editHandler(t, e), "/api/caddyfile/format", `{"content":"a{\nb\n}"}`)
	if len(e.applied) != 0 || len(e.validated) != 0 || len(e.rolledBack) != 0 {
		t.Errorf("formatting called the editor: applied %d, validated %d, rolled back %d", len(e.applied), len(e.validated), len(e.rolledBack))
	}
}

func TestFormatRefusesWhatItCannotRead(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		body    string
		headers map[string]string
		status  int
	}{
		{"not JSON", http.MethodPost, `nope`, jsonType, 400},
		{"no content", http.MethodPost, `{}`, jsonType, 400},
		{"unknown field", http.MethodPost, `{"content":"a","indent":2}`, jsonType, 400},
		{"too large", http.MethodPost, `{"content":"` + strings.Repeat("a", 1<<20+1) + `"}`, jsonType, 413},
		{"no content type", http.MethodPost, `{"content":"a"}`, nil, 415},
		{"GET", http.MethodGet, ``, nil, 405},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := send(t, editHandler(t, newEditor()), tt.method, "/api/caddyfile/format", tt.body, tt.headers)
			if rec.Code != tt.status {
				t.Errorf("status = %d, want %d: %.200s", rec.Code, tt.status, rec.Body)
			}
		})
	}
}

func TestFormatComesFromTheSameOriginOnly(t *testing.T) {
	headers := map[string]string{"Content-Type": "application/json", "Origin": "http://evil.example"}
	rec := send(t, editHandler(t, newEditor()), http.MethodPost, "/api/caddyfile/format", `{"content":"a"}`, headers)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
