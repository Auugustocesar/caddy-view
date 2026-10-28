package server

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Auugustocesar/caddy-view/internal/sites"
	"github.com/Auugustocesar/caddy-view/internal/store"
)

const password = "correct horse battery"

// authHandler is a handler whose hashes cost as little as bcrypt allows, and whose log can be read.
func authHandler(t *testing.T, f *fakeSites, log *bytes.Buffer) http.Handler {
	t.Helper()
	opts := Options{Endpoint: testEndpoint, Sites: f, Now: func() time.Time { return clock }, BasicAuthCost: bcrypt.MinCost}
	if log != nil {
		opts.Logger = slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return New(&fakeCaddy{cfg: parse(t, `{}`)}, opts)
}

// protectedRows is the fake manager with a password on the first site, and the hash it has.
func protectedRows(t *testing.T) (*fakeSites, string) {
	t.Helper()
	f := newFakeSites()
	h, err := bcrypt.GenerateFromPassword([]byte("the old password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	f.rows[0].BasicAuthUser, f.rows[0].BasicAuthHash = "bob", string(h)
	return f, string(h)
}

func lastApplied(t *testing.T, f *fakeSites, host string) store.Site {
	t.Helper()
	if len(f.applied) == 0 {
		t.Fatal("nothing was applied")
	}
	for _, s := range f.applied[len(f.applied)-1] {
		if s.Hostname == host {
			return s
		}
	}
	t.Fatalf("the sites applied have no %s", host)
	return store.Site{}
}

func withAuth(user, password string) string {
	return `{"hostname":"c.example.com","kind":"proxy","upstreams":["10.0.0.7:9000"],"tls":"internal","basicAuth":{"user":"` + user + `","password":"` + password + `"}}`
}

func errorFields(t *testing.T, body map[string]any) map[string]string {
	t.Helper()
	list, _ := body["errors"].([]any)
	out := map[string]string{}
	for _, e := range list {
		m, _ := e.(map[string]any)
		out[m["field"].(string)], _ = m["message"].(string)
	}
	return out
}

// ---- what is answered

func TestCreateWithAPasswordStoresAHashOfItAndAnswersWithoutIt(t *testing.T) {
	f := newFakeSites()
	rec := postJSON(t, authHandler(t, f, nil), "/api/managed-sites", withAuth("bob", password))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	added := lastApplied(t, f, "c.example.com")
	if added.BasicAuthUser != "bob" {
		t.Errorf("BasicAuthUser = %q, want bob", added.BasicAuthUser)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(added.BasicAuthHash), []byte(password)); err != nil {
		t.Errorf("the hash applied is not of the password: %v", err)
	}
	body := rec.Body.String()
	if strings.Contains(body, password) || strings.Contains(body, added.BasicAuthHash) || strings.Contains(strings.ToLower(body), "hash") {
		t.Errorf("the answer gives away the password or its hash: %s", body)
	}
	site, _ := fields(t, rec)["site"].(map[string]any)
	auth, _ := site["basicAuth"].(map[string]any)
	if auth["user"] != "bob" || len(auth) != 1 {
		t.Errorf("site.basicAuth = %v, want only the user bob", site["basicAuth"])
	}
}

func TestTheListSaysWhichSitesHaveAPasswordAndNothingMore(t *testing.T) {
	f, hash := protectedRows(t)
	rec := do(t, authHandler(t, f, nil), http.MethodGet, "/api/managed-sites")
	list, _ := fields(t, rec)["sites"].([]any)
	protected, _ := list[0].(map[string]any)
	plain, _ := list[1].(map[string]any)
	if auth, _ := protected["basicAuth"].(map[string]any); auth["user"] != "bob" || len(auth) != 1 {
		t.Errorf("the protected site has basicAuth %v, want only the user bob", protected["basicAuth"])
	}
	if v, has := plain["basicAuth"]; !has || v != nil {
		t.Errorf("the plain site has basicAuth %v (present: %v), want null", v, has)
	}
	if strings.Contains(rec.Body.String(), hash) {
		t.Error("the list gives away a hash")
	}
}

// ---- changing a password

func TestUpdateKeepsThePasswordWhenNoneIsSent(t *testing.T) {
	f, hash := protectedRows(t)
	rec := put(t, authHandler(t, f, nil), "/api/managed-sites/"+idA,
		`{"hostname":"a.example.com","kind":"proxy","upstreams":["10.0.0.5:8080"],"enabled":false,"basicAuth":{"user":"bob","password":""}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	got := lastApplied(t, f, "a.example.com")
	if got.BasicAuthUser != "bob" || got.BasicAuthHash != hash {
		t.Errorf("the site has %q and %q, want bob and the hash it had", got.BasicAuthUser, got.BasicAuthHash)
	}
	if got.Enabled {
		t.Error("the site is still enabled")
	}
}

func TestUpdateWithANewPasswordMakesANewHash(t *testing.T) {
	f, hash := protectedRows(t)
	rec := put(t, authHandler(t, f, nil), "/api/managed-sites/"+idA,
		`{"hostname":"a.example.com","kind":"proxy","upstreams":["10.0.0.5:8080"],"basicAuth":{"user":"bob","password":"`+password+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	got := lastApplied(t, f, "a.example.com")
	if got.BasicAuthHash == hash || bcrypt.CompareHashAndPassword([]byte(got.BasicAuthHash), []byte(password)) != nil {
		t.Error("the hash is not of the new password")
	}
}

func TestUpdateWithoutBasicAuthTakesThePasswordAway(t *testing.T) {
	for name, body := range map[string]string{
		"left out": `{"hostname":"a.example.com","kind":"proxy","upstreams":["10.0.0.5:8080"]}`,
		"null":     `{"hostname":"a.example.com","kind":"proxy","upstreams":["10.0.0.5:8080"],"basicAuth":null}`,
		"no user":  `{"hostname":"a.example.com","kind":"proxy","upstreams":["10.0.0.5:8080"],"basicAuth":{"user":"","password":""}}`,
	} {
		f, _ := protectedRows(t)
		if rec := put(t, authHandler(t, f, nil), "/api/managed-sites/"+idA, body); rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d: %s", name, rec.Code, rec.Body)
		}
		if got := lastApplied(t, f, "a.example.com"); got.BasicAuthUser != "" || got.BasicAuthHash != "" {
			t.Errorf("%s: the site still has %q and %q", name, got.BasicAuthUser, got.BasicAuthHash)
		}
	}
}

func TestAPasswordIsKeptOnlyForTheSiteThatHasOne(t *testing.T) {
	f, _ := protectedRows(t)
	rec := put(t, authHandler(t, f, nil), "/api/managed-sites/"+idB,
		`{"hostname":"b.example.com","kind":"proxy","upstreams":["10.0.0.5:8080"],"basicAuth":{"user":"bob","password":""}}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
	if _, has := errorFields(t, fields(t, rec))["basicAuthPassword"]; !has {
		t.Errorf("errors = %v, want one for basicAuthPassword", fields(t, rec)["errors"])
	}
	if len(f.applied) != 0 {
		t.Error("something was applied")
	}
}

func TestCreateWithoutAPasswordForAUserIsRefused(t *testing.T) {
	f := newFakeSites()
	rec := postJSON(t, authHandler(t, f, nil), "/api/managed-sites", withAuth("bob", ""))
	if rec.Code != http.StatusUnprocessableEntity || fields(t, rec)["code"] != "invalid-site" {
		t.Fatalf("status = %d: %s, want 422 invalid-site", rec.Code, rec.Body)
	}
	if _, has := errorFields(t, fields(t, rec))["basicAuthPassword"]; !has {
		t.Errorf("errors = %v, want one for basicAuthPassword", fields(t, rec)["errors"])
	}
	if len(f.applied) != 0 {
		t.Error("something was applied")
	}
}

func TestAWeakOrBadPasswordIsRefusedWithTheFieldAndTheSiteItBelongsTo(t *testing.T) {
	tests := []struct {
		name, user, password, field string
	}{
		{"a short password", "bob", "short", "basicAuthPassword"},
		{"a password bcrypt would cut", "bob", strings.Repeat("a", 80), "basicAuthPassword"},
		{"a username with a space", "bob smith", password, "basicAuthUser"},
		{"a password and no user", "", password, "basicAuthUser"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeSites()
			rec := postJSON(t, authHandler(t, f, nil), "/api/managed-sites", withAuth(tt.user, tt.password))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
			}
			body := fields(t, rec)
			if body["code"] != "invalid-site" {
				t.Errorf("code = %v, want invalid-site", body["code"])
			}
			if msg := errorFields(t, body)[tt.field]; msg == "" {
				t.Errorf("errors = %v, want one for %s with a message", body["errors"], tt.field)
			}
			if list, _ := body["errors"].([]any); len(list) > 0 {
				if site, _ := list[0].(map[string]any)["site"].(string); site == "" {
					t.Error("the error names no site")
				}
			}
			if len(f.applied) != 0 {
				t.Error("something was applied")
			}
			if strings.Contains(rec.Body.String(), tt.password) && tt.password != "" {
				t.Errorf("the refusal repeats the password: %s", rec.Body)
			}
		})
	}
}

func TestTheHashIsNotSomethingARequestMaySet(t *testing.T) {
	hash := "$2a$04$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234"
	for name, body := range map[string]string{
		"at the top":      `{"hostname":"c.example.com","kind":"proxy","upstreams":["10.0.0.7"],"basicAuthHash":"` + hash + `","basicAuthUser":"bob"}`,
		"inside the auth": `{"hostname":"c.example.com","kind":"proxy","upstreams":["10.0.0.7"],"basicAuth":{"user":"bob","password":"` + password + `","hash":"` + hash + `"}}`,
	} {
		f := newFakeSites()
		if rec := postJSON(t, authHandler(t, f, nil), "/api/managed-sites", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
		if len(f.applied) != 0 {
			t.Errorf("%s: something was applied", name)
		}
	}
}

// ---- the cost of the hash

func TestTheHashCostsWhatTheOptionSays(t *testing.T) {
	f := newFakeSites()
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint, Sites: f, Now: func() time.Time { return clock }, BasicAuthCost: 5})
	postJSON(t, h, "/api/managed-sites", withAuth("bob", password))
	cost, err := bcrypt.Cost([]byte(lastApplied(t, f, "c.example.com").BasicAuthHash))
	if err != nil || cost != 5 {
		t.Errorf("cost = %d, %v, want 5", cost, err)
	}
}

func TestTheHashCostsTheDefaultWithoutAnOption(t *testing.T) {
	f := newFakeSites()
	h := New(&fakeCaddy{cfg: parse(t, `{}`)}, Options{Endpoint: testEndpoint, Sites: f, Now: func() time.Time { return clock }})
	postJSON(t, h, "/api/managed-sites", withAuth("bob", password))
	cost, err := bcrypt.Cost([]byte(lastApplied(t, f, "c.example.com").BasicAuthHash))
	if err != nil || cost != bcrypt.DefaultCost {
		t.Errorf("cost = %d, %v, want %d", cost, err, bcrypt.DefaultCost)
	}
}

// ---- the preview

func TestThePreviewUsesAPlaceholderInPlaceOfTheHash(t *testing.T) {
	f := newFakeSites()
	rec := postJSON(t, authHandler(t, f, nil), "/api/managed-sites/preview", withAuth("bob", password))
	if rec.Code != http.StatusOK || fields(t, rec)["valid"] != true {
		t.Fatalf("status = %d: %s, want a valid preview", rec.Code, rec.Body)
	}
	previewed := f.previewed[len(f.previewed)-1]
	var site store.Site
	for _, s := range previewed {
		if s.Hostname == "c.example.com" {
			site = s
		}
	}
	if site.BasicAuthUser != "bob" || site.BasicAuthHash != sites.PlaceholderHash {
		t.Errorf("the preview has %q and %q, want bob and the placeholder", site.BasicAuthUser, site.BasicAuthHash)
	}
}

func TestThePreviewOfASiteThatKeepsItsPasswordNeverShowsTheStoredHash(t *testing.T) {
	f, hash := protectedRows(t)
	rec := postJSON(t, authHandler(t, f, nil), "/api/managed-sites/preview",
		`{"id":"`+idA+`","hostname":"a.example.com","kind":"proxy","upstreams":["10.0.0.5:8080"],"basicAuth":{"user":"bob","password":""}}`)
	if rec.Code != http.StatusOK || fields(t, rec)["valid"] != true {
		t.Fatalf("status = %d: %s, want a valid preview", rec.Code, rec.Body)
	}
	for _, s := range f.previewed[len(f.previewed)-1] {
		if s.Hostname == "a.example.com" && s.BasicAuthHash != sites.PlaceholderHash {
			t.Errorf("the preview of the site has the hash %q, want the placeholder", s.BasicAuthHash)
		}
		if s.BasicAuthHash == hash {
			t.Error("the stored hash went into a preview")
		}
	}
	if strings.Contains(rec.Body.String(), hash) {
		t.Error("the preview answer has the stored hash")
	}
}

func TestThePreviewSaysWhatIsWrongWithAPassword(t *testing.T) {
	f := newFakeSites()
	rec := postJSON(t, authHandler(t, f, nil), "/api/managed-sites/preview", withAuth("bob", "short"))
	if rec.Code != http.StatusOK || fields(t, rec)["valid"] != false {
		t.Fatalf("status = %d: %s, want 200 and not valid", rec.Code, rec.Body)
	}
	if msg := errorFields(t, fields(t, rec))["basicAuthPassword"]; msg == "" {
		t.Errorf("errors = %v, want one for basicAuthPassword", fields(t, rec)["errors"])
	}
	if len(f.previewed) != 0 {
		t.Error("a site that is not valid was previewed")
	}
}

func TestThePreviewOfAPasswordWithNoneToKeepIsNotValid(t *testing.T) {
	f := newFakeSites()
	rec := postJSON(t, authHandler(t, f, nil), "/api/managed-sites/preview", withAuth("bob", ""))
	if fields(t, rec)["valid"] != false {
		t.Errorf("answer = %s, want not valid", rec.Body)
	}
	if _, has := errorFields(t, fields(t, rec))["basicAuthPassword"]; !has {
		t.Errorf("errors = %v, want one for basicAuthPassword", fields(t, rec)["errors"])
	}
}

// ---- nothing about a password is logged

func TestNeitherThePasswordNorItsHashIsLogged(t *testing.T) {
	cases := map[string]func(t *testing.T, h http.Handler, f *fakeSites){
		"a create": func(t *testing.T, h http.Handler, _ *fakeSites) {
			postJSON(t, h, "/api/managed-sites", withAuth("bob", password))
		},
		"a create that is refused": func(t *testing.T, h http.Handler, _ *fakeSites) {
			postJSON(t, h, "/api/managed-sites", withAuth("bob smith", password))
		},
		"a create the manager fails": func(t *testing.T, h http.Handler, f *fakeSites) {
			f.applyErr = errors.New("caddy is on fire")
			postJSON(t, h, "/api/managed-sites", withAuth("bob", password))
		},
		"an update": func(t *testing.T, h http.Handler, _ *fakeSites) {
			put(t, h, "/api/managed-sites/"+idA, `{"hostname":"a.example.com","kind":"proxy","upstreams":["10.0.0.5"],"basicAuth":{"user":"bob","password":"`+password+`"}}`)
		},
		"a preview": func(t *testing.T, h http.Handler, _ *fakeSites) {
			postJSON(t, h, "/api/managed-sites/preview", withAuth("bob", password))
		},
		"a body that is not valid JSON": func(t *testing.T, h http.Handler, _ *fakeSites) {
			postJSON(t, h, "/api/managed-sites", `{"basicAuth":{"password":"`+password+`"`)
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			f, stored := protectedRows(t)
			var log bytes.Buffer
			run(t, authHandler(t, f, &log), f)
			for _, secret := range []string{password, stored} {
				if strings.Contains(log.String(), secret) {
					t.Errorf("the log has a secret (%q...):\n%s", secret[:6], log.String())
				}
			}
			for _, applied := range f.applied {
				for _, s := range applied {
					if strings.Contains(log.String(), s.BasicAuthHash) && s.BasicAuthHash != "" {
						t.Errorf("the log has the hash of %s", s.Hostname)
					}
				}
			}
		})
	}
}

func TestAFailureDoesNotRepeatThePasswordInTheAnswer(t *testing.T) {
	f := newFakeSites()
	f.applyErr = errors.New("the manager failed")
	rec := postJSON(t, authHandler(t, f, nil), "/api/managed-sites", withAuth("bob", password))
	if strings.Contains(rec.Body.String(), password) {
		t.Errorf("the answer has the password: %s", rec.Body)
	}
}
