// Package promtext parses the Prometheus text exposition format, which is what Caddy serves at /metrics.
package promtext

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ErrSyntax means the input is not valid Prometheus text. The error names the line.
var ErrSyntax = errors.New("invalid Prometheus text")

// Sample is one line of the exposition: a metric name, its labels and its value. Timestamps are not kept, because
// the time of a reading is the time it was scraped.
type Sample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// Parse reads every sample of an exposition. Comment lines (HELP, TYPE and others) and blank lines are skipped. On
// a syntax error no samples are returned.
func Parse(data []byte) ([]Sample, error) {
	var out []Sample
	for n, raw := range bytes.Split(data, []byte{'\n'}) {
		line := strings.TrimSpace(string(raw))
		if line == "" || line[0] == '#' {
			continue
		}
		s, err := parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %w", ErrSyntax, n+1, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// parseLine parses one non-empty, non-comment line: name, optional labels, value, optional timestamp.
func parseLine(line string) (Sample, error) {
	name, rest := cutName(line, true)
	if name == "" {
		return Sample{}, errors.New("missing metric name")
	}
	s := Sample{Name: name, Labels: map[string]string{}}

	rest = strings.TrimLeft(rest, " \t")
	if strings.HasPrefix(rest, "{") {
		var err error
		if rest, err = parseLabels(rest[1:], s.Labels); err != nil {
			return Sample{}, err
		}
		if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			return Sample{}, errors.New("unexpected text after the labels")
		}
	}

	fields := strings.Fields(rest)
	if len(fields) == 0 || len(fields) > 2 {
		return Sample{}, errors.New("expected a value and an optional timestamp")
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return Sample{}, fmt.Errorf("bad value %q", fields[0])
	}
	s.Value = v
	if len(fields) == 2 {
		if _, err := strconv.ParseInt(fields[1], 10, 64); err != nil {
			return Sample{}, fmt.Errorf("bad timestamp %q", fields[1])
		}
	}
	return s, nil
}

// parseLabels reads label pairs up to the closing brace, which s starts after, and returns the text after it.
func parseLabels(s string, into map[string]string) (string, error) {
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return "", errors.New("unterminated label set")
		}
		if s[0] == '}' {
			return s[1:], nil
		}

		name, rest := cutName(s, false)
		if name == "" {
			return "", errors.New("bad label name")
		}
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, "=") {
			return "", fmt.Errorf("label %q has no value", name)
		}
		rest = strings.TrimLeft(rest[1:], " \t")
		value, after, err := parseQuoted(rest)
		if err != nil {
			return "", fmt.Errorf("label %q: %w", name, err)
		}
		into[name] = value

		s = strings.TrimLeft(after, " \t")
		switch {
		case strings.HasPrefix(s, ","):
			s = s[1:]
		case strings.HasPrefix(s, "}"):
		default:
			return "", errors.New("expected a comma or a closing brace")
		}
	}
}

// parseQuoted reads a double-quoted label value with the escapes \\, \" and \n, and returns the text after it.
func parseQuoted(s string) (value, rest string, err error) {
	if !strings.HasPrefix(s, `"`) {
		return "", "", errors.New("value is not quoted")
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			return b.String(), s[i+1:], nil
		case '\\':
			i++
			if i == len(s) {
				return "", "", errors.New("unterminated value")
			}
			switch s[i] {
			case '\\', '"':
				b.WriteByte(s[i])
			case 'n':
				b.WriteByte('\n')
			default:
				return "", "", fmt.Errorf("unknown escape \\%c", s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", "", errors.New("unterminated value")
}

// cutName splits a metric name (colons allowed) or a label name from the start of s.
func cutName(s string, allowColon bool) (name, rest string) {
	valid := func(c byte, first bool) bool {
		switch {
		case c == '_', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
			return true
		case c == ':':
			return allowColon
		}
		return !first && '0' <= c && c <= '9'
	}
	i := 0
	for i < len(s) && valid(s[i], i == 0) {
		i++
	}
	return s[:i], s[i:]
}

// FormatLabels renders labels in one canonical form: sorted by name, as {name="value",...}, with backslash, quote
// and newline escaped in the values. No labels give an empty string. The result identifies a series together with
// its metric name.
func FormatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}
	slices.Sort(names)

	var b strings.Builder
	b.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(name)
		b.WriteString(`="`)
		b.WriteString(escaper.Replace(labels[name]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

var escaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
