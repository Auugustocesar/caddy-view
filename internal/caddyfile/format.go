package caddyfile

import (
	"regexp"
	"strings"
)

// Format returns the Caddyfile text laid out the way `caddy fmt` lays it out: tabs for nesting, one space between
// tokens, an opening brace at the end of its line, a closing brace on a line of its own, at most one blank line in a
// row and none at the edges of a block, and one newline at the end. Only white space changes; Format never touches a
// word, a quoted or backtick string, a heredoc, or the text of a comment.
//
// Where Caddy's own formatter does something that changes what the text says, Format does not follow it; the cases are
// listed in the tests.
func Format(text string) string {
	var f formatter
	for _, t := range scan(text) {
		f.token(t)
	}
	return f.result()
}

type tokenKind int

const (
	wordToken    tokenKind = iota // a word, a quoted or backtick string, a heredoc, or any mix of them
	commentToken                  // from a # that starts a token to the end of the line
	openToken                     // a { that opens a block
	closeToken                    // a } that closes a block
)

type fmtToken struct {
	kind tokenKind
	text string
	// newlines counts the line breaks between the previous token and this one; zero means the same line.
	newlines int
	// line is where the token starts, counting from 0.
	line int
}

// heredocName is what Caddy accepts as the marker of a heredoc.
var heredocName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// scan splits the text into tokens. Braces are the part that needs care: a { whose matching } comes before any white
// space is a placeholder and part of its word; any other { opens a block, and any } outside a placeholder closes one.
func scan(text string) []fmtToken {
	text = strings.ReplaceAll(text, "\r", "")
	var (
		tokens   []fmtToken
		word     strings.Builder
		newlines int
		line     int // the line being read
		wordLine int // the line the word being read started on
	)
	emit := func(kind tokenKind, s string) {
		at := line
		if kind == wordToken {
			at = wordLine
		}
		tokens = append(tokens, fmtToken{kind: kind, text: s, newlines: newlines, line: at})
		newlines = 0
	}
	// add puts text in the word, which may run over several lines when it is a string or a heredoc.
	add := func(s string) {
		if word.Len() == 0 {
			wordLine = line
		}
		word.WriteString(s)
		line += strings.Count(s, "\n")
	}
	endWord := func() {
		if word.Len() > 0 {
			emit(wordToken, word.String())
			word.Reset()
		}
	}

	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '\n':
			endWord()
			newlines++
			line++
			i++
		case c == ' ' || c == '\t':
			endWord()
			i++
		case c == '#' && word.Len() == 0:
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text) - i
			}
			emit(commentToken, text[i:i+end])
			i += end
		case (c == '"' || c == '`') && closingQuote(text, i) > 0:
			end := closingQuote(text, i)
			add(text[i:end])
			i = end
		case c == '<' && word.Len() == 0 && heredocEnd(text, i) > 0:
			end := heredocEnd(text, i)
			add(text[i:end])
			i = end
		case c == '{' && placeholderEnd(text, i) > 0:
			end := placeholderEnd(text, i)
			add(text[i:end])
			i = end
		case c == '{':
			endWord()
			emit(openToken, "{")
			i++
		case c == '}':
			endWord()
			emit(closeToken, "}")
			i++
		default:
			add(text[i : i+1])
			i++
		}
	}
	endWord()
	return tokens
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' }

// placeholderEnd returns the index just past the } that matches the { at text[start], or -1 when white space or the end
// of the text comes first: then the { is not a placeholder.
func placeholderEnd(text string, start int) int {
	depth := 0
	for i := start; i < len(text); i++ {
		switch c := text[i]; {
		case isSpace(c):
			return -1
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// closingQuote returns the index just past the string that starts at text[start], or -1 when it is never closed, in which
// case the quote is only a character of a word. A double-quoted string ends at the first quote that is not escaped; a
// backtick string ends at the next backtick.
func closingQuote(text string, start int) int {
	quote := text[start]
	for i := start + 1; i < len(text); i++ {
		switch {
		case quote == '"' && text[i] == '\\':
			i++
		case text[i] == quote:
			return i + 1
		}
	}
	return -1
}

// heredocEnd returns the index just past the closing marker of the heredoc that starts at text[start], which is where
// the line goes on. It is -1 when there is no heredoc: no marker that ends the line, or no closing marker.
func heredocEnd(text string, start int) int {
	if !strings.HasPrefix(text[start:], "<<") {
		return -1
	}
	nl := strings.IndexByte(text[start:], '\n')
	if nl < 0 {
		return -1
	}
	nl += start
	marker := text[start+2 : nl]
	if !heredocName.MatchString(marker) {
		return -1
	}
	for line := nl + 1; line <= len(text); {
		end := strings.IndexByte(text[line:], '\n')
		next := len(text) + 1
		if end < 0 {
			end = len(text) - line
		} else {
			next = line + end + 1
		}
		body := text[line : line+end]
		trimmed := strings.TrimLeft(body, " \t")
		if rest, ok := strings.CutPrefix(trimmed, marker); ok && (rest == "" || isSpace(rest[0])) {
			return line + (len(body) - len(trimmed)) + len(marker)
		}
		line = next
	}
	return -1
}

// formatter lays tokens out. A line is held in `line` until the next token shows whether it goes on.
type formatter struct {
	lines   []string
	line    strings.Builder
	indent  int // the nesting the held line is written at
	nesting int
	prev    tokenKind
	started bool // a token has been seen
	// afterOpen: the held line ends with the { that opened a block, and the next token starts a new line.
	afterOpen bool
	// blank: a blank line is wanted before the next line.
	blank bool
}

func (f *formatter) flush() {
	if f.line.Len() == 0 {
		return
	}
	if f.blank && len(f.lines) > 0 {
		f.lines = append(f.lines, "")
	}
	f.blank = false
	f.lines = append(f.lines, strings.Repeat("\t", f.indent)+f.line.String())
	f.line.Reset()
}

// begin starts a new line with s at the current nesting.
func (f *formatter) begin(s string) {
	f.flush()
	f.indent = f.nesting
	f.line.WriteString(s)
}

func (f *formatter) token(t fmtToken) {
	first := !f.started
	prev := f.prev
	f.started, f.prev = true, t.kind

	if f.afterOpen {
		// Whatever follows an opening brace goes on a line of its own, and blank lines there are dropped.
		f.afterOpen = false
		f.flush()
		t.newlines = 1
		f.blank = false
	}

	switch t.kind {
	case openToken:
		switch {
		case !first && prev == wordToken && f.line.Len() > 0:
			f.line.WriteString(" {")
		case prev == closeToken && f.nesting == 0:
			f.flush()
			f.blank = true
			f.begin("{")
		default:
			f.begin("{")
		}
		f.nesting++
		f.afterOpen = true

	case closeToken:
		f.flush()
		f.blank = false
		f.nesting = max(f.nesting-1, 0)
		f.begin("}")

	default:
		f.placeAfterBreak(t, first)
	}
}

// placeAfterBreak writes a word or a comment: on the line it follows when it is on the same line in the text, else on a
// new line, after a blank one when the text has one.
func (f *formatter) placeAfterBreak(t fmtToken, first bool) {
	switch {
	case f.line.Len() > 0 && t.newlines == 0 && f.lastIsClose():
		// A word right after a closing brace: after a top-level block it starts a new, separate item.
		f.flush()
		f.blank = f.nesting == 0
		f.begin(t.text)
	case f.line.Len() > 0 && t.newlines == 0:
		f.line.WriteString(" " + t.text)
	default:
		f.flush()
		if t.newlines >= 2 && !first && len(f.lines) > 0 {
			f.blank = true
		}
		f.begin(t.text)
	}
}

func (f *formatter) lastIsClose() bool { return f.line.String() == "}" }

func (f *formatter) result() string {
	f.flush()
	if len(f.lines) == 0 {
		return "\n"
	}
	return strings.Join(f.lines, "\n") + "\n"
}
