package store

import "testing"

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestDataDir(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		vars    map[string]string
		want    string
		wantErr bool
	}{
		{"linux with XDG_DATA_HOME", "linux", map[string]string{"XDG_DATA_HOME": "/data", "HOME": "/home/u"}, "/data/caddy-view", false},
		{"linux falls back to ~/.local/share", "linux", map[string]string{"HOME": "/home/u"}, "/home/u/.local/share/caddy-view", false},
		{"linux ignores a relative XDG_DATA_HOME", "linux", map[string]string{"XDG_DATA_HOME": "relative", "HOME": "/home/u"}, "/home/u/.local/share/caddy-view", false},
		{"linux without any home", "linux", map[string]string{}, "", true},
		{"freebsd follows the XDG rules", "freebsd", map[string]string{"HOME": "/home/u"}, "/home/u/.local/share/caddy-view", false},
		{"darwin", "darwin", map[string]string{"HOME": "/Users/u"}, "/Users/u/Library/Application Support/caddy-view", false},
		{"darwin without HOME", "darwin", map[string]string{}, "", true},
		{"windows", "windows", map[string]string{"LocalAppData": `C:\Users\u\AppData\Local`}, `C:\Users\u\AppData\Local\caddy-view`, false},
		{"windows without LocalAppData", "windows", map[string]string{}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dataDir(tt.goos, env(tt.vars))
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want error: %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("dataDir = %q, want %q", got, tt.want)
			}
		})
	}
}
