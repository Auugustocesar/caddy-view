package cli

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/instancelock"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// lockFile is where serve keeps the lock of a Caddyfile it may edit: next to it, since that is the directory serve
// already needs to write in.
func lockFile(caddyfile string) string {
	return filepath.Join(filepath.Dir(caddyfile), ".caddy-view.lock")
}

func TestServeRefusesToEditWhileAnotherProcessHoldsTheCaddyfile(t *testing.T) {
	admin := newEditAdmin(t)
	file := writeCaddyfile(t, siteOne)
	held, err := instancelock.Acquire(context.Background(), lockFile(file))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	code, stderr := runBriefly(t, "serve", "--listen", "127.0.0.1:0", "--admin", admin.URL,
		"--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir())
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "Caddyfile") {
		t.Errorf("stderr %q does not say which Caddyfile is taken", stderr)
	}
}

func TestServeDoesNotLockAnythingWithoutAllowEdit(t *testing.T) {
	admin := newEditAdmin(t)
	file := writeCaddyfile(t, siteOne)
	held, err := instancelock.Acquire(context.Background(), lockFile(file))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	_, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file) // read-only: nothing to protect
	if code := stop(); code != 0 {
		t.Errorf("serve exited with %d", code)
	}
}

func TestServeReleasesTheCaddyfileWhenItStops(t *testing.T) {
	admin := newEditAdmin(t)
	file := writeCaddyfile(t, siteOne)
	_, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", t.TempDir())
	if code := stop(); code != 0 {
		t.Fatalf("serve exited with %d", code)
	}
	l, err := instancelock.Acquire(context.Background(), lockFile(file))
	if err != nil {
		t.Fatalf("the lock is still held after serve stopped: %v", err)
	}
	l.Release()
}

func TestServeResolvesRevisionsLeftUnfinishedByACrash(t *testing.T) {
	ctx := context.Background()
	admin := newEditAdmin(t)
	file := writeCaddyfile(t, siteOne)
	dataDir := t.TempDir()

	// What a process killed in the middle of two applies would have left in the database.
	db, err := store.OpenSQLite(ctx, filepath.Join(dataDir, "caddy-view.db"))
	if err != nil {
		t.Fatal(err)
	}
	add := func(content string, status store.RevisionStatus) {
		t.Helper()
		rev, err := db.AddRevision(ctx, store.Revision{
			Time: time.Now(), Trigger: store.TriggerManualEdit, Status: store.StatusPending, Content: content, SHA256: digestOf(content),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.UpdateRevision(ctx, rev.Number, status, ""); err != nil {
			t.Fatal(err)
		}
	}
	add(siteTwo, store.StatusPending) // 1: never reached the disk
	add(siteOne, store.StatusLoading) // 2: the file is this, and Caddy (the fake) runs something else
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	base, stop, _, _ := startServe(t, "--admin", admin.URL, "--caddyfile", file, "--allow-edit", "--data-dir", dataDir)
	defer stop()

	a := api{t, base}
	for number, want := range map[string]string{"1": "failed", "2": "recovery-required"} {
		status, got := a.call(http.MethodGet, "/api/revisions/"+number, "")
		if status != http.StatusOK || got["status"] != want {
			t.Errorf("revision %s = %d %v, want status %q", number, status, got, want)
		}
	}
}
