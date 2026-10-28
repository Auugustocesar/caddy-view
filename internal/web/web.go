// Package web serves the single-page UI that is embedded in the binary.
package web

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// The UI is built from web/ into dist by `npm run build`, and the result is committed so that `go install` works.
//
//go:embed all:dist
var embedded embed.FS

const (
	indexFile   = "index.html"
	assetsDir   = "assets"
	hashedCache = "public, max-age=31536000, immutable"
	revalidate  = "no-cache"
	htmlType    = "text/html; charset=utf-8"
)

// The policy lets the page load only its own script and style, and talk only to its own origin. Nothing in the UI
// needs more, and it keeps a stray log line or config value from becoming a script. The one addition is a nonce for
// style: the code editor puts <style> elements in the page, and a nonce that is new with every response admits
// those and no other inline style. %s is where it goes.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self' 'nonce-%s'; connect-src 'self'; " +
	"img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// noncePlaceholder is what index.html holds where the nonce goes; the page reads it from a <meta> tag.
const noncePlaceholder = "__CSP_NONCE__"

// newNonce returns 128 random bits, in the alphabet that is safe in a header and in an attribute.
func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Handler serves the UI that was built into the binary.
func Handler() http.Handler {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic("web: " + err.Error()) // the name is a constant that the embed directive above guarantees
	}
	return NewHandler(sub)
}

// NewHandler serves the UI files in fsys: index.html at the root and as the fallback for client-side routes, and
// everything else as static files. A missing file that looks like a file (it has an extension, or lives under
// assets/) is a 404, so a broken script reference is not hidden behind an HTML page.
func NewHandler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		nonce, err := newNonce()
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		h.Set("Content-Security-Policy", fmt.Sprintf(contentSecurityPolicy, nonce))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Cleaning a rooted path removes every "..", so the name cannot point outside fsys.
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = indexFile
		}
		data, err := fs.ReadFile(fsys, name)
		switch {
		case err == nil:
		case !errors.Is(err, fs.ErrNotExist) && !isDirectory(fsys, name):
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		case looksLikeAFile(name):
			http.NotFound(w, r)
			return
		default:
			name = indexFile
			if data, err = fs.ReadFile(fsys, name); err != nil {
				http.NotFound(w, r)
				return
			}
		}

		if name == indexFile {
			data = bytes.ReplaceAll(data, []byte(noncePlaceholder), []byte(nonce))
		}
		h.Set("Content-Type", contentType(name))
		if strings.HasPrefix(name, assetsDir+"/") {
			h.Set("Cache-Control", hashedCache)
		} else {
			h.Set("Cache-Control", revalidate)
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}

// isDirectory reports whether name is a directory in fsys, which ReadFile reports as an error.
func isDirectory(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && info.IsDir()
}

func looksLikeAFile(name string) bool {
	return path.Ext(name) != "" || name == assetsDir || strings.HasPrefix(name, assetsDir+"/")
}

// contentType is set explicitly because on Windows the system registry can change what the standard library
// answers for .js and .css.
func contentType(name string) string {
	switch path.Ext(name) {
	case ".html":
		return htmlType
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}
