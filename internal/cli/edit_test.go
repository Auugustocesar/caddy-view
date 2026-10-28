package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	siteOne = "one.example.com {\n\trespond \"one\"\n}\n"
	siteTwo = "two.example.com {\n\trespond \"two\"\n}\n"
)

func digestOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// editAdmin is a Caddy Admin API that behaves like Caddy where it matters: a Caddyfile adapts to a configuration that
// holds its text, a load makes that configuration the running one, and /config/ returns the running one. It
// remembers the loads.
type editAdmin struct {
	*httptest.Server
	mu      sync.Mutex
	loads   []string
	running string // the JSON /config/ returns; goodConfig until something is loaded
}

func adapted(caddyfile string) string {
	b, _ := json.Marshal(map[string]any{"src": caddyfile})
	return string(b)
}

func newEditAdmin(t *testing.T) *editAdmin {
	t.Helper()
	a := &editAdmin{running: goodConfig}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/config/":
			a.mu.Lock()
			defer a.mu.Unlock()
			io.WriteString(w, a.running)
		case "/adapt":
			io.WriteString(w, `{"result":`+adapted(string(body))+`,"warnings":[]}`)
		case "/load":
			a.mu.Lock()
			a.loads = append(a.loads, string(body))
			a.running = adapted(string(body))
			a.mu.Unlock()
			io.WriteString(w, "{}")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(a.Close)
	return a
}

func (a *editAdmin) loaded() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.loads...)
}

// runBriefly runs the command for a short while. A serve that starts instead of refusing its flags gives up with
// exit code 0 when the time is over, so a missing refusal fails the test instead of hanging it.
func runBriefly(t *testing.T, args ...string) (code int, stderr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	code = Run(ctx, args, &out, &errOut)
	return code, errOut.String()
}

func writeCaddyfile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestServeRefusesEditingThatCannotBeDoneSafely(t *testing.T) {
	admin := newEditAdmin(t)
	dir := t.TempDir()
	file := writeCaddyfile(t, siteOne)
	base := []string{"serve", "--listen", "127.0.0.1:0", "--admin", admin.URL}
	tests := []struct {
		name  string
		args  []string
		names string // the flag the message must name
	}{
		{"no Caddyfile to edit", []string{"--allow-edit", "--data-dir", dir}, "--caddyfile"},
		{"history only in memory", []string{"--allow-edit", "--caddyfile", file, "--no-persist"}, "--no-persist"},
		{"the Caddyfile is a directory", []string{"--allow-edit", "--caddyfile", dir, "--data-dir", dir}, "--caddyfile"},
		{"the Caddyfile does not exist", []string{"--allow-edit", "--caddyfile", filepath.Join(dir, "missing"), "--data-dir", dir}, "--caddyfile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stderr := runBriefly(t, append(append([]string(nil), base...), tt.args...)...)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2\nstderr: %s", code, stderr)
			}
			if !strings.Contains(stderr, tt.names) {
				t.Errorf("stderr %q does not name %s", stderr, tt.names)
			}
		})
	}
}

// api talks to a running serve like a script would: JSON in, JSON out, no Origin header.
type api struct {
	t    *testing.T
	base string
}

func (a api) call(method, path, body string) (int, map[string]any) {
	a.t.Helper()
	req, err := http.NewRequest(method, a.base+path, strings.NewReader(body))
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		a.t.Fatalf("%s %s: the answer is not JSON: %v", method, path, err)
	}
	return res.StatusCode, out
}

func TestServeLeavesTheCaddyfileAloneWithoutAllowEdit(t *testing.T) {
	admin := newEditAdmin(t)
	file := writeCaddyfile(t, siteOne)
	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file)
	defer stop()

	a := api{t, base}
	for _, path := range []string{"/api/caddyfile", "/api/revisions"} {
		if status, _ := a.call(http.MethodGet, path, ""); status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 without --allow-edit", path, status)
		}
	}
}

func TestServeEditsTheCaddyfileThroughTheAPI(t *testing.T) {
	admin := newEditAdmin(t)
	file := writeCaddyfile(t, siteOne)
	dataDir := t.TempDir()
	args := []string{"--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", dataDir}

	base, stop, _, _ := startServe(t, args...)
	a := api{t, base}

	if status, got := a.call(http.MethodGet, "/api/caddyfile", ""); status != http.StatusOK || got["content"] != siteOne || got["sha256"] != digestOf(siteOne) {
		t.Fatalf("GET /api/caddyfile = %d %v, want the file", status, got)
	}

	apply := func(content, baseDigest string) (int, map[string]any) {
		b, _ := json.Marshal(map[string]string{"content": content, "baseSha256": baseDigest})
		return a.call(http.MethodPost, "/api/caddyfile/apply", string(b))
	}
	if status, got := apply(siteTwo, digestOf(siteOne)); status != http.StatusOK {
		t.Fatalf("apply = %d %v, want 200", status, got)
	}
	if onDisk, _ := os.ReadFile(file); string(onDisk) != siteTwo {
		t.Errorf("the Caddyfile on disk = %q, want the applied content", onDisk)
	}
	if got := admin.loaded(); len(got) != 1 || got[0] != siteTwo {
		t.Errorf("caddy loaded %q, want the applied content once", got)
	}

	// The same base again is stale now: the file moved on.
	if status, got := apply(siteOne, digestOf(siteOne)); status != http.StatusConflict || got["code"] != "conflict" {
		t.Errorf("a stale apply = %d %v, want 409 conflict", status, got)
	}

	// Roll back to revision 1, which holds the content applied first.
	if status, got := a.call(http.MethodPost, "/api/revisions/1/rollback", ""); status != http.StatusOK {
		t.Fatalf("rollback = %d %v, want 200", status, got)
	}
	if onDisk, _ := os.ReadFile(file); string(onDisk) != siteTwo {
		t.Errorf("after rolling back to revision 1 the file = %q, want %q", onDisk, siteTwo)
	}

	if code := stop(); code != 0 {
		t.Fatalf("serve exited with %d", code)
	}

	// The revisions are in the database, so a new process sees them.
	base, stop, _, _ = startServe(t, args...)
	defer stop()
	a = api{t, base}
	status, got := a.call(http.MethodGet, "/api/revisions", "")
	revs, _ := got["revisions"].([]any)
	if status != http.StatusOK || len(revs) != 2 {
		t.Errorf("after a restart GET /api/revisions = %d with %d revisions, want 2", status, len(revs))
	}
}

func TestServeWarnsWhenTheCaddyfileChangedOutsideCaddyView(t *testing.T) {
	admin := newEditAdmin(t)
	file := writeCaddyfile(t, siteOne)
	dataDir := t.TempDir()
	args := []string{"--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", dataDir}

	base, stop, _, _ := startServe(t, args...)
	b, _ := json.Marshal(map[string]string{"content": siteTwo, "baseSha256": digestOf(siteOne)})
	if status, got := (api{t, base}).call(http.MethodPost, "/api/caddyfile/apply", string(b)); status != http.StatusOK {
		t.Fatalf("apply = %d %v", status, got)
	}
	stop()

	if err := os.WriteFile(file, []byte(siteOne), 0o644); err != nil { // someone edits it by hand
		t.Fatal(err)
	}
	_, stop, _, stderr := startServe(t, args...)
	defer stop()
	if log := stderr.String(); !strings.Contains(log, "WARN") || !strings.Contains(log, filepath.Base(file)) {
		t.Errorf("stderr = %q, want a warning that names the Caddyfile", log)
	}
}
