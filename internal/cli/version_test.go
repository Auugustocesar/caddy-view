package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestVersionPrintsTheVersion(t *testing.T) {
	code, stdout, stderr := run(t, "version")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q, want 0 and nothing", code, stderr)
	}
	if !strings.HasPrefix(stdout, "caddy-view ") || strings.TrimSpace(stdout) == "caddy-view" {
		t.Errorf("stdout = %q, want %q followed by a version", stdout, "caddy-view ")
	}
}

func TestVersionComesFromTheBuildWhenSet(t *testing.T) {
	old := version
	version = "1.2.3"
	t.Cleanup(func() { version = old })
	if _, stdout, _ := run(t, "version"); strings.TrimSpace(stdout) != "caddy-view 1.2.3" {
		t.Errorf("stdout = %q, want the version set at build time", stdout)
	}
}

func TestUsageListsVersion(t *testing.T) {
	if _, stdout, _ := run(t, "help"); !strings.Contains(stdout, "version") {
		t.Errorf("usage %q does not list version", stdout)
	}
}

func TestServeSecureCookieMarksTheSessionCookie(t *testing.T) {
	admin := fakeAdmin(t, goodConfig)
	base, stop, _, stderr := startServeWithLogin(t, "--admin", admin.URL, "--secure-cookie")
	defer stop()
	password := createdLine.FindStringSubmatch(stderr.String())[1]
	resp, err := http.Post(base+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"`+password+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cookies := resp.Cookies()
	if resp.StatusCode != http.StatusOK || len(cookies) != 1 || !cookies[0].Secure {
		t.Errorf("status %d, cookies %+v, want 200 and one Secure cookie", resp.StatusCode, cookies)
	}
}
