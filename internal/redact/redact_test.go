package redact

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decode(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("invalid test JSON: %v\n%s", err, raw)
	}
	return m
}

func TestConfig(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "password",
			in:   `{"password":"hunter2","username":"bob"}`,
			want: `{"password":"[redacted]","username":"bob"}`,
		},
		{
			name: "basic auth accounts inside an array",
			in:   `{"accounts":[{"username":"a","password":"$2a$14$abc"},{"username":"b","password":"$2a$14$def"}]}`,
			want: `{"accounts":[{"username":"a","password":"[redacted]"},{"username":"b","password":"[redacted]"}]}`,
		},
		{
			name: "ACME DNS provider token",
			in:   `{"provider":{"name":"cloudflare","api_token":"abcd1234"}}`,
			want: `{"provider":{"name":"cloudflare","api_token":"[redacted]"}}`,
		},
		{
			name: "suffix rules",
			in:   `{"signing_key":"k","client_secret":"s","db_password":"p","access_token":"t","db_passwd":"q"}`,
			want: `{"signing_key":"[redacted]","client_secret":"[redacted]","db_password":"[redacted]","access_token":"[redacted]","db_passwd":"[redacted]"}`,
		},
		{
			name: "exact names",
			in:   `{"token":"t","secret":"s","key":"/etc/ssl/private/site.pem","password":"p","passwd":"q"}`,
			want: `{"token":"[redacted]","secret":"[redacted]","key":"[redacted]","password":"[redacted]","passwd":"[redacted]"}`,
		},
		{
			name: "case does not matter",
			in:   `{"Password":"p","API_TOKEN":"t","Client_Secret":"s"}`,
			want: `{"Password":"[redacted]","API_TOKEN":"[redacted]","Client_Secret":"[redacted]"}`,
		},
		{
			name: "credential headers, with hyphens, hide the whole list",
			in:   `{"Authorization":["Bearer abc"],"X-Api-Key":["k"],"Cookie":["a=b"],"Set-Cookie":["s=1"],"Proxy-Authorization":["Basic x"],"Content-Type":["text/plain"]}`,
			want: `{"Authorization":"[redacted]","X-Api-Key":"[redacted]","Cookie":"[redacted]","Set-Cookie":"[redacted]","Proxy-Authorization":"[redacted]","Content-Type":["text/plain"]}`,
		},
		{
			name: "public keys are not secrets",
			in:   `{"public_key":"ssh-rsa AAAA"}`,
			want: `{"public_key":"ssh-rsa AAAA"}`,
		},
		{
			name: "look-alike names are kept",
			in:   `{"key_type":"ed25519","keyword":"x","monkey":"y","tokenizer":"z","secrets_dir":"/run/secrets","passwords_file":"f"}`,
			want: `{"key_type":"ed25519","keyword":"x","monkey":"y","tokenizer":"z","secrets_dir":"/run/secrets","passwords_file":"f"}`,
		},
		{
			name: "placeholders are references and stay visible",
			in:   `{"api_token":"{env.CF_API_TOKEN}","password":"{file./run/secrets/pw}","secret":"{env.A.B}"}`,
			want: `{"api_token":"{env.CF_API_TOKEN}","password":"{file./run/secrets/pw}","secret":"{env.A.B}"}`,
		},
		{
			name: "braces with spaces are not a placeholder",
			in:   `{"password":"{ not a placeholder }","token":"{partial"}`,
			want: `{"password":"[redacted]","token":"[redacted]"}`,
		},
		{
			name: "empty and null values have nothing to hide",
			in:   `{"password":"","token":null}`,
			want: `{"password":"","token":null}`,
		},
		{
			name: "non-string secrets are hidden whole",
			in:   `{"secret":{"a":1,"b":[2]},"token":123,"key":true}`,
			want: `{"secret":"[redacted]","token":"[redacted]","key":"[redacted]"}`,
		},
		{
			name: "secrets deep in arrays of objects",
			in:   `{"apps":{"http":{"servers":{"srv0":{"routes":[{"handle":[{"handler":"authentication","providers":{"http_basic":{"accounts":[{"password":"x"}]}}}]}]}}}}}`,
			want: `{"apps":{"http":{"servers":{"srv0":{"routes":[{"handle":[{"handler":"authentication","providers":{"http_basic":{"accounts":[{"password":"[redacted]"}]}}}]}]}}}}}`,
		},
		{
			name: "everything else is untouched",
			in:   `{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.1:80"}],"flush_interval":-1,"enabled":true,"nothing":null}`,
			want: `{"handler":"reverse_proxy","upstreams":[{"dial":"10.0.0.1:80"}],"flush_interval":-1,"enabled":true,"nothing":null}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Config(decode(t, tt.in))
			want := decode(t, tt.want)
			if !reflect.DeepEqual(got, want) {
				gotJSON, _ := json.Marshal(got)
				t.Errorf("Config =\n%s\nwant\n%s", gotJSON, tt.want)
			}
		})
	}
}

func TestConfigDoesNotModifyItsInput(t *testing.T) {
	const raw = `{"password":"p","nested":{"token":"t","list":[{"secret":"s"}]},"plain":"v"}`
	in := decode(t, raw)
	_ = Config(in)
	if !reflect.DeepEqual(in, decode(t, raw)) {
		t.Errorf("Config modified its input: %v", in)
	}
}

func TestConfigReturnsACopy(t *testing.T) {
	in := decode(t, `{"nested":{"plain":"v"},"list":["a"]}`)
	out := Config(in)
	out["nested"].(map[string]any)["plain"] = "changed"
	out["list"].([]any)[0] = "changed"
	if in["nested"].(map[string]any)["plain"] != "v" || in["list"].([]any)[0] != "a" {
		t.Errorf("the result shares memory with the input: %v", in)
	}
}

func TestConfigWithNothingToRedact(t *testing.T) {
	if got := Config(nil); got != nil {
		t.Errorf("Config(nil) = %v, want nil", got)
	}
	if got := Config(map[string]any{}); got == nil || len(got) != 0 {
		t.Errorf("Config(empty) = %#v, want an empty non-nil map", got)
	}
}
