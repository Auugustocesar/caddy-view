package caddyfile

import (
	"errors"
	"strings"
)

// ErrUnsafeImportPath is returned for a path that cannot be written in an import line without changing its meaning.
var ErrUnsafeImportPath = errors.New("the path cannot be written safely in a Caddyfile")

// AddImport returns the Caddyfile text with an `import` of the file added at the top level: after the global options
// block when the text starts with one (comments aside), else before the first site or snippet, and before the comments
// that sit right on top of it. The file must be an absolute path; one with white space is quoted, and one with a quote,
// a brace or a line break is refused. Nothing else in the text changes, and the line ending of the text is kept.
func AddImport(text, file string) (string, error) {
	line, err := importLine(file)
	if err != nil {
		return "", err
	}
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	lf := strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(insertImport(lf, line), "\n", eol), nil
}

// importLine is the directive for the file.
func importLine(file string) (string, error) {
	if file == "" || !isAbsolute(file) || strings.ContainsAny(file, "\"`{}\r\n") {
		return "", ErrUnsafeImportPath
	}
	if strings.ContainsAny(file, " \t") {
		return `import "` + file + `"`, nil
	}
	return "import " + file, nil
}

// insertImport puts the line into LF text.
func insertImport(text, importLine string) string {
	lines := strings.Split(text, "\n")
	var content []fmtToken
	for _, t := range scan(text) {
		if t.kind != commentToken {
			content = append(content, t)
		}
	}
	if len(content) == 0 {
		// Nothing but comments, or nothing at all: the import goes last.
		base := strings.TrimRight(text, " \t\n")
		if base == "" {
			return importLine + "\n"
		}
		return base + "\n\n" + importLine + "\n"
	}

	if content[0].kind == openToken {
		if end, ok := closingLine(content); ok {
			return afterLine(lines, end, importLine)
		}
	}
	return beforeLine(lines, attachedComments(lines, content[0].line), importLine)
}

// closingLine finds the line of the brace that closes the block the tokens start with.
func closingLine(tokens []fmtToken) (int, bool) {
	depth := 0
	for _, t := range tokens {
		switch t.kind {
		case openToken:
			depth++
		case closeToken:
			depth--
			if depth == 0 {
				return t.line, true
			}
		}
	}
	return 0, false
}

// attachedComments backs up from a line over the comment lines that sit directly on top of it, with no blank line
// between: they belong to it and the import must not come between them.
func attachedComments(lines []string, at int) int {
	for at > 0 && strings.HasPrefix(strings.TrimSpace(lines[at-1]), "#") {
		at--
	}
	return at
}

// beforeLine puts the import before lines[at], set apart from what comes before it and after it by a blank line.
func beforeLine(lines []string, at int, importLine string) string {
	var b strings.Builder
	if at > 0 {
		b.WriteString(strings.Join(lines[:at], "\n"))
		b.WriteString("\n")
		if strings.TrimSpace(lines[at-1]) != "" {
			b.WriteString("\n")
		}
	}
	b.WriteString(importLine)
	b.WriteString("\n\n")
	b.WriteString(strings.Join(lines[at:], "\n"))
	return b.String()
}

// afterLine puts the import after lines[end], set apart from the block before it and from what follows.
func afterLine(lines []string, end int, importLine string) string {
	rest := lines[end+1:]
	for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
		rest = rest[1:]
	}
	out := strings.Join(lines[:end+1], "\n") + "\n\n" + importLine + "\n"
	if len(rest) > 0 {
		out += "\n" + strings.Join(rest, "\n")
	}
	return out
}
