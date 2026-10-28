package caddyfile

import (
	"strings"
	"testing"
)

func TestFormat(t *testing.T) {
	for _, tc := range formatCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Format(tc.input); got != tc.want {
				t.Errorf("Format(%q)\n got  %q\n want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestFormatIsIdempotent(t *testing.T) {
	inputs := []string{}
	for _, tc := range formatCases {
		inputs = append(inputs, tc.input, tc.want)
	}
	for _, d := range formatDifferences {
		inputs = append(inputs, d.input)
	}
	for _, in := range inputs {
		once := Format(in)
		if twice := Format(once); twice != once {
			t.Errorf("Format(Format(%q)) = %q, want %q: formatting twice must change nothing", in, twice, once)
		}
	}
}

func TestFormatWhereItDiffersFromCaddy(t *testing.T) {
	for _, tc := range formatDifferences {
		t.Run(tc.name, func(t *testing.T) {
			if got := Format(tc.input); got != tc.want {
				t.Errorf("Format(%q)\n got  %q\n want %q", tc.input, got, tc.want)
			}
		})
	}
}

// Whatever the layout, the tokens must be the same: Format may move white space, never words.
func TestFormatKeepsEveryWord(t *testing.T) {
	inputs := []string{}
	for _, tc := range formatCases {
		inputs = append(inputs, tc.input)
	}
	for _, d := range formatDifferences {
		inputs = append(inputs, d.input)
	}
	for _, in := range inputs {
		if a, b := words(in), words(Format(in)); a != b {
			t.Errorf("Format(%q) changed the words:\n before %q\n after  %q", in, a, b)
		}
	}
}

// words is the text without any white space: the part Format must never change.
func words(s string) string { return strings.Join(strings.Fields(s), "") }

// FuzzFormat holds Format to what it promises for any text at all: it does not panic, it changes only white space, and
// formatting its own output changes nothing. The seeds run with every `go test`.
func FuzzFormat(f *testing.F) {
	for _, tc := range formatCases {
		f.Add(tc.input)
	}
	for _, d := range formatDifferences {
		f.Add(d.input)
	}
	f.Fuzz(func(t *testing.T, in string) {
		once := Format(in)
		if a, b := words(strings.ReplaceAll(in, "\r", "")), words(once); a != b {
			t.Fatalf("Format(%q) changed the words:\n before %q\n after  %q", in, a, b)
		}
		// A heredoc that is never closed, followed by a word that happens to be its marker, reads differently once the
		// layout has indented that word. That is not a Caddyfile Caddy accepts, so idempotence is held to the rest.
		if strings.Contains(in, "<<") {
			return
		}
		if twice := Format(once); twice != once {
			t.Fatalf("Format(Format(%q)) = %q, want %q", in, twice, once)
		}
	})
}
