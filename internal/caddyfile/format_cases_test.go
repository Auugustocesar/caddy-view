package caddyfile

// formatCase is one input and what `caddy fmt` (Caddy 2.11.7) makes of it. The unit test checks Format against want;
// the integration test checks Format against a real Caddy on the same inputs, so want cannot drift from Caddy.
type formatCase struct {
	name  string
	input string
	want  string
}

// formatCases are the cases where Caddy's formatter does what its documentation says. The cases where it does not are
// in formatDifferences.
var formatCases = []formatCase{
	{"spaces between tokens collapse and indentation is rewritten", "abc   def\n\tghi jkl\nmno pqr", "abc def\nghi jkl\nmno pqr\n"},
	{"tabs between tokens collapse", "a\t\tb\tc", "a b c\n"},
	{"a final newline is added", "a {\n\tb\n}", "a {\n\tb\n}\n"},
	{"extra newlines at the end go", "a {\n\tb\n}   \n\n\n", "a {\n\tb\n}\n"},
	{"blank lines at the start go", "\n\n\na\n\n\n\nb\n", "a\n\nb\n"},
	{"empty input is one newline", "", "\n"},
	{"white space only is one newline", "  \n\t\n", "\n"},
	{"CRLF becomes LF", "a {\r\n\tb   c\r\n}\r\n", "a {\n\tb c\n}\n"},
	{"CR inside a block goes with the blank lines", "a {\r\n\tb\r\n\r\n\r\n\tc\r\n}", "a {\n\tb\n\n\tc\n}\n"},

	{"a brace gets a space before it", "a{\n\tb\n}\n\nc{ d\n}", "a {\n\tb\n}\n\nc {\n\td\n}\n"},
	{"a block that is all on one line is opened out", "a { b }", "a {\n\tb\n}\n"},
	{"nested blocks on one line", "a { b { c } }", "a {\n\tb {\n\t\tc\n\t}\n}\n"},
	{"a closing brace after a word gets its own line", "abc {\n\tdef\n}ghi{\n\tjkl mno\npqr}", "abc {\n\tdef\n}\n\nghi {\n\tjkl mno\n\tpqr\n}\n"},
	{"an opening brace on its own line joins the line above", "a\n{\n\tb\n}\n", "a {\n\tb\n}\n"},
	{"even with blank lines between", "import conf.d/*.caddy\n\n{\n\tadmin off\n}\n", "import conf.d/*.caddy {\n\tadmin off\n}\n"},
	{"the global options block stays on its own line", "{\nadmin off\n}\n\nexample.com{\nrespond hi\n}", "{\n\tadmin off\n}\n\nexample.com {\n\trespond hi\n}\n"},
	{"a block that follows a closed one is set apart", "a {\n\tb\n}\n{\n\tc\n}\n", "a {\n\tb\n}\n\n{\n\tc\n}\n"},
	{"blocks that follow each other keep one newline", "a {\n\tb\n}\nc {\n\td\n}\n", "a {\n\tb\n}\nc {\n\td\n}\n"},
	{"more than one blank line becomes one", "a {\n\tb\n}\n\n\n\nc {\n\td\n}\n", "a {\n\tb\n}\n\nc {\n\td\n}\n"},
	{"blank lines after an opening brace go, the others stay as one", "a {\n\n\n\tb\n\n\n\n\tc\n\n}\n", "a {\n\tb\n\n\tc\n}\n"},
	{"a blank line before a closing brace goes", "a {\n\tb\n\n}\n", "a {\n\tb\n}\n"},
	{"indentation follows nesting", "  a\nb\n\n\tc {\n\t\td\n\t}\n\n\te { f\n\t}\n\n\n\n\tg {\n\th {\n\ti\n\t}\n\t}\n\n\t\t{\n\tt\n\t\t}\n\n\tw {\n\t  x { y\n\t  }\n\t  z\n\t}\n\n\t@m\n\tn ",
		"a\nb\n\nc {\n\td\n}\n\ne {\n\tf\n}\n\ng {\n\th {\n\t\ti\n\t}\n}\n\n{\n\tt\n}\n\nw {\n\tx {\n\t\ty\n\t}\n\tz\n}\n\n@m\nn\n"},
	{"matcher blocks", "a {\n\t@m {\n\t\tpath /x\n\t}\n\thandle @m {\n\t\trespond hi\n\t}\n}\n", "a {\n\t@m {\n\t\tpath /x\n\t}\n\thandle @m {\n\t\trespond hi\n\t}\n}\n"},
	{"a snippet", "(log) {\n\tlog {\n\t\toutput file x\n\t}\n}\nexample.com {\n\timport log\n}", "(log) {\n\tlog {\n\t\toutput file x\n\t}\n}\nexample.com {\n\timport log\n}\n"},
	{"an address list", "a.com,   b.com {\n\trespond hi\n}", "a.com, b.com {\n\trespond hi\n}\n"},
	{"a named matcher", "example.com {\n\t@api path /api/*\n\treverse_proxy @api   127.0.0.1:9\n}", "example.com {\n\t@api path /api/*\n\treverse_proxy @api 127.0.0.1:9\n}\n"},
	{"unbalanced: too many opening braces", "a\n{\n\t{\n}\n", "a {\n\t{\n\t}\n"},
	{"unbalanced: too many closing braces", "a\n{\n\t{\n}}}\n", "a {\n\t{\n\t}\n}\n}\n"},
	{"unbalanced: a closing brace alone", "}\n", "}\n"},
	{"unbalanced: an opening brace alone", "{\n", "{\n"},

	{"a placeholder is not a block", "foo {bar}", "foo {bar}\n"},
	{"placeholders inside words", "foo{bar} foo{bar}baz", "foo{bar} foo{bar}baz\n"},
	{"a brace with a space after it opens a block even inside a word", "foo{bar} foo{ bar}baz", "foo{bar} foo {\n\tbar\n}\n\nbaz\n"},
	{"environment placeholders", "{$A}\n\nb {\n{$C}\n}\n\nd { {$E}\n}\n\n{ {$F}\n}\n", "{$A}\n\nb {\n\t{$C}\n}\n\nd {\n\t{$E}\n}\n\n{\n\t{$F}\n}\n"},
	{"a placeholder with a port", ":{$PORT}", ":{$PORT}\n"},
	{"a placeholder as an argument", "a {\n\theader X-A {http.request.host}\n}\n", "a {\n\theader X-A {http.request.host}\n}\n"},
	{"placeholders with brackets", "(x) {\n\timport {args[0]}\n}\nimport x foo", "(x) {\n\timport {args[0]}\n}\nimport x foo\n"},

	{"a quoted string is kept as it is", "a {\n\trespond \"{ }\" 200\n}\n", "a {\n\trespond \"{ }\" 200\n}\n"},
	{"quoted JSON", "foo\nbar      \"{\\\"key\\\":34}\"\n", "foo\nbar \"{\\\"key\\\":34}\"\n"},
	{"quotes keep their spaces and their line breaks", "a {\n\trespond \"line1\nline2   x\" 200\n}\n", "a {\n\trespond \"line1\nline2   x\" 200\n}\n"},
	{"quotes with escapes next to a comment sign", "\"a \\\"b\\\" \"#c\n\td\n\ne {\n\"f\"\n}\n\ng { \"h\"\n}\n\ni {\n\t\"\\\"\"\n}", "\"a \\\"b\\\" \"#c\nd\n\ne {\n\t\"f\"\n}\n\ng {\n\t\"h\"\n}\n\ni {\n\t\"\\\"\"\n}\n"},
	{"a hash inside quotes is not a comment", "a {\n\trespond \"a # b\" 200\n}\n", "a {\n\trespond \"a # b\" 200\n}\n"},
	{"a backtick string is kept as it is", "a {\n\trespond `x   y`\n}\n", "a {\n\trespond `x   y`\n}\n"},
	{"braces in a backtick string are not blocks", "a {\n\trespond `{ }`   b\n}\n", "a {\n\trespond `{ }` b\n}\n"},
	{"a heredoc is kept as it is, and the line goes on after its end", "a {\n\trespond <<EOF\n  hello   world\n    { }\nEOF 200\n}\n", "a {\n\trespond <<EOF\n  hello   world\n    { }\nEOF 200\n}\n"},
	{"a heredoc keeps its indentation", "a {\n\trespond <<EOF\n\t\ttext\n\tEOF\n}\n", "a {\n\trespond <<EOF\n\t\ttext\n\tEOF\n}\n"},

	{"a comment on its own line", "# just a comment\n", "# just a comment\n"},
	{"comments inside a block", "a {\n\tb\n\t# note\n\tc\n}\n", "a {\n\tb\n\t# note\n\tc\n}\n"},
	{"blank lines around a comment after an opening brace", "a {\n\n\t# note\n\n\tb\n}\n", "a {\n\t# note\n\n\tb\n}\n"},
	{"a comment after an opening brace moves down", "a { # c\n\tb\n}\n", "a {\n\t# c\n\tb\n}\n"},
	{"a comment after a directive stays, trailing spaces and all", "a {   \n  b # note   \n}   \n", "a {\n\tb # note   \n}\n"},
	{"a comment hides a brace from the layout", "# a\n #b {\n\tc\n}\n", "# a\n#b {\nc\n}\n"},
	{"a hash after a word starts a comment", "a {\n\trespond #x\n}\n", "a {\n\trespond #x\n}\n"},

	{"a Unicode host and a quoted string", "é.example.com {\n\trespond \"olá   mundo\"\n}\n", "é.example.com {\n\trespond \"olá   mundo\"\n}\n"},
}

// formatDifferences are inputs where Caddy 2.11.7's formatter does something that cannot be called a layout rule: it
// changes what the text says or follows a quirk of its own. Format does not copy them; each says what Format does.
var formatDifferences = []struct {
	name  string
	input string
	// want is what Format must return. caddy is what Caddy returned when this was written.
	want, caddy string
}{
	{"a line that ends in a backslash keeps the indentation of the next one", "a {\n\tb \\\n\tc\n}\n", "a {\n\tb \\\n\tc\n}\n", "a {\n\tb \\\n c\n}\n"},
	{"a hash inside a word is part of the word", "a {\n\tfoo#bar {\n\t\tb\n\t}\n}", "a {\n\tfoo#bar {\n\t\tb\n\t}\n}\n", "a {\n\tfoo#bar {\n\tb\n}\n}\n"},
	{"a word right after a closing brace goes to its own line", "a {\n\tb {\n\t\tc\n\t}d\n}", "a {\n\tb {\n\t\tc\n\t}\n\td\n}\n", "a {\n\tb {\n\t\tc\n\t}\td\n}\n"},
	{"the space after << is kept", "a {\n\trespond <<  x\n}\n", "a {\n\trespond << x\n}\n", "a {\n\trespond <<x\n}\n"},
}
