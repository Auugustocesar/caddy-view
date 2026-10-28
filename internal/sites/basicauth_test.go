package sites

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// hashOf makes a real bcrypt hash at the lowest cost, which is all a test needs.
func hashOf(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

func protected(t *testing.T, id, host, user, password string) store.Site {
	t.Helper()
	s := proxy(id, host, "10.0.0.5")
	s.BasicAuthUser, s.BasicAuthHash = user, hashOf(t, password)
	return s
}

// ---- SetBasicAuth

func TestSetBasicAuthMakesAHashOfThePassword(t *testing.T) {
	s := proxy("a", "app.example.com", "10.0.0.5")
	if errs := SetBasicAuth(&s, "bob", "correct horse", nil, bcrypt.MinCost); errs != nil {
		t.Fatalf("SetBasicAuth() = %v, want no problem", errs)
	}
	if s.BasicAuthUser != "bob" {
		t.Errorf("BasicAuthUser = %q, want bob", s.BasicAuthUser)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(s.BasicAuthHash), []byte("correct horse")); err != nil {
		t.Errorf("the hash %q is not a hash of the password: %v", s.BasicAuthHash, err)
	}
	if got := Validate(s, nil, Options{}); got != nil {
		t.Errorf("a site with the hash SetBasicAuth made is not valid: %v", got)
	}
}

func TestSetBasicAuthDoesNotKeepThePassword(t *testing.T) {
	s := proxy("a", "app.example.com", "10.0.0.5")
	SetBasicAuth(&s, "bob", "correct horse", nil, bcrypt.MinCost)
	if dump := fmt.Sprintf("%+v", s); strings.Contains(dump, "correct horse") {
		t.Errorf("the site holds the password: %s", dump)
	}
}

func TestSetBasicAuthSaltsEveryHash(t *testing.T) {
	a, b := proxy("a", "a.example.com", "10.0.0.5"), proxy("b", "b.example.com", "10.0.0.5")
	SetBasicAuth(&a, "bob", "correct horse", nil, bcrypt.MinCost)
	SetBasicAuth(&b, "bob", "correct horse", nil, bcrypt.MinCost)
	if a.BasicAuthHash == b.BasicAuthHash {
		t.Error("the same password made the same hash twice: it is not salted")
	}
}

func TestSetBasicAuthUsesTheCostItIsGiven(t *testing.T) {
	s := proxy("a", "app.example.com", "10.0.0.5")
	SetBasicAuth(&s, "bob", "correct horse", nil, 5)
	if cost, err := bcrypt.Cost([]byte(s.BasicAuthHash)); err != nil || cost != 5 {
		t.Errorf("cost = %d, %v, want 5", cost, err)
	}
}

func TestSetBasicAuthTrimsTheUsername(t *testing.T) {
	s := proxy("a", "app.example.com", "10.0.0.5")
	if errs := SetBasicAuth(&s, "  bob \t", "correct horse", nil, bcrypt.MinCost); errs != nil || s.BasicAuthUser != "bob" {
		t.Errorf("SetBasicAuth() = %v with the user %q, want no problem and bob", errs, s.BasicAuthUser)
	}
}

func TestSetBasicAuthWithNoUserTakesItAway(t *testing.T) {
	for _, user := range []string{"", "   "} {
		s := protected(t, "a", "app.example.com", "bob", "correct horse")
		if errs := SetBasicAuth(&s, user, "", &s, bcrypt.MinCost); errs != nil {
			t.Errorf("SetBasicAuth(%q) = %v, want no problem", user, errs)
		}
		if s.BasicAuthUser != "" || s.BasicAuthHash != "" {
			t.Errorf("after SetBasicAuth(%q) the site has %q and %q, want neither", user, s.BasicAuthUser, s.BasicAuthHash)
		}
	}
}

func TestSetBasicAuthRefusesAPasswordWithNoUser(t *testing.T) {
	s := proxy("a", "app.example.com", "10.0.0.5")
	errs := SetBasicAuth(&s, "", "correct horse", nil, bcrypt.MinCost)
	if !reflect.DeepEqual(fieldsOf(errs), []string{"basicAuthUser"}) {
		t.Errorf("fields = %v, want basicAuthUser", fieldsOf(errs))
	}
	if s.BasicAuthUser != "" || s.BasicAuthHash != "" {
		t.Errorf("the site was changed: %+v", s)
	}
}

func TestSetBasicAuthKeepsThePreviousHashWithoutAPassword(t *testing.T) {
	previous := protected(t, "a", "app.example.com", "bob", "correct horse")

	same := proxy("a", "app.example.com", "10.0.0.5")
	if errs := SetBasicAuth(&same, "bob", "", &previous, bcrypt.MinCost); errs != nil {
		t.Fatalf("SetBasicAuth() = %v, want no problem", errs)
	}
	if same.BasicAuthUser != "bob" || same.BasicAuthHash != previous.BasicAuthHash {
		t.Errorf("the site has %q and %q, want bob and the hash it had", same.BasicAuthUser, same.BasicAuthHash)
	}

	renamed := proxy("a", "app.example.com", "10.0.0.5")
	if errs := SetBasicAuth(&renamed, "alice", "", &previous, bcrypt.MinCost); errs != nil {
		t.Fatalf("SetBasicAuth() = %v, want no problem", errs)
	}
	if renamed.BasicAuthUser != "alice" || renamed.BasicAuthHash != previous.BasicAuthHash {
		t.Errorf("the site has %q and %q, want alice with the hash it had", renamed.BasicAuthUser, renamed.BasicAuthHash)
	}
}

func TestSetBasicAuthMakesANewHashWhenThereIsANewPassword(t *testing.T) {
	previous := protected(t, "a", "app.example.com", "bob", "correct horse")
	s := proxy("a", "app.example.com", "10.0.0.5")
	if errs := SetBasicAuth(&s, "bob", "battery staple", &previous, bcrypt.MinCost); errs != nil {
		t.Fatal(errs)
	}
	if bcrypt.CompareHashAndPassword([]byte(s.BasicAuthHash), []byte("battery staple")) != nil {
		t.Error("the hash is not of the new password")
	}
	if bcrypt.CompareHashAndPassword([]byte(s.BasicAuthHash), []byte("correct horse")) == nil {
		t.Error("the hash is still of the old password")
	}
}

func TestSetBasicAuthNeedsAPasswordWhenThereIsNoneToKeep(t *testing.T) {
	unprotected := proxy("a", "app.example.com", "10.0.0.5")
	tests := map[string]*store.Site{"no previous site": nil, "a previous site without a password": &unprotected}
	for name, previous := range tests {
		s := proxy("a", "app.example.com", "10.0.0.5")
		errs := SetBasicAuth(&s, "bob", "", previous, bcrypt.MinCost)
		if !reflect.DeepEqual(fieldsOf(errs), []string{"basicAuthPassword"}) {
			t.Errorf("%s: fields = %v, want basicAuthPassword", name, fieldsOf(errs))
		}
		if s.BasicAuthUser != "" || s.BasicAuthHash != "" {
			t.Errorf("%s: the site was changed: %+v", name, s)
		}
	}
}

func TestSetBasicAuthRefusesBadUsernames(t *testing.T) {
	good := []string{"bob", "Bob", "alice.smith", "a_b-c", "user@example.com", "0", strings.Repeat("a", 64)}
	bad := map[string]string{
		"a space inside":   "bob smith",
		"a colon":          "bob:smith",
		"a quote":          `bo"b`,
		"a backtick":       "bo`b",
		"a brace":          "bo{b}",
		"a hash":           "bob#1",
		"a backslash":      `bo\b`,
		"a line break":     "bob\nevil",
		"a tab inside":     "bo\tb",
		"non-ASCII":        "josé",
		"too long":         strings.Repeat("a", 65),
		"a dollar sign":    "bo$b",
		"a semicolon":      "bob;",
		"a slash":          "bob/smith",
		"a null":           "bob\x00",
		"an angle bracket": "bob<",
	}
	for _, user := range good {
		s := proxy("a", "app.example.com", "10.0.0.5")
		if errs := SetBasicAuth(&s, user, "correct horse", nil, bcrypt.MinCost); errs != nil {
			t.Errorf("SetBasicAuth(%q) = %v, want it accepted", user, errs)
		}
	}
	for name, user := range bad {
		s := proxy("a", "app.example.com", "10.0.0.5")
		errs := SetBasicAuth(&s, user, "correct horse", nil, bcrypt.MinCost)
		if !reflect.DeepEqual(fieldsOf(errs), []string{"basicAuthUser"}) {
			t.Errorf("%s: SetBasicAuth(%q) fields = %v, want basicAuthUser", name, user, fieldsOf(errs))
		}
		if s.BasicAuthHash != "" {
			t.Errorf("%s: the site was changed", name)
		}
	}
}

func TestSetBasicAuthRefusesBadPasswords(t *testing.T) {
	tests := []struct {
		name     string
		password string
		ok       bool
	}{
		{"eight bytes", "12345678", true},
		{"with spaces", "correct horse battery", true},
		{"the longest bcrypt takes, 72 bytes", strings.Repeat("a", 72), true},
		{"72 bytes of two-byte letters", strings.Repeat("é", 36), true},
		{"seven bytes", "1234567", false},
		{"73 bytes, which bcrypt would silently cut", strings.Repeat("a", 73), false},
		{"74 bytes of two-byte letters", strings.Repeat("é", 37), false},
		{"a hundred bytes", strings.Repeat("a", 100), false},
	}
	for _, tt := range tests {
		s := proxy("a", "app.example.com", "10.0.0.5")
		errs := SetBasicAuth(&s, "bob", tt.password, nil, bcrypt.MinCost)
		if tt.ok && errs != nil {
			t.Errorf("%s: SetBasicAuth() = %v, want it accepted", tt.name, errs)
		}
		if !tt.ok && !reflect.DeepEqual(fieldsOf(errs), []string{"basicAuthPassword"}) {
			t.Errorf("%s: fields = %v, want basicAuthPassword", tt.name, fieldsOf(errs))
		}
		if !tt.ok && s.BasicAuthHash != "" {
			t.Errorf("%s: the site was changed", tt.name)
		}
	}
}

func TestSetBasicAuthReportsBothFieldsWhenBothAreWrong(t *testing.T) {
	s := proxy("a", "app.example.com", "10.0.0.5")
	errs := SetBasicAuth(&s, "bob smith", "short", nil, bcrypt.MinCost)
	if !reflect.DeepEqual(fieldsOf(errs), []string{"basicAuthPassword", "basicAuthUser"}) {
		t.Errorf("fields = %v, want both", fieldsOf(errs))
	}
}

func TestTheMessagesNeverContainThePassword(t *testing.T) {
	s := proxy("a", "app.example.com", "10.0.0.5")
	for _, e := range SetBasicAuth(&s, "bob smith", "pw", nil, bcrypt.MinCost) {
		if strings.Contains(e.Message, "pw") && !strings.Contains(strings.ToLower(e.Message), "password") {
			t.Errorf("the message %q repeats what was typed", e.Message)
		}
	}
	long := strings.Repeat("secret!!", 20)
	for _, e := range SetBasicAuth(&s, "bob", long, nil, bcrypt.MinCost) {
		if strings.Contains(e.Message, long) {
			t.Errorf("the message %q repeats the password", e.Message)
		}
	}
}

func TestThePlaceholderHashIsValidAndIsNotAnyonesHash(t *testing.T) {
	s := proxy("a", "app.example.com", "10.0.0.5")
	s.BasicAuthUser, s.BasicAuthHash = "bob", PlaceholderHash
	if errs := Validate(s, nil, Options{}); errs != nil {
		t.Errorf("a site with the placeholder is not valid: %v", errs)
	}
	if bcrypt.CompareHashAndPassword([]byte(PlaceholderHash), []byte("")) == nil || bcrypt.CompareHashAndPassword([]byte(PlaceholderHash), []byte("password")) == nil {
		t.Error("the placeholder matches a password")
	}
}

// ---- Validate

func TestValidateAcceptsAProxyWithAPassword(t *testing.T) {
	good := []store.Site{
		protected(t, "a", "app.example.com", "bob", "correct horse"),
		protected(t, "a", "app.example.com", "user@example.com", "correct horse"),
	}
	for _, s := range good {
		if errs := Validate(s, nil, Options{}); errs != nil {
			t.Errorf("Validate(%q) = %v, want it valid", s.BasicAuthUser, errs)
		}
	}
}

func TestValidateRefusesHalfAPassword(t *testing.T) {
	onlyUser := proxy("a", "app.example.com", "10.0.0.5")
	onlyUser.BasicAuthUser = "bob"
	onlyHash := proxy("a", "app.example.com", "10.0.0.5")
	onlyHash.BasicAuthHash = hashOf(t, "correct horse")
	if got := fieldsOf(Validate(onlyUser, nil, Options{})); !reflect.DeepEqual(got, []string{"basicAuthPassword"}) {
		t.Errorf("a user without a hash: fields = %v, want basicAuthPassword", got)
	}
	if got := fieldsOf(Validate(onlyHash, nil, Options{})); !reflect.DeepEqual(got, []string{"basicAuthUser"}) {
		t.Errorf("a hash without a user: fields = %v, want basicAuthUser", got)
	}
}

func TestValidateRefusesAUsernameThatCannotBeWritten(t *testing.T) {
	for _, user := range []string{"bob smith", "bo\"b", "bob\n}\nevil", "bob:x", "bo{b}", strings.Repeat("a", 65)} {
		s := protected(t, "a", "app.example.com", "bob", "correct horse")
		s.BasicAuthUser = user
		if got := fieldsOf(Validate(s, nil, Options{})); !reflect.DeepEqual(got, []string{"basicAuthUser"}) {
			t.Errorf("Validate(%q) fields = %v, want basicAuthUser", user, got)
		}
	}
}

func TestValidateRefusesAHashThatIsNotBcryptOrThatCouldBreakTheFile(t *testing.T) {
	real := hashOf(t, "correct horse")
	tests := map[string]string{
		"plain text":                    "correct horse",
		"a hash of another kind":        "$1$salt$abcdefghijklmnopqrstuv",
		"a bcrypt hash cut short":       real[:40],
		"a bcrypt hash with more":       real + "x",
		"a line break after the hash":   real + "\n}\nevil.example.com {",
		"a space and a directive":       real + " respond pwned",
		"a brace":                       real[:30] + "{" + real[31:],
		"an unknown version":            "$2z" + real[3:],
		"a cost of 99":                  real[:4] + "99" + real[6:],
		"a quote":                       real[:30] + `"` + real[31:],
		"the empty hash with a user":    "",
		"white space around a good one": " " + real,
	}
	for name, hash := range tests {
		s := protected(t, "a", "app.example.com", "bob", "correct horse")
		s.BasicAuthHash = hash
		if got := fieldsOf(Validate(s, nil, Options{})); !reflect.DeepEqual(got, []string{"basicAuthPassword"}) {
			t.Errorf("%s: fields = %v, want basicAuthPassword", name, got)
		}
	}
}

func TestValidateRefusesAPasswordOnARedirect(t *testing.T) {
	s := redirect("a", "old.example.com", "https://new.example.com", 301)
	s.BasicAuthUser, s.BasicAuthHash = "bob", hashOf(t, "correct horse")
	if got := fieldsOf(Validate(s, nil, Options{})); !reflect.DeepEqual(got, []string{"basicAuthUser"}) {
		t.Errorf("fields = %v, want basicAuthUser: only a proxy has a password", got)
	}
}

func TestNormalizeKeepsTheBasicAuth(t *testing.T) {
	s := protected(t, "a", "App.Example.COM", "Bob", "correct horse")
	n := Normalize(s)
	if n.BasicAuthUser != "Bob" || n.BasicAuthHash != s.BasicAuthHash {
		t.Errorf("Normalize changed the basic auth to %q and %q", n.BasicAuthUser, n.BasicAuthHash)
	}
}

// ---- Generate

func TestGenerateAProxyWithAPassword(t *testing.T) {
	s := protected(t, "a", "app.example.com", "bob", "correct horse")
	got := Generate([]store.Site{s}, Options{})
	want := text(
		header,
		"",
		"app.example.com {",
		"\tbasic_auth {",
		"\t\tbob "+s.BasicAuthHash,
		"\t}",
		"\treverse_proxy 10.0.0.5",
		"}",
	)
	if got.Text != want {
		t.Errorf("Generate =\n%s\nwant\n%s", got.Text, want)
	}
}

func TestGenerateTheBasicAuthAfterTheAccessLogAndTLSAndBeforeTheProxy(t *testing.T) {
	s := protected(t, "a", "app.example.com", "bob", "correct horse")
	s.AccessLog, s.TLS, s.SkipTLSVerify, s.Upstreams = true, store.TLSInternal, true, []string{"https://10.0.0.5:8443"}
	got := Generate([]store.Site{s}, Options{AccessLog: "/var/log/caddy/access.log"})
	block := got.Text[strings.Index(got.Text, "app.example.com {"):]
	want := strings.Join([]string{
		"app.example.com {",
		"\timport caddy-view-access-log",
		"\ttls internal",
		"\tbasic_auth {",
		"\t\tbob " + s.BasicAuthHash,
		"\t}",
		"\treverse_proxy https://10.0.0.5:8443 {",
		"\t\ttransport http {",
		"\t\t\ttls_insecure_skip_verify",
		"\t\t}",
		"\t}",
		"}",
	}, "\n") + "\n"
	if block != want {
		t.Errorf("the block =\n%s\nwant\n%s", block, want)
	}
}

func TestGenerateLeavesAPasswordedSiteOutWhenItIsDisabled(t *testing.T) {
	s := protected(t, "a", "app.example.com", "bob", "correct horse")
	s.Enabled = false
	got := Generate([]store.Site{s}, Options{})
	if strings.Contains(got.Text, s.BasicAuthHash) || strings.Contains(got.Text, "basic_auth") {
		t.Errorf("a disabled site left its password in the file:\n%s", got.Text)
	}
}

func TestGenerateSaysWhichLinesAPasswordedSiteHas(t *testing.T) {
	a := protected(t, "a", "a.example.com", "bob", "correct horse")
	b := proxy("b", "b.example.com", "10.0.0.6")
	got := Generate([]store.Site{a, b}, Options{})
	if len(got.Blocks) != 2 {
		t.Fatalf("blocks = %+v, want two", got.Blocks)
	}
	// Every line of the first site, the password included, belongs to it.
	lines := strings.Split(got.Text, "\n")
	for n := got.Blocks[0].Start; n <= got.Blocks[0].End; n++ {
		if id, ok := got.SiteAt(n); !ok || id != "a" {
			t.Errorf("line %d (%q) belongs to %q, %v, want a", n, lines[n-1], id, ok)
		}
	}
	if id, _ := got.SiteAt(got.Blocks[1].Start); id != "b" {
		t.Errorf("the second block starts at a line of %q, want b", id)
	}
}

func TestGenerateIsTheSameWithAndWithoutAPasswordForTheOtherSites(t *testing.T) {
	plain := proxy("p", "plain.example.com", "10.0.0.6")
	alone := Generate([]store.Site{plain}, Options{}).Text
	with := Generate([]store.Site{protected(t, "a", "a.example.com", "bob", "correct horse"), plain}, Options{}).Text
	if !strings.Contains(with, strings.TrimPrefix(alone, header+"\n")) {
		t.Errorf("a site without a password was written differently next to one with it:\n%s", with)
	}
}
