package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

const (
	indexHTML = `<!doctype html><title>caddy-view</title><div id="app"></div>`
	appJS     = `console.log("app")`
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte(indexHTML)},
		"assets/app-3f2a1b.js": {Data: []byte(appJS)},
		"favicon.svg":          {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
	}
}

func get(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	NewHandler(testFS()).ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestServesTheIndexAtTheRoot(t *testing.T) {
	rec := get(t, http.MethodGet, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != indexHTML {
		t.Errorf("body = %q, want the index page", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache so a new build is picked up", cc)
	}
}

func TestServesTheIndexForClientSideRoutes(t *testing.T) {
	for _, path := range []string{"/config", "/logs", "/logs/", "/some/deep/route"} {
		t.Run(path, func(t *testing.T) {
			rec := get(t, http.MethodGet, path)
			if rec.Code != http.StatusOK || rec.Body.String() != indexHTML {
				t.Errorf("status %d, body %q, want the index page so a reload keeps the tab", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestServesHashedAssetsWithALongCache(t *testing.T) {
	rec := get(t, http.MethodGet, "/assets/app-3f2a1b.js")
	if rec.Code != http.StatusOK || rec.Body.String() != appJS {
		t.Fatalf("status %d, body %q, want the asset", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want JavaScript", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want a year and immutable for a hashed asset", cc)
	}
}

func TestServesOtherFilesWithoutALongCache(t *testing.T) {
	rec := get(t, http.MethodGet, "/favicon.svg")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache for a file whose name has no hash", cc)
	}
}

func TestAMissingFileIsNotTheIndex(t *testing.T) {
	for _, path := range []string{"/assets/missing.js", "/nothing.css", "/assets/"} {
		t.Run(path, func(t *testing.T) {
			if rec := get(t, http.MethodGet, path); rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404 (an HTML page where a script is expected hides the real error)", rec.Code)
			}
		})
	}
}

func TestOnlyReadMethodsAreAllowed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			rec := get(t, method, "/")
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
				t.Errorf("Allow = %q, want %q", allow, "GET, HEAD")
			}
		})
	}
	t.Run("HEAD has no body", func(t *testing.T) {
		rec := get(t, http.MethodHead, "/")
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Errorf("status %d with %d body bytes, want 200 and none", rec.Code, rec.Body.Len())
		}
	})
}

func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	for _, path := range []string{"/", "/assets/app-3f2a1b.js", "/config", "/assets/missing.js"} {
		t.Run(path, func(t *testing.T) {
			h := get(t, http.MethodGet, path).Header()
			csp := h.Get("Content-Security-Policy")
			for _, want := range []string{
				"default-src 'none'", "script-src 'self'", "style-src 'self'", "connect-src 'self'",
				"base-uri 'none'", "form-action 'none'", "frame-ancestors 'none'",
			} {
				if !strings.Contains(csp, want) {
					t.Errorf("Content-Security-Policy %q lacks %q", csp, want)
				}
			}
			for _, bad := range []string{"unsafe-inline", "unsafe-eval", "http:", "https:", "*"} {
				if strings.Contains(csp, bad) {
					t.Errorf("Content-Security-Policy %q must not contain %q", csp, bad)
				}
			}
			want := map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Referrer-Policy":        "no-referrer",
				"X-Frame-Options":        "DENY",
			}
			for name, value := range want {
				if got := h.Get(name); got != value {
					t.Errorf("%s = %q, want %q", name, got, value)
				}
			}
		})
	}
}

func TestPathTraversalCannotLeaveTheUIFiles(t *testing.T) {
	for _, path := range []string{"/../go.mod", "/assets/../../go.mod", "/%2e%2e/go.mod", "/assets/..%2f..%2fgo.mod"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.URL.Path = path
			NewHandler(testFS()).ServeHTTP(rec, req)
			if body := rec.Body.String(); strings.Contains(body, "module ") {
				t.Errorf("leaked a file outside the UI: %q", body)
			}
		})
	}
}

func TestTheEmbeddedUIHasAnIndexPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<div id=\"app\">") {
		t.Errorf("status %d, body %q, want the built index page with the app mount point", rec.Code, rec.Body.String())
	}
}

// ---- the style nonce

// The editor puts <style> elements in the page, which `style-src 'self'` forbids. A nonce that changes with every
// response, written in the page and in the policy, lets those elements in and nothing else.

const nonceHTML = `<!doctype html><meta name="csp-nonce" content="__CSP_NONCE__"><div id="app"></div>`

func nonceGet(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	fsys := fstest.MapFS{
		"index.html":           {Data: []byte(nonceHTML)},
		"assets/app-3f2a1b.js": {Data: []byte(`const mark = "__CSP_NONCE__"`)},
	}
	rec := httptest.NewRecorder()
	NewHandler(fsys).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

var (
	policyNonce = regexp.MustCompile(`style-src 'self' 'nonce-([A-Za-z0-9_-]+)'`)
	pageNonce   = regexp.MustCompile(`<meta name="csp-nonce" content="([A-Za-z0-9_-]+)"\s*/?>`)
)

func TestThePolicyAndThePageShareAStyleNonce(t *testing.T) {
	rec := nonceGet(t, "/")
	inPolicy := policyNonce.FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))
	inPage := pageNonce.FindStringSubmatch(rec.Body.String())
	if inPolicy == nil || inPage == nil {
		t.Fatalf("policy %q, page %q: want a nonce in both", rec.Header().Get("Content-Security-Policy"), rec.Body.String())
	}
	if inPolicy[1] != inPage[1] {
		t.Errorf("nonce in the policy %q, in the page %q, want the same", inPolicy[1], inPage[1])
	}
	if strings.Contains(rec.Body.String(), "__CSP_NONCE__") {
		t.Error("the placeholder is still in the page")
	}
}

func TestEveryRouteOfTheUIGetsTheNonceToo(t *testing.T) {
	rec := nonceGet(t, "/caddyfile")
	if pageNonce.FindStringSubmatch(rec.Body.String()) == nil || strings.Contains(rec.Body.String(), "__CSP_NONCE__") {
		t.Errorf("body %q, want the nonce in the page served for a client-side route", rec.Body.String())
	}
}

func TestTheNonceChangesWithEveryResponse(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		nonce := pageNonce.FindStringSubmatch(nonceGet(t, "/").Body.String())[1]
		if len(nonce) < 22 { // 16 random bytes, base64 without padding
			t.Fatalf("nonce %q is shorter than 128 bits", nonce)
		}
		if seen[nonce] {
			t.Fatalf("nonce %q was served twice", nonce)
		}
		seen[nonce] = true
	}
}

func TestOnlyTheIndexPageIsRewritten(t *testing.T) {
	rec := nonceGet(t, "/assets/app-3f2a1b.js")
	if body := rec.Body.String(); body != `const mark = "__CSP_NONCE__"` {
		t.Errorf("a script was rewritten: %q", body)
	}
}

func TestAPageWithoutAPlaceholderIsServedAsItIs(t *testing.T) {
	if rec := get(t, http.MethodGet, "/"); rec.Body.String() != indexHTML {
		t.Errorf("body = %q, want the page untouched", rec.Body.String())
	}
}

func TestTheNonceDoesNotLoosenTheRestOfThePolicy(t *testing.T) {
	csp := nonceGet(t, "/").Header().Get("Content-Security-Policy")
	for _, bad := range []string{"unsafe-inline", "unsafe-eval", "http:", "https:", "*"} {
		if strings.Contains(csp, bad) {
			t.Errorf("Content-Security-Policy %q must not contain %q", csp, bad)
		}
	}
	if strings.Contains(csp, "script-src 'self' 'nonce") {
		t.Errorf("Content-Security-Policy %q gives scripts a nonce, which they do not need", csp)
	}
}

func TestTheEmbeddedIndexPageCarriesTheNoncePlaceholder(t *testing.T) {
	// The build must leave the placeholder for the handler to fill, or the editor's styles are blocked again.
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if pageNonce.FindStringSubmatch(rec.Body.String()) == nil {
		t.Errorf("body %q, want a csp-nonce meta tag with a nonce", rec.Body.String())
	}
}
