package server

import (
	"net/http"
	"testing"
)

func TestAllowedHostsAreAcceptedBesideLoopback(t *testing.T) {
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint, AllowedHosts: []string{"View.Example.com", "10.0.0.5"}, UI: uiStub})
	cases := map[string]int{
		"view.example.com":           http.StatusOK, // the list is not case sensitive, and neither is the header
		"VIEW.EXAMPLE.COM:8443":      http.StatusOK, // a port is not part of the name
		"10.0.0.5:8484":              http.StatusOK,
		"localhost:8484":             http.StatusOK,
		"127.0.0.1":                  http.StatusOK,
		"other.example.com":          http.StatusForbidden,
		"view.example.com.evil.test": http.StatusForbidden,
		"evil.test":                  http.StatusForbidden,
		"10.0.0.6":                   http.StatusForbidden,
		"":                           http.StatusForbidden,
		"xview.example.com":          http.StatusForbidden,
	}
	for host, want := range cases {
		for _, path := range []string{"/api/status", "/"} { // the UI is behind the same check
			if got := doTo(h, http.MethodGet, host, path).Code; got != want {
				t.Errorf("GET %s with Host %q = %d, want %d", path, host, got, want)
			}
		}
	}
}

func TestWithoutAllowedHostsOnlyLoopbackIsAccepted(t *testing.T) {
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint})
	if got := doTo(h, http.MethodGet, "view.example.com", "/api/status").Code; got != http.StatusForbidden {
		t.Errorf("status = %d, want 403", got)
	}
}
