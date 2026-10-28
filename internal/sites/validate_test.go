package sites

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

func proxy(id, host string, upstreams ...string) store.Site {
	return store.Site{ID: id, Hostname: host, Kind: store.SiteProxy, Upstreams: upstreams, TLS: store.TLSAutomatic, Enabled: true}
}

func redirect(id, host, target string, code int) store.Site {
	return store.Site{ID: id, Hostname: host, Kind: store.SiteRedirect, RedirectTarget: target, RedirectCode: code, TLS: store.TLSAutomatic, Enabled: true}
}

func fieldsOf(errs []FieldError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Field)
		if e.Message == "" {
			out = append(out, "(no message for "+e.Field+")")
		}
	}
	sort.Strings(out)
	return out
}

func TestValidAcceptsAGoodSite(t *testing.T) {
	good := []store.Site{
		proxy("a", "app.example.com", "10.0.0.5:8080"),
		proxy("a", "app.example.com", "http://10.0.0.5:8080", "https://backend.internal:8443"),
		proxy("a", "localhost", "127.0.0.1:3000"),
		proxy("a", "10.0.0.1", "backend"),
		proxy("a", "::1", "[::1]:8080"),
		proxy("a", "[::1]", "backend:80"),
		proxy("a", "App.Example.COM", "10.0.0.5"),
		redirect("a", "old.example.com", "https://new.example.com{uri}", 301),
		redirect("a", "old.example.com", "http://new.example.com/path?q=1", 0),
		redirect("a", "old.example.com", "https://new.example.com", 308),
	}
	for _, s := range good {
		if errs := Validate(s, nil, Options{}); len(errs) != 0 {
			t.Errorf("Validate(%+v) = %v, want no problem", s, errs)
		}
	}
}

func TestValidateFindsTheProblemsOfASite(t *testing.T) {
	long := strings.Repeat("a", 64)
	manyLabels := strings.Repeat("abcdefghi.", 26) + "com" // longer than 253
	tests := []struct {
		name string
		site store.Site
		want []string // fields, sorted
	}{
		{"no hostname", proxy("a", "", "10.0.0.5"), []string{"hostname"}},
		{"a space in the hostname", proxy("a", "app example.com", "10.0.0.5"), []string{"hostname"}},
		{"a port in the hostname", proxy("a", "app.example.com:8080", "10.0.0.5"), []string{"hostname"}},
		{"a scheme in the hostname", proxy("a", "https://app.example.com", "10.0.0.5"), []string{"hostname"}},
		{"a wildcard hostname", proxy("a", "*.example.com", "10.0.0.5"), []string{"hostname"}},
		{"a path in the hostname", proxy("a", "app.example.com/x", "10.0.0.5"), []string{"hostname"}},
		{"a label that starts with a hyphen", proxy("a", "-app.example.com", "10.0.0.5"), []string{"hostname"}},
		{"a label that ends with a hyphen", proxy("a", "app-.example.com", "10.0.0.5"), []string{"hostname"}},
		{"an underscore", proxy("a", "my_app.example.com", "10.0.0.5"), []string{"hostname"}},
		{"a trailing dot", proxy("a", "app.example.com.", "10.0.0.5"), []string{"hostname"}},
		{"an empty label", proxy("a", "app..example.com", "10.0.0.5"), []string{"hostname"}},
		{"a label of 64 characters", proxy("a", long+".example.com", "10.0.0.5"), []string{"hostname"}},
		{"a hostname longer than 253", proxy("a", manyLabels, "10.0.0.5"), []string{"hostname"}},
		{"braces in the hostname", proxy("a", "{env.HOST}", "10.0.0.5"), []string{"hostname"}},
		{"a newline in the hostname", proxy("a", "app.example.com\nrespond hacked", "10.0.0.5"), []string{"hostname"}},

		{"no kind", func() store.Site { s := proxy("a", "app.example.com", "10.0.0.5"); s.Kind = ""; return s }(), []string{"kind"}},
		{"an unknown kind", func() store.Site { s := proxy("a", "app.example.com", "10.0.0.5"); s.Kind = "magic"; return s }(), []string{"kind"}},

		{"a proxy without upstreams", proxy("a", "app.example.com"), []string{"upstreams"}},
		{"an empty upstream", proxy("a", "app.example.com", ""), []string{"upstreams"}},
		{"a port of zero", proxy("a", "app.example.com", "10.0.0.5:0"), []string{"upstreams"}},
		{"a port above 65535", proxy("a", "app.example.com", "10.0.0.5:70000"), []string{"upstreams"}},
		{"a port that is not a number", proxy("a", "app.example.com", "10.0.0.5:abc"), []string{"upstreams"}},
		{"a path in an upstream", proxy("a", "app.example.com", "http://10.0.0.5:8080/api"), []string{"upstreams"}},
		{"a query in an upstream", proxy("a", "app.example.com", "10.0.0.5:8080?x=1"), []string{"upstreams"}},
		{"a user in an upstream", proxy("a", "app.example.com", "http://user@10.0.0.5"), []string{"upstreams"}},
		{"an unknown scheme in an upstream", proxy("a", "app.example.com", "ftp://10.0.0.5"), []string{"upstreams"}},
		{"a space in an upstream", proxy("a", "app.example.com", "10.0.0.5 8080"), []string{"upstreams"}},
		{"braces in an upstream", proxy("a", "app.example.com", "{env.UPSTREAM}"), []string{"upstreams"}},
		{"the same upstream twice", proxy("a", "app.example.com", "10.0.0.5:8080", "10.0.0.5:8080"), []string{"upstreams"}},
		{"the same upstream twice, once with the default scheme", proxy("a", "app.example.com", "http://10.0.0.5:8080", "10.0.0.5:8080"), []string{"upstreams"}},
		{"one good upstream and one bad", proxy("a", "app.example.com", "10.0.0.5:8080", "10.0.0.6:0"), []string{"upstreams"}},

		{"skipping verification with http upstreams only", func() store.Site {
			s := proxy("a", "app.example.com", "10.0.0.5:8080")
			s.SkipTLSVerify = true
			return s
		}(), []string{"skipTlsVerify"}},

		{"a redirect target on a proxy", func() store.Site {
			s := proxy("a", "app.example.com", "10.0.0.5")
			s.RedirectTarget = "https://x.example.com"
			return s
		}(), []string{"redirectTarget"}},
		{"a redirect code on a proxy", func() store.Site {
			s := proxy("a", "app.example.com", "10.0.0.5")
			s.RedirectCode = 301
			return s
		}(), []string{"redirectCode"}},

		{"a redirect without a target", redirect("a", "old.example.com", "", 301), []string{"redirectTarget"}},
		{"a redirect to a relative path", redirect("a", "old.example.com", "/new", 301), []string{"redirectTarget"}},
		{"a redirect to another scheme", redirect("a", "old.example.com", "ftp://x.example.com", 301), []string{"redirectTarget"}},
		{"a redirect to a protocol relative url", redirect("a", "old.example.com", "//x.example.com", 301), []string{"redirectTarget"}},
		{"a space in a redirect target", redirect("a", "old.example.com", "https://x.example.com/a b", 301), []string{"redirectTarget"}},
		{"a quote in a redirect target", redirect("a", "old.example.com", `https://x.example.com/"`, 301), []string{"redirectTarget"}},
		{"another placeholder in a redirect target", redirect("a", "old.example.com", "https://x.example.com{path}", 301), []string{"redirectTarget"}},
		{"an environment placeholder in a redirect target", redirect("a", "old.example.com", "https://x.example.com{env.SECRET}", 301), []string{"redirectTarget"}},
		{"a newline in a redirect target", redirect("a", "old.example.com", "https://x.example.com\nrespond hacked", 301), []string{"redirectTarget"}},
		{"a redirect code that is not one", redirect("a", "old.example.com", "https://x.example.com", 200), []string{"redirectCode"}},
		{"a redirect with upstreams", func() store.Site {
			s := redirect("a", "old.example.com", "https://x.example.com", 301)
			s.Upstreams = []string{"10.0.0.5"}
			return s
		}(), []string{"upstreams"}},
		{"a redirect that skips verification", func() store.Site {
			s := redirect("a", "old.example.com", "https://x.example.com", 301)
			s.SkipTLSVerify = true
			return s
		}(), []string{"skipTlsVerify"}},

		{"an unknown tls mode", func() store.Site { s := proxy("a", "app.example.com", "10.0.0.5"); s.TLS = "maybe"; return s }(), []string{"tls"}},

		{"an access log nobody knows the file of", func() store.Site {
			s := proxy("a", "app.example.com", "10.0.0.5")
			s.AccessLog = true
			return s
		}(), []string{"accessLog"}},

		{"several problems at once", proxy("a", "", "10.0.0.5:0"), []string{"hostname", "upstreams"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fieldsOf(Validate(tt.site, nil, Options{}))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Validate(%+v) fields = %v, want %v", tt.site, got, tt.want)
			}
		})
	}
}

func TestValidateAllowsTheAccessLogWhenItsFileIsKnown(t *testing.T) {
	s := proxy("a", "app.example.com", "10.0.0.5")
	s.AccessLog = true
	if errs := Validate(s, nil, Options{AccessLog: "/var/log/caddy/access.log"}); len(errs) != 0 {
		t.Errorf("Validate = %v, want it accepted", errs)
	}
}

func TestValidateAllowsSkippingVerificationWithAnHTTPSUpstream(t *testing.T) {
	s := proxy("a", "app.example.com", "https://10.0.0.5:8443")
	s.SkipTLSVerify = true
	if errs := Validate(s, nil, Options{}); len(errs) != 0 {
		t.Errorf("Validate = %v, want it accepted", errs)
	}
}

func TestValidateRefusesAHostnameAnotherSiteHas(t *testing.T) {
	others := []store.Site{proxy("b", "app.example.com", "10.0.0.6"), proxy("c", "other.example.com", "10.0.0.7")}
	for _, host := range []string{"app.example.com", "APP.Example.com"} {
		got := fieldsOf(Validate(proxy("a", host, "10.0.0.5"), others, Options{}))
		if !reflect.DeepEqual(got, []string{"hostname"}) {
			t.Errorf("Validate with hostname %q = %v, want a hostname problem", host, got)
		}
	}
	// A site is not a duplicate of itself: editing it keeps its hostname.
	if errs := Validate(proxy("b", "app.example.com", "10.0.0.9"), others, Options{}); len(errs) != 0 {
		t.Errorf("Validate of an existing site = %v, want no problem", errs)
	}
	if errs := Validate(proxy("a", "new.example.com", "10.0.0.5"), others, Options{}); len(errs) != 0 {
		t.Errorf("Validate of a new hostname = %v, want no problem", errs)
	}
}

func TestValidateAnswersInWordsForTheUser(t *testing.T) {
	for _, e := range Validate(proxy("a", "", "10.0.0.5:0"), nil, Options{}) {
		if len(e.Message) < 10 || strings.Contains(e.Message, "%") {
			t.Errorf("the message for %s is %q, want a sentence", e.Field, e.Message)
		}
	}
}

func TestNormalize(t *testing.T) {
	t.Run("hostname", func(t *testing.T) {
		for in, want := range map[string]string{
			"App.Example.COM": "app.example.com", "  app.example.com ": "app.example.com", "[::1]": "::1", "::1": "::1", "LOCALHOST": "localhost",
		} {
			if got := Normalize(proxy("a", in, "10.0.0.5")).Hostname; got != want {
				t.Errorf("Normalize(%q).Hostname = %q, want %q", in, got, want)
			}
		}
	})
	t.Run("upstreams", func(t *testing.T) {
		in := proxy("a", "app.example.com", "http://10.0.0.5:8080", "HTTPS://Backend.Internal:8443", " 10.0.0.7 ", "https://other")
		want := []string{"10.0.0.5:8080", "https://backend.internal:8443", "10.0.0.7", "https://other"}
		if got := Normalize(in).Upstreams; !reflect.DeepEqual(got, want) {
			t.Errorf("Normalize upstreams = %v, want %v", got, want)
		}
	})
	t.Run("defaults", func(t *testing.T) {
		p := proxy("a", "app.example.com", "10.0.0.5")
		p.TLS = ""
		if got := Normalize(p).TLS; got != store.TLSAutomatic {
			t.Errorf("an empty TLS mode became %q, want automatic", got)
		}
		if got := Normalize(redirect("a", "old.example.com", "https://x.example.com", 0)).RedirectCode; got != 301 {
			t.Errorf("a redirect code of 0 became %d, want 301", got)
		}
		if got := Normalize(proxy("a", "app.example.com", "10.0.0.5")).RedirectCode; got != 0 {
			t.Errorf("a proxy got the redirect code %d, want it left alone", got)
		}
	})
	t.Run("a normalized site is still the same one", func(t *testing.T) {
		in := proxy("a", "App.example.com", "http://10.0.0.5")
		in.Enabled, in.AccessLog = true, true
		got := Normalize(in)
		if got.ID != "a" || got.Kind != store.SiteProxy || !got.Enabled || !got.AccessLog {
			t.Errorf("Normalize changed more than the form: %+v", got)
		}
	})
	t.Run("it shares nothing with its argument", func(t *testing.T) {
		in := proxy("a", "app.example.com", "http://10.0.0.5")
		got := Normalize(in)
		got.Upstreams[0] = "changed"
		if in.Upstreams[0] != "http://10.0.0.5" {
			t.Errorf("Normalize changed the upstreams of its argument: %v", in.Upstreams)
		}
	})
	t.Run("what it makes validates", func(t *testing.T) {
		if errs := Validate(Normalize(proxy("a", "App.Example.com", "HTTP://10.0.0.5:80")), nil, Options{}); len(errs) != 0 {
			t.Errorf("a normalized valid site has problems: %v", errs)
		}
	})
}

func TestNewID(t *testing.T) {
	a, b := NewID(), NewID()
	if len(a) != 32 || strings.Trim(a, "0123456789abcdef") != "" {
		t.Errorf("NewID() = %q, want 32 lower-case hexadecimal characters", a)
	}
	if a == b {
		t.Errorf("two IDs are the same: %q", a)
	}
}
