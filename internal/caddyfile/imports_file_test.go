package caddyfile

import "testing"

func TestImportsFile(t *testing.T) {
	const (
		dir  = "/etc/caddy"
		file = "conf.d/caddy-view-sites.caddy"
	)
	tests := []struct {
		name string
		text string
		file string
		want bool
	}{
		{"a relative glob", "import conf.d/*.caddy\n", file, true},
		{"the exact relative file", "import conf.d/caddy-view-sites.caddy\n", file, true},
		{"a leading dot slash", "import ./conf.d/*.caddy\n", file, true},
		{"quoted", "import \"conf.d/*.caddy\"\n", file, true},
		{"backtick quoted", "import `conf.d/*.caddy`\n", file, true},
		{"a trailing comment", "import conf.d/*.caddy # managed by caddy-view\n", file, true},
		{"indented, after global options", "{\n\tadmin off\n}\n\timport conf.d/*.caddy\n", file, true},
		{"after other imports", "import a/*.caddy\nimport conf.d/*.caddy\n", file, true},
		{"arguments after the pattern", "import conf.d/*.caddy one\n", file, true},
		{"Windows line endings", "import conf.d/*.caddy\r\n", file, true},
		{"an absolute glob", "import /etc/caddy/conf.d/*.caddy\n", file, true},
		{"the exact absolute file", "import /etc/caddy/conf.d/caddy-view-sites.caddy\n", file, true},
		{"an absolute file asked about by its absolute path", "import /etc/caddy/conf.d/*.caddy\n", "/etc/caddy/conf.d/caddy-view-sites.caddy", true},
		{"a relative glob asked about by an absolute path", "import conf.d/*.caddy\n", "/etc/caddy/conf.d/caddy-view-sites.caddy", true},

		{"commented out", "# import conf.d/*.caddy\n", file, false},
		{"another folder", "import snippets/*\n", file, false},
		{"another folder, absolute", "import /srv/other/*.caddy\n", file, false},
		{"a glob that does not match", "import conf.d/*.conf\n", file, false},
		{"a glob that does not cross a folder", "import *.caddy\n", file, false},
		{"the word only starts with import", "important.example.com {\n\trespond ok\n}\n", file, false},
		{"empty", "", file, false},
		{"inside a heredoc", "example.com {\n\trespond <<EOF\nimport conf.d/*.caddy\nEOF\n}\n", file, false},
		{"inside a site block", "example.com {\n\timport conf.d/*.caddy\n}\n", file, false},
		{"a snippet of that name", "(conf.d/caddy-view-sites.caddy) {\n\trespond ok\n}\nimport other\n", file, false},
		{"a placeholder pattern cannot be matched", "import {$CONF}/*.caddy\n", file, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ImportsFile(tt.text, dir, tt.file); got != tt.want {
				t.Errorf("ImportsFile(%q, %q, %q) = %v, want %v", tt.text, dir, tt.file, got, tt.want)
			}
		})
	}
}
