package doctor

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustConfig(t *testing.T, raw string) Config {
	t.Helper()
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("invalid test config: %v", err)
	}
	return cfg
}

func assertStatus(t *testing.T, got Result, want Status) {
	t.Helper()
	if got.Status != want {
		t.Fatalf("status = %q, want %q (message: %q)", got.Status, want, got.Message)
	}
	if got.ID == "" {
		t.Errorf("result has no ID")
	}
	if got.Message == "" {
		t.Errorf("result has no message")
	}
	if want != StatusOK && got.Hint == "" {
		t.Errorf("non-ok result has no hint")
	}
}

func TestCheckAdminTransport(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want Status
	}{
		{"unix socket URL", "unix:///run/caddy/admin.sock", StatusOK},
		{"unix socket caddy syntax", "unix//run/caddy/admin.sock", StatusOK},
		{"loopback hostname", "localhost:2019", StatusOK},
		{"loopback IPv4 URL", "http://127.0.0.1:2019", StatusOK},
		{"loopback IPv6", "[::1]:2019", StatusOK},
		{"LAN address is exposed", "http://192.168.50.123:2019", StatusWarn},
		{"all interfaces is exposed", "0.0.0.0:2019", StatusWarn},
		{"empty address", "", StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStatus(t, CheckAdminTransport(tt.addr), tt.want)
		})
	}
}

func TestCheckMetrics(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		want        Status
		mentionsAll []string
	}{
		{
			name:   "metrics enabled on the only server",
			config: `{"apps":{"http":{"servers":{"srv0":{"metrics":{}}}}}}`,
			want:   StatusOK,
		},
		{
			name:   "metrics missing",
			config: `{"apps":{"http":{"servers":{"srv0":{"listen":[":443"]}}}}}`,
			want:   StatusWarn,
		},
		{
			name:        "only some servers have metrics, missing ones are named",
			config:      `{"apps":{"http":{"servers":{"srv1":{},"srv0":{"metrics":{}},"srv2":{}}}}}`,
			want:        StatusWarn,
			mentionsAll: []string{"srv1", "srv2"},
		},
		{
			name:   "no HTTP app at all",
			config: `{}`,
			want:   StatusWarn,
		},
		{
			name:   "http app without servers",
			config: `{"apps":{"http":{"servers":{}}}}`,
			want:   StatusWarn,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckMetrics(mustConfig(t, tt.config))
			assertStatus(t, got, tt.want)
			for _, s := range tt.mentionsAll {
				if !strings.Contains(got.Message, s) {
					t.Errorf("message %q does not mention %q", got.Message, s)
				}
			}
			if tt.want != StatusOK && !strings.Contains(got.Hint, "metrics") {
				t.Errorf("hint %q does not mention the metrics option", got.Hint)
			}
		})
	}
}

// A server with an empty "logs" object has access logging enabled in Caddy's JSON config.
// v1 simplification: any logger writing JSON to a file counts, without mapping it to a specific server.
func TestCheckAccessLog(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   Status
	}{
		{
			name: "access logs enabled, JSON file logger",
			config: `{
				"apps":{"http":{"servers":{"srv0":{"logs":{}}}}},
				"logging":{"logs":{"log0":{"writer":{"output":"file","filename":"/var/log/caddy/access.log"},"encoder":{"format":"json"}}}}
			}`,
			want: StatusOK,
		},
		{
			name: "access logging not enabled on any server",
			config: `{
				"apps":{"http":{"servers":{"srv0":{}}}},
				"logging":{"logs":{"log0":{"writer":{"output":"file","filename":"/var/log/caddy/access.log"},"encoder":{"format":"json"}}}}
			}`,
			want: StatusWarn,
		},
		{
			name: "access logs go to stderr, there is no file to read",
			config: `{
				"apps":{"http":{"servers":{"srv0":{"logs":{}}}}},
				"logging":{"logs":{"log0":{"writer":{"output":"stderr"},"encoder":{"format":"json"}}}}
			}`,
			want: StatusWarn,
		},
		{
			name: "file logger but console encoder",
			config: `{
				"apps":{"http":{"servers":{"srv0":{"logs":{}}}}},
				"logging":{"logs":{"log0":{"writer":{"output":"file","filename":"/var/log/caddy/access.log"},"encoder":{"format":"console"}}}}
			}`,
			want: StatusWarn,
		},
		{
			name: "file logger without explicit encoder is not trusted to be JSON",
			config: `{
				"apps":{"http":{"servers":{"srv0":{"logs":{}}}}},
				"logging":{"logs":{"log0":{"writer":{"output":"file","filename":"/var/log/caddy/access.log"}}}}
			}`,
			want: StatusWarn,
		},
		{
			name: "one good logger among several is enough",
			config: `{
				"apps":{"http":{"servers":{"srv0":{"logs":{}}}}},
				"logging":{"logs":{
					"default":{"writer":{"output":"stderr"}},
					"log0":{"writer":{"output":"file","filename":"/var/log/caddy/access.log"},"encoder":{"format":"json"}}
				}}
			}`,
			want: StatusOK,
		},
		{
			name:   "empty config",
			config: `{}`,
			want:   StatusWarn,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStatus(t, CheckAccessLog(mustConfig(t, tt.config)), tt.want)
		})
	}
}

func TestCheckImport(t *testing.T) {
	const target = "conf.d/caddy-view-sites.caddy"
	tests := []struct {
		name      string
		caddyfile string
		want      Status
	}{
		{"glob import matches", "import conf.d/*.caddy\n", StatusOK},
		{"exact import matches", "import conf.d/caddy-view-sites.caddy\n", StatusOK},
		{"leading dot slash is ignored", "import ./conf.d/*.caddy\n", StatusOK},
		{"quoted argument", "import \"conf.d/*.caddy\"\n", StatusOK},
		{"trailing comment", "import conf.d/*.caddy # managed by caddy-view\n", StatusOK},
		{"indented inside global options", "{\n\tadmin off\n}\n\timport conf.d/*.caddy\n", StatusOK},
		{"commented out", "# import conf.d/*.caddy\n", StatusWarn},
		{"import of a different folder", "import snippets/*\n", StatusWarn},
		{"glob that does not match the target", "import conf.d/*.conf\n", StatusWarn},
		{"word starting with import is not the directive", "important.example.com {\n\trespond ok\n}\n", StatusWarn},
		{"empty Caddyfile", "", StatusWarn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStatus(t, CheckImport(tt.caddyfile, target), tt.want)
		})
	}
}
