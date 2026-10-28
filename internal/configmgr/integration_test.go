//go:build integration

package configmgr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// These tests run a real Caddy. They need the binary in CADDY_BIN or on the PATH; without it they skip, unless
// CADDY_VIEW_REQUIRE_INTEGRATION=1, as in CI, where a missing binary is a failure. The Caddy runs with its admin API
// and its site on free loopback ports and with its own config and data directories, so nothing else is touched.

func caddyBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("CADDY_BIN")
	if bin == "" {
		bin, _ = exec.LookPath("caddy")
	}
	if bin == "" {
		if os.Getenv("CADDY_VIEW_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("no Caddy binary: set CADDY_BIN or put caddy on the PATH")
		}
		t.Skip("no Caddy binary: set CADDY_BIN or put caddy on the PATH")
	}
	return bin
}

// freePort returns a loopback port that nothing listens on now.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// realCaddy is a running Caddy with a Manager in front of it.
type realCaddy struct {
	mgr       *Manager
	client    *caddy.Client
	store     *store.Memory
	path      string
	adminAddr string
	sitePort  int
}

// caddyfile is a Caddyfile that keeps the admin where it is and answers body on the site port.
func (r *realCaddy) caddyfile(body string) string {
	return fmt.Sprintf("{\n\tadmin %s\n\tauto_https off\n}\n:%d {\n\trespond %q\n}\n", r.adminAddr, r.sitePort, body)
}

func startCaddy(t *testing.T, firstBody string) *realCaddy {
	t.Helper()
	bin := caddyBinary(t)
	dir := t.TempDir()
	r := &realCaddy{path: filepath.Join(dir, "Caddyfile"), sitePort: freePort(t), store: store.NewMemory()}
	r.adminAddr = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	if err := os.WriteFile(r.path, []byte(r.caddyfile(firstBody)), 0o644); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	cmd := exec.Command(bin, "run", "--config", r.path, "--adapter", "caddyfile")
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = &logs, &logs
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+filepath.Join(dir, "config"), "XDG_DATA_HOME="+filepath.Join(dir, "data"),
		"APPDATA="+filepath.Join(dir, "config"), "LOCALAPPDATA="+filepath.Join(dir, "data"))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start caddy: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill() // the process may be gone already
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("caddy output:\n%s", logs.String())
		}
	})

	client, err := caddy.New("http://" + r.adminAddr)
	if err != nil {
		t.Fatal(err)
	}
	r.client = client
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := client.Config(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("caddy did not come up\n%s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.mgr = New(client, r.store, Options{Path: r.path})
	return r
}

// serves reports what the site answers now.
func (r *realCaddy) serves(t *testing.T) string {
	t.Helper()
	res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", r.sitePort))
	if err != nil {
		t.Fatalf("the site does not answer: %v", err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func (r *realCaddy) reachable(t *testing.T) {
	t.Helper()
	if _, err := r.client.Config(context.Background()); err != nil {
		t.Fatalf("caddy is not reachable on %s any more: %v", r.adminAddr, err)
	}
}

func (r *realCaddy) onDisk(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *realCaddy) apply(t *testing.T, content string) (store.Revision, error) {
	t.Helper()
	cur, err := r.mgr.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r.mgr.Apply(context.Background(), ApplyRequest{Content: content, BaseSHA256: cur.SHA256})
}

func TestRealCaddyServesAValidChangeAtOnce(t *testing.T) {
	r := startCaddy(t, "v0")
	if got := r.serves(t); got != "v0" {
		t.Fatalf("the site answers %q before any change, want v0", got)
	}
	rev, err := r.apply(t, r.caddyfile("v1"))
	if err != nil {
		t.Fatal(err)
	}
	if rev.Status != store.StatusApplied || r.serves(t) != "v1" || r.onDisk(t) != r.caddyfile("v1") {
		t.Errorf("revision %+v, site %q: want applied, serving v1 and the file written", rev, r.serves(t))
	}
}

func TestRealCaddyRejectsASyntaxErrorAndKeepsServing(t *testing.T) {
	r := startCaddy(t, "v0")
	broken := fmt.Sprintf("{\n\tadmin %s\n}\n:%d {\n\tnot_a_directive\n}\n", r.adminAddr, r.sitePort)

	rev, err := r.apply(t, broken)
	var adaptErr *caddy.AdaptError
	if !errors.Is(err, ErrInvalid) || !errors.As(err, &adaptErr) || adaptErr.Line == 0 {
		t.Fatalf("Apply() error = %v, want ErrInvalid with a line", err)
	}
	if rev.Status != store.StatusFailed || r.onDisk(t) != r.caddyfile("v0") || r.serves(t) != "v0" {
		t.Errorf("revision %+v, file and site changed: want failed, the old file and v0", rev)
	}
}

func TestRealCaddyUndoesAChangeItCannotLoad(t *testing.T) {
	r := startCaddy(t, "v0")
	// The candidate adapts fine, because adapting reads no files, and fails only when Caddy loads the certificate.
	unloadable := fmt.Sprintf("{\n\tadmin %s\n\tauto_https off\n}\n:%d {\n\ttls /no/such/cert.pem /no/such/key.pem\n\trespond \"v0\"\n}\n",
		r.adminAddr, r.sitePort)

	rev, err := r.apply(t, unloadable)
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("Apply() error = %v, want ErrApplyFailed", err)
	}
	if rev.Status != store.StatusFailed || r.onDisk(t) != r.caddyfile("v0") {
		t.Errorf("revision %+v, file %q: want failed and the old file back", rev, r.onDisk(t))
	}
	if r.serves(t) != "v0" {
		t.Errorf("the site answers %q, want v0 still", r.serves(t))
	}
	r.reachable(t)
}

func TestRealCaddyRollsBack(t *testing.T) {
	r := startCaddy(t, "v0")
	for _, body := range []string{"v1", "v2"} {
		if _, err := r.apply(t, r.caddyfile(body)); err != nil {
			t.Fatal(err)
		}
	}
	rev, err := r.mgr.Rollback(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Trigger != store.TriggerRollback || r.serves(t) != "v1" || r.onDisk(t) != r.caddyfile("v1") {
		t.Errorf("revision %+v, site %q: want a rollback revision serving v1", rev, r.serves(t))
	}
}

func TestRealCaddyNoticesAnEditMadeOutside(t *testing.T) {
	r := startCaddy(t, "v0")
	cur, err := r.mgr.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.apply(t, r.caddyfile("v1")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.path, []byte(r.caddyfile("by-hand")), 0o644); err != nil {
		t.Fatal(err)
	}

	// An editor that still holds the file as it was before everything is stale.
	if _, err := r.mgr.Apply(context.Background(), ApplyRequest{Content: r.caddyfile("v2"), BaseSHA256: cur.SHA256}); !errors.Is(err, ErrConflict) {
		t.Errorf("Apply() error = %v, want ErrConflict", err)
	}
	d, err := r.mgr.Reconcile(context.Background())
	if err != nil || !d.Drifted {
		t.Errorf("Reconcile() = %+v, %v, want drift", d, err)
	}
	if r.serves(t) != "v1" {
		t.Errorf("the site answers %q, want v1: nothing was loaded", r.serves(t))
	}
}

func TestRealCaddyStaysReachableWhenAChangeWouldMoveTheAdmin(t *testing.T) {
	r := startCaddy(t, "v0")
	// The same site, but the admin line is gone, so Caddy would fall back to localhost:2019.
	noAdmin := fmt.Sprintf("{\n\tauto_https off\n}\n:%d {\n\trespond \"v1\"\n}\n", r.sitePort)

	rev, err := r.apply(t, noAdmin)
	if !errors.Is(err, ErrAdminMoved) {
		t.Fatalf("Apply() error = %v, want ErrAdminMoved", err)
	}
	if rev.Status != store.StatusFailed || r.onDisk(t) != r.caddyfile("v0") || r.serves(t) != "v0" {
		t.Errorf("revision %+v: want failed, the old file and v0", rev)
	}
	r.reachable(t)
}
