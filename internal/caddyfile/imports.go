// Package caddyfile reads the parts of Caddyfile text that caddy-view must understand without asking Caddy.
package caddyfile

import (
	"strings"
)

// RelativeImport is an `import` of a file by a relative path, which Caddy resolves against its own working directory
// when it is given the text through the Admin API, and not against the directory of the Caddyfile.
type RelativeImport struct {
	// Pattern is the path or glob as written.
	Pattern string
	// Line is where the directive is, counting from 1.
	Line int
}

// RelativeImports returns the relative file imports of a Caddyfile, in order.
//
// It reads the text the way Caddy's lexer does, as far as `import` needs: tokens are separated by white space, a
// quoted or backtick string is one token, `#` at the start of a token begins a comment, and the lines of a heredoc are
// not Caddyfile. An `import` is a directive when it is the first token of a line or follows `{`. Its pattern counts as
// a relative file when it is not absolute, is not a snippet defined in the text, has no placeholder (it cannot be
// known before Caddy expands it) and has a path separator, a glob character or a dot, which a snippet name never has.
func RelativeImports(text string) []RelativeImport {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}

	tokens := make([][]token, len(lines))
	var heredoc string // the marker that ends the heredoc being skipped
	for i, l := range lines {
		if heredoc != "" {
			if strings.TrimSpace(l) == heredoc {
				heredoc = ""
			}
			continue
		}
		tokens[i] = tokenize(l)
		for _, tok := range tokens[i] {
			if marker, ok := heredocMarker(tok); ok {
				heredoc = marker
			}
		}
	}

	snippets := map[string]bool{}
	for _, line := range tokens {
		if len(line) >= 1 && !line[0].quoted && isSnippetName(line[0].text) {
			snippets[line[0].text[1:len(line[0].text)-1]] = true
		}
	}

	var out []RelativeImport
	for i, line := range tokens {
		for j, tok := range line {
			if tok.quoted || tok.text != "import" || (j > 0 && line[j-1].text != "{") || j+1 >= len(line) {
				continue
			}
			pattern := line[j+1].text
			if isRelativeFile(pattern, snippets) {
				out = append(out, RelativeImport{Pattern: pattern, Line: i + 1})
			}
		}
	}
	return out
}

type token struct {
	text   string
	quoted bool
}

// tokenize splits one line into tokens. A comment ends the line.
func tokenize(line string) []token {
	var out []token
	for i := 0; i < len(line); {
		switch c := line[i]; {
		case c == ' ' || c == '\t':
			i++
		case c == '#':
			return out
		case c == '"' || c == '`':
			var b strings.Builder
			i++
			for i < len(line) && line[i] != c {
				if c == '"' && line[i] == '\\' && i+1 < len(line) && line[i+1] == '"' {
					i++
				}
				b.WriteByte(line[i])
				i++
			}
			i++ // the closing quote, if there is one
			out = append(out, token{text: b.String(), quoted: true})
		default:
			start := i
			for i < len(line) && line[i] != ' ' && line[i] != '\t' {
				i++
			}
			out = append(out, token{text: line[start:i]})
		}
	}
	return out
}

// heredocMarker returns the marker of a heredoc that starts with this token (`<<EOF`).
func heredocMarker(t token) (string, bool) {
	if t.quoted || !strings.HasPrefix(t.text, "<<") || len(t.text) == 2 {
		return "", false
	}
	return t.text[2:], true
}

// isSnippetName reports whether a token defines a snippet: `(name)`.
func isSnippetName(s string) bool {
	return len(s) > 2 && s[0] == '(' && s[len(s)-1] == ')'
}

func isRelativeFile(pattern string, snippets map[string]bool) bool {
	switch {
	case pattern == "", pattern == "}", strings.Contains(pattern, "{"):
		return false
	case isAbsolute(pattern), snippets[pattern]:
		return false
	}
	return strings.ContainsAny(pattern, `/\*?[.`)
}

// isAbsolute reports whether a path is absolute on Unix or on Windows, whatever system this runs on, since the
// Caddyfile belongs to the system Caddy runs on.
func isAbsolute(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '/' || p[2] == '\\') &&
		(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}
