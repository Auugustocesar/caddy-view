package sites

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

const header = "# Managed by caddy-view. Changes made here are overwritten: edit the sites in caddy-view."

// text joins lines into a file that ends with a newline.
func text(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func TestGenerateWithNoSitesIsTheHeader(t *testing.T) {
	for _, in := range [][]store.Site{nil, {}} {
		if got := Generate(in, Options{}); got.Text != text(header) || len(got.Blocks) != 0 {
			t.Errorf("Generate(%v) = %+v, want only the header", in, got)
		}
	}
}

func TestGenerateAProxy(t *testing.T) {
	got := Generate([]store.Site{proxy("a", "app.example.com", "10.0.0.5:8080")}, Options{})
	want := text(
		header,
		"",
		"app.example.com {",
		"\treverse_proxy 10.0.0.5:8080",
		"}",
	)
	if got.Text != want {
		t.Errorf("Generate =\n%s\nwant\n%s", got.Text, want)
	}
}

func TestGenerateTheOptionsOfASite(t *testing.T) {
	tests := []struct {
		name string
		site store.Site
		opts Options
		want []string // the lines of the block, with the header and the blank line before it
	}{
		{
			"several upstreams",
			proxy("a", "app.example.com", "10.0.0.5:8080", "https://10.0.0.6:8443"),
			Options{},
			[]string{"app.example.com {", "\treverse_proxy 10.0.0.5:8080 https://10.0.0.6:8443", "}"},
		},
		{
			"skipping the verification of an https upstream",
			func() store.Site {
				s := proxy("a", "app.example.com", "https://10.0.0.5:8443")
				s.SkipTLSVerify = true
				return s
			}(),
			Options{},
			[]string{
				"app.example.com {",
				"\treverse_proxy https://10.0.0.5:8443 {",
				"\t\ttransport http {",
				"\t\t\ttls_insecure_skip_verify",
				"\t\t}",
				"\t}",
				"}",
			},
		},
		{
			"the internal certificate authority",
			func() store.Site {
				s := proxy("a", "app.example.com", "10.0.0.5")
				s.TLS = store.TLSInternal
				return s
			}(),
			Options{},
			[]string{"app.example.com {", "\ttls internal", "\treverse_proxy 10.0.0.5", "}"},
		},
		{
			"plain http",
			func() store.Site {
				s := proxy("a", "app.example.com", "10.0.0.5")
				s.TLS = store.TLSOff
				return s
			}(),
			Options{},
			[]string{"http://app.example.com {", "\treverse_proxy 10.0.0.5", "}"},
		},
		{
			"a redirect",
			redirect("a", "old.example.com", "https://new.example.com{uri}", 308),
			Options{},
			[]string{"old.example.com {", "\tredir https://new.example.com{uri} 308", "}"},
		},
		{
			"an ip address in brackets",
			proxy("a", "::1", "10.0.0.5"),
			Options{},
			[]string{"[::1] {", "\treverse_proxy 10.0.0.5", "}"},
		},
		{
			"the access log, then tls, then the handler",
			func() store.Site {
				s := proxy("a", "app.example.com", "10.0.0.5")
				s.AccessLog, s.TLS = true, store.TLSInternal
				return s
			}(),
			Options{AccessLog: "/var/log/caddy/access.log"},
			[]string{
				"(caddy-view-access-log) {",
				"\tlog {",
				"\t\toutput file /var/log/caddy/access.log",
				"\t\tformat json",
				"\t}",
				"}",
				"",
				"app.example.com {",
				"\timport caddy-view-access-log",
				"\ttls internal",
				"\treverse_proxy 10.0.0.5",
				"}",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := text(append([]string{header, ""}, tt.want...)...)
			if got := Generate([]store.Site{tt.site}, tt.opts); got.Text != want {
				t.Errorf("Generate =\n%s\nwant\n%s", got.Text, want)
			}
		})
	}
}

func TestGenerateSortsByHostnameAndLeavesOutDisabledSites(t *testing.T) {
	disabled := proxy("c", "c.example.com", "10.0.0.7")
	disabled.Enabled = false
	disabled.AccessLog = true // a disabled site must not bring the snippet in
	all := []store.Site{
		proxy("b", "b.example.com", "10.0.0.6"),
		disabled,
		redirect("a", "a.example.com", "https://x.example.com", 301),
	}
	got := Generate(all, Options{AccessLog: "/var/log/caddy/access.log"})
	want := text(
		header,
		"",
		"a.example.com {",
		"\tredir https://x.example.com 301",
		"}",
		"",
		"b.example.com {",
		"\treverse_proxy 10.0.0.6",
		"}",
	)
	if got.Text != want {
		t.Errorf("Generate =\n%s\nwant\n%s", got.Text, want)
	}
}

func TestGenerateIsTheSameWhateverTheOrderOfTheInput(t *testing.T) {
	a, b, c := proxy("a", "a.example.com", "10.0.0.1"), proxy("b", "b.example.com", "10.0.0.2"), proxy("c", "c.example.com", "10.0.0.3")
	first := Generate([]store.Site{a, b, c}, Options{})
	second := Generate([]store.Site{c, a, b}, Options{})
	if first.Text != second.Text || !reflect.DeepEqual(first.Blocks, second.Blocks) {
		t.Errorf("the output depends on the order of the input:\n%s\nvs\n%s", first.Text, second.Text)
	}
}

func TestGenerateNormalizesTheSitesFirst(t *testing.T) {
	s := proxy("a", "App.Example.COM", "http://10.0.0.5:80")
	s.TLS = ""
	got := Generate([]store.Site{s}, Options{})
	want := text(header, "", "app.example.com {", "\treverse_proxy 10.0.0.5:80", "}")
	if got.Text != want {
		t.Errorf("Generate =\n%s\nwant\n%s", got.Text, want)
	}
}

func TestGenerateSaysWhichLinesBelongToWhichSite(t *testing.T) {
	skip := proxy("b", "b.example.com", "https://10.0.0.6:8443")
	skip.SkipTLSVerify = true
	all := []store.Site{skip, proxy("a", "a.example.com", "10.0.0.5")}
	got := Generate(all, Options{})

	// Lines: 1 header, 2 blank, 3-5 a.example.com, 6 blank, 7-13 b.example.com.
	wantBlocks := []Block{{SiteID: "a", Start: 3, End: 5}, {SiteID: "b", Start: 7, End: 13}}
	if !reflect.DeepEqual(got.Blocks, wantBlocks) {
		t.Fatalf("Blocks = %+v, want %+v\n%s", got.Blocks, wantBlocks, got.Text)
	}
	for line, want := range map[int]string{3: "a", 4: "a", 5: "a", 7: "b", 10: "b", 13: "b"} {
		if id, ok := got.SiteAt(line); !ok || id != want {
			t.Errorf("SiteAt(%d) = %q, %v, want %q", line, id, ok, want)
		}
	}
	for _, line := range []int{-1, 0, 1, 2, 6, 14, 99} {
		if id, ok := got.SiteAt(line); ok {
			t.Errorf("SiteAt(%d) = %q, true, want a line that belongs to no site", line, id)
		}
	}
}

func TestGenerateBlocksFollowTheAccessLogSnippet(t *testing.T) {
	s := proxy("a", "a.example.com", "10.0.0.5")
	s.AccessLog = true
	got := Generate([]store.Site{s}, Options{AccessLog: "/var/log/caddy/access.log"})
	lines := strings.Split(strings.TrimSuffix(got.Text, "\n"), "\n")
	if len(got.Blocks) != 1 {
		t.Fatalf("Blocks = %+v, want one", got.Blocks)
	}
	b := got.Blocks[0]
	if lines[b.Start-1] != "a.example.com {" || lines[b.End-1] != "}" {
		t.Errorf("the block of a spans %q to %q, want it to start at its address and end at its brace", lines[b.Start-1], lines[b.End-1])
	}
	if id, ok := got.SiteAt(3); ok {
		t.Errorf("SiteAt(3) = %q, true, want the snippet to belong to no site", id)
	}
}
