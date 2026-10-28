//go:build integration

package configmgr

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/Auugustocesar/caddy-view/internal/sites"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

// getWith asks the site that answers to host, with the credentials given (none when user is empty), and returns the
// status, the WWW-Authenticate header and what the site said.
func (m *managedCaddy) getWith(t *testing.T, host, user, password string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", m.httpPort), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", host, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, res.Header.Get("WWW-Authenticate"), strings.TrimSpace(string(b))
}

func passwordProtected(t *testing.T, id, host, upstream, user, password string) store.Site {
	t.Helper()
	s := httpSite(id, host, upstream)
	if errs := sites.SetBasicAuth(&s, user, password, nil, bcrypt.MinCost); errs != nil {
		t.Fatalf("SetBasicAuth: %v", errs)
	}
	return s
}

func TestRealCaddyAsksForThePasswordOfAProtectedSite(t *testing.T) {
	ctx := context.Background()
	m := startManagedCaddy(t)
	backendAddr := backend(t, "the secret page")
	protected := passwordProtected(t, "a", "private.localhost", backendAddr, "bob", "correct horse battery")
	open := httpSite("b", "open.localhost", backendAddr)

	rev, err := m.mgr.ApplySites(ctx, []store.Site{protected, open})
	if err != nil || rev.Status != store.StatusApplied {
		t.Fatalf("ApplySites() = %+v, %v, want it applied", rev, err)
	}

	if status, challenge, body := m.getWith(t, "private.localhost", "", ""); status != http.StatusUnauthorized || !strings.HasPrefix(challenge, "Basic") || body == "the secret page" {
		t.Errorf("without credentials: %d %q %q, want 401 with a Basic challenge and not the page", status, challenge, body)
	}
	if status, _, body := m.getWith(t, "private.localhost", "bob", "correct horse battery"); status != http.StatusOK || body != "the secret page" {
		t.Errorf("with the right credentials: %d %q, want 200 and the page", status, body)
	}
	if status, _, body := m.getWith(t, "private.localhost", "bob", "wrong password!"); status != http.StatusUnauthorized || body == "the secret page" {
		t.Errorf("with a wrong password: %d %q, want 401", status, body)
	}
	if status, _, body := m.getWith(t, "private.localhost", "mallory", "correct horse battery"); status != http.StatusUnauthorized || body == "the secret page" {
		t.Errorf("with a wrong user: %d %q, want 401", status, body)
	}
	if status, _, body := m.getWith(t, "open.localhost", "", ""); status != http.StatusOK || body != "the secret page" {
		t.Errorf("the site without a password: %d %q, want 200 and the page", status, body)
	}
}

func TestRealCaddyKeepsEachSitesOwnPassword(t *testing.T) {
	ctx := context.Background()
	m := startManagedCaddy(t)
	backendAddr := backend(t, "page")
	a := passwordProtected(t, "a", "one.localhost", backendAddr, "alice", "alice's password")
	b := passwordProtected(t, "b", "two.localhost", backendAddr, "bob", "bob's password!!")
	if _, err := m.mgr.ApplySites(ctx, []store.Site{a, b}); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := m.getWith(t, "one.localhost", "alice", "alice's password"); status != http.StatusOK {
		t.Errorf("alice at her site: %d, want 200", status)
	}
	if status, _, _ := m.getWith(t, "one.localhost", "bob", "bob's password!!"); status != http.StatusUnauthorized {
		t.Errorf("bob at alice's site: %d, want 401", status)
	}
	if status, _, _ := m.getWith(t, "two.localhost", "bob", "bob's password!!"); status != http.StatusOK {
		t.Errorf("bob at his site: %d, want 200", status)
	}
}

func TestRealCaddyStopsAskingWhenThePasswordIsTakenAway(t *testing.T) {
	ctx := context.Background()
	m := startManagedCaddy(t)
	backendAddr := backend(t, "page")
	protected := passwordProtected(t, "a", "private.localhost", backendAddr, "bob", "correct horse battery")
	if _, err := m.mgr.ApplySites(ctx, []store.Site{protected}); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := m.getWith(t, "private.localhost", "", ""); status != http.StatusUnauthorized {
		t.Fatalf("before: %d, want 401", status)
	}
	rows, _ := m.store.Sites(ctx)
	rows[0].BasicAuthUser, rows[0].BasicAuthHash = "", ""
	if _, err := m.mgr.ApplySites(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if status, _, body := m.getWith(t, "private.localhost", "", ""); status != http.StatusOK || body != "page" {
		t.Errorf("after: %d %q, want 200 and the page", status, body)
	}
}

func TestRealCaddyRollsBackToTheSitesWithTheirPasswords(t *testing.T) {
	ctx := context.Background()
	m := startManagedCaddy(t)
	backendAddr := backend(t, "page")
	protected := passwordProtected(t, "a", "private.localhost", backendAddr, "bob", "correct horse battery")
	first, err := m.mgr.ApplySites(ctx, []store.Site{protected})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := m.store.Sites(ctx)
	rows[0].BasicAuthUser, rows[0].BasicAuthHash = "", ""
	if _, err := m.mgr.ApplySites(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := m.getWith(t, "private.localhost", "", ""); status != http.StatusOK {
		t.Fatalf("with the password taken away: %d, want 200", status)
	}

	if _, err := m.mgr.Rollback(ctx, first.Number); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := m.getWith(t, "private.localhost", "", ""); status != http.StatusUnauthorized {
		t.Errorf("after the roll back: %d, want 401: the revision must bring the password back", status)
	}
	if status, _, body := m.getWith(t, "private.localhost", "bob", "correct horse battery"); status != http.StatusOK || body != "page" {
		t.Errorf("with the credentials after the roll back: %d %q, want 200", status, body)
	}
	if got, _ := m.store.Sites(ctx); len(got) != 1 || got[0].BasicAuthUser != "bob" || bcrypt.CompareHashAndPassword([]byte(got[0].BasicAuthHash), []byte("correct horse battery")) != nil {
		t.Errorf("the rows after the roll back = %+v, want bob with his hash", got)
	}
}

func TestTheManagedFileHoldsTheHashAndNotThePassword(t *testing.T) {
	ctx := context.Background()
	m := startManagedCaddy(t)
	protected := passwordProtected(t, "a", "private.localhost", backend(t, "page"), "bob", "correct horse battery")
	if _, err := m.mgr.ApplySites(ctx, []store.Site{protected}); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(m.managed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), "correct horse battery") {
		t.Error("the managed file has the password")
	}
	if !strings.Contains(string(file), protected.BasicAuthHash) {
		t.Error("the managed file has no hash")
	}
}
