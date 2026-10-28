package caddyfile

import (
	"reflect"
	"testing"
)

func TestRelativeImports(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []RelativeImport
	}{
		{"none", "example.com {\n\trespond ok\n}\n", nil},
		{"empty", "", nil},
		{"a relative glob", "import conf.d/*.caddy\n", []RelativeImport{{"conf.d/*.caddy", 1}}},
		{"after global options", "{\n\tadmin off\n}\nimport conf.d/*.caddy\n", []RelativeImport{{"conf.d/*.caddy", 4}}},
		{"dot slash", "import ./conf.d/*.caddy\n", []RelativeImport{{"./conf.d/*.caddy", 1}}},
		{"parent directory", "import ../shared/headers.caddy\n", []RelativeImport{{"../shared/headers.caddy", 1}}},
		{"a bare file name", "import sites.caddy\n", []RelativeImport{{"sites.caddy", 1}}},
		{"a glob without a slash", "import *.caddy\n", []RelativeImport{{"*.caddy", 1}}},
		{"quoted with a space", "import \"sites/a b.caddy\"\n", []RelativeImport{{"sites/a b.caddy", 1}}},
		{"backtick quoted", "import `conf.d/*.caddy`\n", []RelativeImport{{"conf.d/*.caddy", 1}}},
		{"arguments after the pattern", "import conf.d/*.caddy one two\n", []RelativeImport{{"conf.d/*.caddy", 1}}},
		{"inside a site block", "example.com {\n\timport snippets/headers.caddy\n}\n", []RelativeImport{{"snippets/headers.caddy", 2}}},
		{"after an opening brace on the same line", "example.com { import snippets/headers.caddy }\n", []RelativeImport{{"snippets/headers.caddy", 1}}},
		{"a trailing comment", "import conf.d/*.caddy # managed\n", []RelativeImport{{"conf.d/*.caddy", 1}}},
		{"Windows line endings", "import conf.d/*.caddy\r\nexample.com {\r\n}\r\n", []RelativeImport{{"conf.d/*.caddy", 1}}},
		{
			"several, in order",
			"import a/*.caddy\nexample.com {\n\timport b.caddy\n}\nimport /abs/c.caddy\nimport d/*.caddy\n",
			[]RelativeImport{{"a/*.caddy", 1}, {"b.caddy", 3}, {"d/*.caddy", 6}},
		},

		{"an absolute glob", "import /etc/caddy/conf.d/*.caddy\n", nil},
		{"an absolute path with a Windows drive", "import C:/caddy/conf.d/*.caddy\n", nil},
		{"an absolute path with a Windows drive and backslashes", `import C:\caddy\conf.d\*.caddy` + "\n", nil},
		{"a UNC path", `import \\server\share\sites.caddy` + "\n", nil},
		{"quoted and absolute", "import \"/etc/caddy/my sites/*.caddy\"\n", nil},
		{"commented out", "# import conf.d/*.caddy\n", nil},
		{"commented out after indentation", "\t# import conf.d/*.caddy\n", nil},
		{"a snippet defined in the text", "(cors) {\n\theader Access-Control-Allow-Origin *\n}\nexample.com {\n\timport cors\n}\n", nil},
		{"a snippet with arguments", "(tls) {\n\ttls {args[0]}\n}\nexample.com {\n\timport tls internal\n}\n", nil},
		{"a bare name that is not a file", "example.com {\n\timport cors\n}\n", nil},
		{"a placeholder in the pattern", "import {$CADDY_CONF}/*.caddy\n", nil},
		{"the word only starts with import", "important.example.com {\n\trespond ok\n}\n", nil},
		{"import is not the first token", "example.com {\n\trespond import conf.d/x.caddy\n}\n", nil},
		{"inside a heredoc", "example.com {\n\trespond <<EOF\nimport conf.d/*.caddy\nEOF\n}\n", nil},
		{"inside a quoted string", "example.com {\n\trespond \"import conf.d/*.caddy\"\n}\n", nil},
		{"an import with no pattern", "import\n", nil},
		{
			"a heredoc ends and imports count again",
			"example.com {\n\trespond <<EOF\nimport hidden/x.caddy\nEOF\n\timport shown/y.caddy\n}\n",
			[]RelativeImport{{"shown/y.caddy", 5}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RelativeImports(tt.text); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RelativeImports(%q) = %+v, want %+v", tt.text, got, tt.want)
			}
		})
	}
}
