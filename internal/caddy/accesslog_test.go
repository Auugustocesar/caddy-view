package caddy

import (
	"reflect"
	"testing"
)

func loggingConfig(logs string) string {
	return `{"logging":{"logs":` + logs + `}}`
}

func TestAccessLogFiles(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   []string
	}{
		{
			name: "a JSON file logger",
			config: loggingConfig(`{"log0":{"writer":{"output":"file","filename":"/var/log/caddy/access.log"},
				"encoder":{"format":"json"}}}`),
			want: []string{"/var/log/caddy/access.log"},
		},
		{
			name: "stderr loggers are ignored",
			config: loggingConfig(`{"default":{"writer":{"output":"stderr"},"encoder":{"format":"json"}},
				"log0":{"writer":{"output":"file","filename":"/a.log"},"encoder":{"format":"json"}}}`),
			want: []string{"/a.log"},
		},
		{
			name: "a console encoder is ignored",
			config: loggingConfig(`{"log0":{"writer":{"output":"file","filename":"/a.log"},
				"encoder":{"format":"console"}}}`),
			want: []string{},
		},
		{
			name:   "a logger without an encoder is not trusted to be JSON",
			config: loggingConfig(`{"log0":{"writer":{"output":"file","filename":"/a.log"}}}`),
			want:   []string{},
		},
		{
			name:   "a file writer without a filename is ignored",
			config: loggingConfig(`{"log0":{"writer":{"output":"file"},"encoder":{"format":"json"}}}`),
			want:   []string{},
		},
		{
			name:   "an empty filename is ignored",
			config: loggingConfig(`{"log0":{"writer":{"output":"file","filename":""},"encoder":{"format":"json"}}}`),
			want:   []string{},
		},
		{
			name: "several loggers are sorted by path and deduplicated",
			config: loggingConfig(`{
				"log2":{"writer":{"output":"file","filename":"/z.log"},"encoder":{"format":"json"}},
				"log0":{"writer":{"output":"file","filename":"/a.log"},"encoder":{"format":"json"}},
				"log1":{"writer":{"output":"file","filename":"/z.log"},"encoder":{"format":"json"}}
			}`),
			want: []string{"/a.log", "/z.log"},
		},
		{name: "no logging section", config: `{}`, want: []string{}},
		{name: "empty logs", config: loggingConfig(`{}`), want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AccessLogFiles(parseConfig(t, tt.config))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AccessLogFiles = %#v, want %#v", got, tt.want)
			}
		})
	}
	t.Run("nil config", func(t *testing.T) {
		if got := AccessLogFiles(nil); got == nil || len(got) != 0 {
			t.Errorf("AccessLogFiles(nil) = %#v, want an empty non-nil slice", got)
		}
	})
}

func TestAccessLogFilesToleratesMalformedConfig(t *testing.T) {
	configs := map[string]string{
		"logs is a list":       loggingConfig(`[]`),
		"logger is a string":   loggingConfig(`{"log0":"oops"}`),
		"writer is a list":     loggingConfig(`{"log0":{"writer":[],"encoder":{"format":"json"}}}`),
		"filename is a number": loggingConfig(`{"log0":{"writer":{"output":"file","filename":7},"encoder":{"format":"json"}}}`),
		"encoder is a string":  loggingConfig(`{"log0":{"writer":{"output":"file","filename":"/a.log"},"encoder":"json"}}`),
		"logger is null":       loggingConfig(`{"log0":null}`),
	}
	for name, raw := range configs {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("AccessLogFiles panicked: %v", r)
				}
			}()
			if got := AccessLogFiles(parseConfig(t, raw)); got == nil || len(got) != 0 {
				t.Errorf("AccessLogFiles = %#v, want an empty non-nil slice", got)
			}
		})
	}
}
