package caddy

import (
	"encoding/json"
	"reflect"
	"testing"
)

func parseConfig(t *testing.T, raw string) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("invalid test config: %v", err)
	}
	return cfg
}

// serversConfig wraps a servers object in the apps.http.servers path.
func serversConfig(servers string) string {
	return `{"apps":{"http":{"servers":` + servers + `}}}`
}

func TestExtractSites(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   []Site
	}{
		{
			name: "caddyfile style reverse proxy",
			config: serversConfig(`{"srv0":{"listen":[":443"],"routes":[
				{"match":[{"host":["app.example.com"]}],"handle":[{"handler":"subroute","routes":[
					{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.5:8080"}]}]}
				]}],"terminal":true}
			]}}`),
			want: []Site{{Server: "srv0", Host: "app.example.com", Upstreams: []string{"10.0.0.5:8080"}}},
		},
		{
			name: "several hosts in one matcher give one site each",
			config: serversConfig(`{"srv0":{"routes":[
				{"match":[{"host":["a.example.com","b.example.com"]}],"handle":[
					{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.5:80"}]}
				]}
			]}}`),
			want: []Site{
				{Server: "srv0", Host: "a.example.com", Upstreams: []string{"10.0.0.5:80"}},
				{Server: "srv0", Host: "b.example.com", Upstreams: []string{"10.0.0.5:80"}},
			},
		},
		{
			name: "several match objects are alternatives",
			config: serversConfig(`{"srv0":{"routes":[
				{"match":[{"host":["a.example.com"]},{"host":["b.example.com"]}],"handle":[
					{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.5:80"}]}
				]}
			]}}`),
			want: []Site{
				{Server: "srv0", Host: "a.example.com", Upstreams: []string{"10.0.0.5:80"}},
				{Server: "srv0", Host: "b.example.com", Upstreams: []string{"10.0.0.5:80"}},
			},
		},
		{
			name: "site without a reverse proxy is listed with no upstreams",
			config: serversConfig(`{"srv0":{"routes":[
				{"match":[{"host":["static.example.com"]}],"handle":[{"handler":"static_response","body":"ok"}]}
			]}}`),
			want: []Site{{Server: "srv0", Host: "static.example.com", Upstreams: []string{}}},
		},
		{
			name: "route without a matcher is a catch-all with an empty host",
			config: serversConfig(`{"srv0":{"routes":[
				{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.9:3000"}]}]}
			]}}`),
			want: []Site{{Server: "srv0", Host: "", Upstreams: []string{"10.0.0.9:3000"}}},
		},
		{
			name: "matcher without a host is also a catch-all",
			config: serversConfig(`{"srv0":{"routes":[
				{"match":[{"path":["/api/*"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.9:3000"}]}]}
			]}}`),
			want: []Site{{Server: "srv0", Host: "", Upstreams: []string{"10.0.0.9:3000"}}},
		},
		{
			name: "host and path in one matcher use the host",
			config: serversConfig(`{"srv0":{"routes":[
				{"match":[{"host":["app.example.com"],"path":["/api/*"]}],"handle":[
					{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.7:80"}]}
				]}
			]}}`),
			want: []Site{{Server: "srv0", Host: "app.example.com", Upstreams: []string{"10.0.0.7:80"}}},
		},
		{
			name: "upstreams are found through nested subroutes",
			config: serversConfig(`{"srv0":{"routes":[
				{"match":[{"host":["deep.example.com"]}],"handle":[{"handler":"subroute","routes":[
					{"handle":[{"handler":"subroute","routes":[
						{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.8:80"}]}]}
					]}]}
				]}]}
			]}}`),
			want: []Site{{Server: "srv0", Host: "deep.example.com", Upstreams: []string{"10.0.0.8:80"}}},
		},
		{
			name: "several upstreams keep their order",
			config: serversConfig(`{"srv0":{"routes":[
				{"match":[{"host":["lb.example.com"]}],"handle":[
					{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.2:80"},{"dial":"10.0.0.1:80"}]}
				]}
			]}}`),
			want: []Site{{Server: "srv0", Host: "lb.example.com", Upstreams: []string{"10.0.0.2:80", "10.0.0.1:80"}}},
		},
		{
			name: "routes of the same host are merged without duplicates",
			config: serversConfig(`{"srv0":{"routes":[
				{"match":[{"host":["app.example.com"],"path":["/api/*"]}],"handle":[
					{"handler":"reverse_proxy","upstreams":[{"dial":"x:1"},{"dial":"y:1"}]}
				]},
				{"match":[{"host":["app.example.com"]}],"handle":[
					{"handler":"reverse_proxy","upstreams":[{"dial":"y:1"},{"dial":"z:1"}]}
				]}
			]}}`),
			want: []Site{{Server: "srv0", Host: "app.example.com", Upstreams: []string{"x:1", "y:1", "z:1"}}},
		},
		{
			name: "upstreams without a dial address are ignored",
			config: serversConfig(`{"srv0":{"routes":[
				{"match":[{"host":["srv.example.com"]}],"handle":[
					{"handler":"reverse_proxy","upstreams":[{"lookup_srv":"_http._tcp.backend"},{"dial":"1.1.1.1:80"}]}
				]}
			]}}`),
			want: []Site{{Server: "srv0", Host: "srv.example.com", Upstreams: []string{"1.1.1.1:80"}}},
		},
		{
			name: "servers are sorted by name and routes keep config order",
			config: serversConfig(`{
				"srv1":{"routes":[{"match":[{"host":["z.example.com"]}],"handle":[]},{"match":[{"host":["m.example.com"]}],"handle":[]}]},
				"srv0":{"routes":[{"match":[{"host":["b.example.com"]}],"handle":[]},{"match":[{"host":["a.example.com"]}],"handle":[]}]}
			}`),
			want: []Site{
				{Server: "srv0", Host: "b.example.com", Upstreams: []string{}},
				{Server: "srv0", Host: "a.example.com", Upstreams: []string{}},
				{Server: "srv1", Host: "z.example.com", Upstreams: []string{}},
				{Server: "srv1", Host: "m.example.com", Upstreams: []string{}},
			},
		},
		{
			name:   "same host on two servers stays separate",
			config: serversConfig(`{"srv0":{"routes":[{"match":[{"host":["a.example.com"]}],"handle":[]}]},"srv1":{"routes":[{"match":[{"host":["a.example.com"]}],"handle":[]}]}}`),
			want: []Site{
				{Server: "srv0", Host: "a.example.com", Upstreams: []string{}},
				{Server: "srv1", Host: "a.example.com", Upstreams: []string{}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractSites(parseConfig(t, tt.config))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExtractSites =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestExtractSitesWithNoData(t *testing.T) {
	configs := map[string]string{
		"empty config":          `{}`,
		"no http app":           `{"apps":{}}`,
		"no servers":            serversConfig(`{}`),
		"server without routes": serversConfig(`{"srv0":{"listen":[":443"]}}`),
		"server with no routes": serversConfig(`{"srv0":{"routes":[]}}`),
	}
	for name, raw := range configs {
		t.Run(name, func(t *testing.T) {
			got := ExtractSites(parseConfig(t, raw))
			if got == nil || len(got) != 0 {
				t.Errorf("ExtractSites = %#v, want an empty non-nil slice (it is encoded as [], not null)", got)
			}
		})
	}
	t.Run("nil config", func(t *testing.T) {
		if got := ExtractSites(nil); got == nil || len(got) != 0 {
			t.Errorf("ExtractSites(nil) = %#v, want an empty non-nil slice", got)
		}
	})
}

func TestExtractSitesToleratesMalformedConfig(t *testing.T) {
	configs := map[string]string{
		"routes is a string":      serversConfig(`{"srv0":{"routes":"oops"}}`),
		"server is a number":      serversConfig(`{"srv0":42}`),
		"servers is a list":       serversConfig(`[]`),
		"match is an object":      serversConfig(`{"srv0":{"routes":[{"match":{"host":["a"]},"handle":[]}]}}`),
		"host is a string":        serversConfig(`{"srv0":{"routes":[{"match":[{"host":"a.example.com"}],"handle":[]}]}}`),
		"handle is a string":      serversConfig(`{"srv0":{"routes":[{"match":[{"host":["a"]}],"handle":"x"}]}}`),
		"upstreams is an object":  serversConfig(`{"srv0":{"routes":[{"handle":[{"handler":"reverse_proxy","upstreams":{}}]}]}}`),
		"dial is a number":        serversConfig(`{"srv0":{"routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":7}]}]}]}}`),
		"route is null":           serversConfig(`{"srv0":{"routes":[null]}}`),
		"handler entry is a list": serversConfig(`{"srv0":{"routes":[{"handle":[[]]}]}}`),
	}
	for name, raw := range configs {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ExtractSites panicked: %v", r)
				}
			}()
			if got := ExtractSites(parseConfig(t, raw)); got == nil {
				t.Error("ExtractSites returned nil, want a non-nil slice")
			}
		})
	}
}

// proxyRoute is a route that matches host (empty: no host matcher) and proxies to dial, in the shape the Caddyfile
// adapter writes for `handle @host { reverse_proxy dial }`: the proxy sits in a subroute of its own.
func proxyRoute(host, dial string) string {
	match := ""
	if host != "" {
		match = `"match":[{"host":["` + host + `"]}],`
	}
	return `{` + match + `"handle":[{"handler":"subroute","routes":[
		{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"` + dial + `"}]}]}
	]}],"terminal":true}`
}

// wildcardRoute is a route for host whose subroute holds the given inner routes, as `*.example.com { ... }` with one
// `handle @name` block per subdomain is adapted.
func wildcardRoute(host string, inner ...string) string {
	joined := ""
	for i, r := range inner {
		if i > 0 {
			joined += ","
		}
		joined += r
	}
	return `{"match":[{"host":["` + host + `"]}],"handle":[{"handler":"subroute","routes":[` + joined + `]}],"terminal":true}`
}

func TestExtractSitesFollowsHostsInsideSubroutes(t *testing.T) {
	abort := `{"handle":[{"handler":"static_response","abort":true}]}`
	tests := []struct {
		name   string
		config string
		want   []Site
	}{
		{
			name: "a wildcard with one route per subdomain gives one site per subdomain, and not the wildcard",
			config: serversConfig(`{"srv0":{"routes":[` + wildcardRoute("*.example.com",
				proxyRoute("app.example.com", "10.0.0.1:8080"),
				proxyRoute("vault.example.com", "10.0.0.2:8000")) + `]}}`),
			want: []Site{
				{Server: "srv0", Host: "app.example.com", Upstreams: []string{"10.0.0.1:8080"}},
				{Server: "srv0", Host: "vault.example.com", Upstreams: []string{"10.0.0.2:8000"}},
			},
		},
		{
			name: "a fallback that answers for unknown subdomains is not a site",
			config: serversConfig(`{"srv0":{"routes":[` + wildcardRoute("*.example.com",
				proxyRoute("app.example.com", "10.0.0.1:8080"), abort) + `]}}`),
			want: []Site{{Server: "srv0", Host: "app.example.com", Upstreams: []string{"10.0.0.1:8080"}}},
		},
		{
			name: "a fallback that proxies is the wildcard's own site, after the subdomains before it",
			config: serversConfig(`{"srv0":{"routes":[` + wildcardRoute("*.example.com",
				proxyRoute("app.example.com", "10.0.0.1:8080"), proxyRoute("", "10.0.0.9:80")) + `]}}`),
			want: []Site{
				{Server: "srv0", Host: "app.example.com", Upstreams: []string{"10.0.0.1:8080"}},
				{Server: "srv0", Host: "*.example.com", Upstreams: []string{"10.0.0.9:80"}},
			},
		},
		{
			name: "a subdomain served by two inner routes is one site, and an upstream is listed once",
			config: serversConfig(`{"srv0":{"routes":[` + wildcardRoute("*.example.com",
				proxyRoute("guaca.example.com", "10.0.0.5:8080"),
				proxyRoute("guaca.example.com", "10.0.0.5:8080"),
				proxyRoute("guaca.example.com", "10.0.0.6:8080")) + `]}}`),
			want: []Site{{Server: "srv0", Host: "guaca.example.com", Upstreams: []string{"10.0.0.5:8080", "10.0.0.6:8080"}}},
		},
		{
			name: "an inner route that names several hosts gives one site each",
			config: serversConfig(`{"srv0":{"routes":[` + wildcardRoute("*.example.com", `{"match":[{"host":["a.example.com","b.example.com"]}],"handle":[
				{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.3:80"}]}
			]}`) + `]}}`),
			want: []Site{
				{Server: "srv0", Host: "a.example.com", Upstreams: []string{"10.0.0.3:80"}},
				{Server: "srv0", Host: "b.example.com", Upstreams: []string{"10.0.0.3:80"}},
			},
		},
		{
			name: "a host with its own proxy and routes for other hosts inside lists its own site first",
			config: serversConfig(`{"srv0":{"routes":[{"match":[{"host":["x.example.com"]}],"handle":[
				{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.1:80"}]},
				{"handler":"subroute","routes":[` + proxyRoute("y.example.com", "10.0.0.2:80") + `]}
			]}]}}`),
			want: []Site{
				{Server: "srv0", Host: "x.example.com", Upstreams: []string{"10.0.0.1:80"}},
				{Server: "srv0", Host: "y.example.com", Upstreams: []string{"10.0.0.2:80"}},
			},
		},
		{
			name: "hosts nested more than one level deep are found",
			config: serversConfig(`{"srv0":{"routes":[` + wildcardRoute("*.example.com", wildcardRoute("*.eu.example.com",
				proxyRoute("a.eu.example.com", "10.0.1.1:80"))) + `]}}`),
			want: []Site{{Server: "srv0", Host: "a.eu.example.com", Upstreams: []string{"10.0.1.1:80"}}},
		},
		{
			// Guards what already worked: inner routes without a host belong to the host that holds them.
			name: "inner routes without a host are merged into the outer host",
			config: serversConfig(`{"srv0":{"routes":[` + wildcardRoute("app.example.com",
				`{"match":[{"path":["/api/*"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.1:80"}]}]}`,
				`{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.2:80"}]}]}`) + `]}}`),
			want: []Site{{Server: "srv0", Host: "app.example.com", Upstreams: []string{"10.0.0.1:80", "10.0.0.2:80"}}},
		},
		{
			name: "a host whose subroute proxies nothing is still listed, with no upstreams",
			config: serversConfig(`{"srv0":{"routes":[` + wildcardRoute("docs.example.com",
				`{"handle":[{"handler":"file_server"}]}`) + `]}}`),
			want: []Site{{Server: "srv0", Host: "docs.example.com", Upstreams: []string{}}},
		},
		{
			name: "a catch-all route with a subroute is still one catch-all site",
			config: serversConfig(`{"srv0":{"routes":[{"handle":[{"handler":"subroute","routes":[` +
				`{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.9:80"}]}]}]}]}]}}`),
			want: []Site{{Server: "srv0", Host: "", Upstreams: []string{"10.0.0.9:80"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractSites(parseConfig(t, tt.config))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExtractSites =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestExtractSitesToleratesMalformedSubroutes(t *testing.T) {
	configs := map[string]string{
		"inner routes is a string":   serversConfig(`{"srv0":{"routes":[{"match":[{"host":["a.example.com"]}],"handle":[{"handler":"subroute","routes":"oops"}]}]}}`),
		"inner route is null":        serversConfig(`{"srv0":{"routes":[{"match":[{"host":["a.example.com"]}],"handle":[{"handler":"subroute","routes":[null]}]}]}}`),
		"inner match is an object":   serversConfig(`{"srv0":{"routes":[{"handle":[{"handler":"subroute","routes":[{"match":{"host":["b"]},"handle":[]}]}]}]}}`),
		"inner host is a number":     serversConfig(`{"srv0":{"routes":[{"handle":[{"handler":"subroute","routes":[{"match":[{"host":[7]}],"handle":[]}]}]}]}}`),
		"subroute inside a subroute": serversConfig(`{"srv0":{"routes":[{"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"subroute"}]}]}]}]}}`),
	}
	for name, raw := range configs {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ExtractSites panicked: %v", r)
				}
			}()
			if got := ExtractSites(parseConfig(t, raw)); got == nil {
				t.Error("ExtractSites returned nil, want a non-nil slice")
			}
		})
	}
}

func TestCountHealthChecks(t *testing.T) {
	proxy := func(extra string) string {
		return `{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.1:80"}]` + extra + `}`
	}
	tests := []struct {
		name         string
		handles      string
		wantProxies  int
		wantWithTest int
	}{
		{"no proxies", `{"handler":"static_response"}`, 0, 0},
		{"a proxy without checks", proxy(``), 1, 0},
		{"active checks", proxy(`,"health_checks":{"active":{"uri":"/health"}}`), 1, 1},
		{"passive checks", proxy(`,"health_checks":{"passive":{"fail_duration":30000000000}}`), 1, 1},
		{"an empty health_checks object is no check", proxy(`,"health_checks":{}`), 1, 0},
		{"one with and one without", proxy(`,"health_checks":{"active":{"uri":"/"}}`) + `,` + proxy(``), 2, 1},
		{"a proxy inside a subroute", `{"handler":"subroute","routes":[{"handle":[` + proxy(``) + `]}]}`, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := map[string]any{}
			raw := `{"apps":{"http":{"servers":{"srv0":{"routes":[{"match":[{"host":["a.example.com"]}],"handle":[` + tt.handles + `]}]}}}}}`
			if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
				t.Fatal(err)
			}
			proxies, withChecks := CountHealthChecks(cfg)
			if proxies != tt.wantProxies || withChecks != tt.wantWithTest {
				t.Errorf("CountHealthChecks = %d, %d; want %d, %d", proxies, withChecks, tt.wantProxies, tt.wantWithTest)
			}
		})
	}
	t.Run("an empty configuration", func(t *testing.T) {
		if p, w := CountHealthChecks(map[string]any{}); p != 0 || w != 0 {
			t.Errorf("CountHealthChecks = %d, %d; want 0, 0", p, w)
		}
	})
}
