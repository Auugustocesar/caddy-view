package caddyfile

import (
	"errors"
	"strings"
	"testing"
)

const importedFile = "/etc/caddy/conf.d/caddy-view-sites.caddy"

func TestAddImport(t *testing.T) {
	const site = "example.com {\n\trespond \"hi\"\n}\n"
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"a Caddyfile with sites only gets it on top", site,
			"import " + importedFile + "\n\n" + site},
		{"an empty Caddyfile is just the import", "", "import " + importedFile + "\n"},
		{"white space only is the same as empty", "\n\n  \n", "import " + importedFile + "\n"},
		{"comments at the top stay above it", "# my server\n# since 2024\n\n" + site,
			"# my server\n# since 2024\n\nimport " + importedFile + "\n\n" + site},
		{"a comment glued to the first site stays with it", "# the blog\n" + site,
			"import " + importedFile + "\n\n# the blog\n" + site},
		{"only comments", "# nothing here yet\n", "# nothing here yet\n\nimport " + importedFile + "\n"},
		{"the global options block stays first", "{\n\tadmin localhost:2019\n}\n\n" + site,
			"{\n\tadmin localhost:2019\n}\n\nimport " + importedFile + "\n\n" + site},
		{"even without a blank line after it", "{\n\tadmin off\n}\n" + site,
			"{\n\tadmin off\n}\n\nimport " + importedFile + "\n\n" + site},
		{"a global block with blocks inside it is skipped whole", "{\n\tlog {\n\t\toutput stdout\n\t}\n\tadmin off\n}\n" + site,
			"{\n\tlog {\n\t\toutput stdout\n\t}\n\tadmin off\n}\n\nimport " + importedFile + "\n\n" + site},
		{"comments before the global block", "# global\n{\n\tadmin off\n}\n" + site,
			"# global\n{\n\tadmin off\n}\n\nimport " + importedFile + "\n\n" + site},
		{"braces in a comment or a string do not end the block", "{\n\t# }\n\tadmin \"}\"\n}\n" + site,
			"{\n\t# }\n\tadmin \"}\"\n}\n\nimport " + importedFile + "\n\n" + site},
		{"a placeholder is not a block", "{$ADMIN}\n" + site, "import " + importedFile + "\n\n{$ADMIN}\n" + site},
		{"a snippet at the top is not the global block", "(log) {\n\tlog\n}\n" + site,
			"import " + importedFile + "\n\n(log) {\n\tlog\n}\n" + site},
		{"a global block only counts when it is first", site + "\n{\n\tadmin off\n}\n",
			"import " + importedFile + "\n\n" + site + "\n{\n\tadmin off\n}\n"},
		{"the global block alone", "{\n\tadmin off\n}\n", "{\n\tadmin off\n}\n\nimport " + importedFile + "\n"},
		{"a global block with its brace on the line above is still the global block", "{\n\tadmin off\n}", "{\n\tadmin off\n}\n\nimport " + importedFile + "\n"},
		{"a text that does not end in a newline", "example.com {\n\trespond hi\n}", "import " + importedFile + "\n\nexample.com {\n\trespond hi\n}"},
		{"CRLF line ends are kept", "{\r\n\tadmin off\r\n}\r\nexample.com {\r\n}\r\n",
			"{\r\n\tadmin off\r\n}\r\n\r\nimport " + importedFile + "\r\n\r\nexample.com {\r\n}\r\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AddImport(tc.input, importedFile)
			if err != nil {
				t.Fatalf("AddImport() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("AddImport(%q)\n got  %q\n want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestAddImportWritesAPathCaddyReadsBack(t *testing.T) {
	const dir = "/etc/caddy"
	for _, file := range []string{
		"/etc/caddy/conf.d/caddy-view-sites.caddy",
		"/srv/my sites/caddy-view-sites.caddy",
		"C:/Users/me/caddy/conf.d/caddy-view-sites.caddy",
		"C:/Users/me/My Caddy/conf.d/sites.caddy",
	} {
		t.Run(file, func(t *testing.T) {
			got, err := AddImport("example.com {\n}\n", file)
			if err != nil {
				t.Fatal(err)
			}
			if !ImportsFile(got, dir, file) {
				t.Errorf("the result does not import %q:\n%s", file, got)
			}
		})
	}
}

func TestAddImportQuotesAPathWithSpaces(t *testing.T) {
	got, err := AddImport("", "/srv/my sites/sites.caddy")
	if err != nil || got != "import \"/srv/my sites/sites.caddy\"\n" {
		t.Errorf("AddImport() = %q, %v, want the path in double quotes", got, err)
	}
}

func TestAddImportRefusesAPathItCannotWriteSafely(t *testing.T) {
	for _, file := range []string{"", "/a\"b.caddy", "/a`b.caddy", "/a{b}.caddy", "/a\nb.caddy", "/a\rb.caddy", "relative/sites.caddy", "-"} {
		t.Run(strings.ReplaceAll(file, "\n", `\n`), func(t *testing.T) {
			if got, err := AddImport("example.com {\n}\n", file); !errors.Is(err, ErrUnsafeImportPath) || got != "" {
				t.Errorf("AddImport(%q) = %q, %v, want ErrUnsafeImportPath", file, got, err)
			}
		})
	}
}

// What AddImport writes is already laid out the way caddy fmt lays it out, so the next Format changes nothing.
func TestAddImportLeavesAFormattedCaddyfileFormatted(t *testing.T) {
	for _, tc := range []string{
		"example.com {\n\trespond \"hi\"\n}\n",
		"{\n\tadmin off\n}\n\nexample.com {\n\trespond \"hi\"\n}\n",
		"# a note\n\n(log) {\n\tlog\n}\n\nexample.com {\n\timport log\n}\n",
		"",
	} {
		got, err := AddImport(tc, importedFile)
		if err != nil {
			t.Fatal(err)
		}
		if formatted := Format(got); formatted != got {
			t.Errorf("AddImport(%q) = %q, but Format makes it %q", tc, got, formatted)
		}
	}
}

func TestAddImportKeepsEveryWord(t *testing.T) {
	for _, tc := range []string{"example.com {\n\trespond hi\n}\n", "{\n\tadmin off\n}\nexample.com {\n}\n", "# c\n{$X}\n", "{\n"} {
		got, err := AddImport(tc, importedFile)
		if err != nil {
			t.Fatal(err)
		}
		if want := words(tc) + words("import "+importedFile); len(words(got)) != len(want) {
			t.Errorf("AddImport(%q) = %q: the words are not the text plus the import", tc, got)
		}
	}
}

func TestAddImportDoesNotPanicOnBrokenText(t *testing.T) {
	for _, tc := range []string{"{", "}", "{{{", "}}}", "\"", "`", "<<EOF\n", "{\n\tadmin off\n"} {
		if _, err := AddImport(tc, importedFile); err != nil {
			t.Errorf("AddImport(%q) error = %v, want the import added somewhere", tc, err)
		}
	}
}

func FuzzAddImport(f *testing.F) {
	for _, tc := range formatCases {
		f.Add(tc.input)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got, err := AddImport(in, importedFile)
		if err != nil {
			t.Fatalf("AddImport(%q) error = %v", in, err)
		}
		// Whatever the text, the import is a directive of its own at the top level, and nothing else was lost.
		if !strings.Contains(got, "import "+importedFile) {
			t.Fatalf("AddImport(%q) = %q has no import line", in, got)
		}
		if a, b := words(strings.ReplaceAll(in, "\r", "")), strings.ReplaceAll(words(strings.ReplaceAll(got, "\r", "")), words("import "+importedFile), ""); a != b {
			t.Fatalf("AddImport(%q) changed more than it added:\n before %q\n after  %q", in, a, b)
		}
	})
}
