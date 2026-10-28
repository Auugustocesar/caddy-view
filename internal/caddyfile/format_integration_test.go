//go:build integration

package caddyfile

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
)

// caddyFmt runs `caddy fmt -` from CADDY_BIN, or from the PATH.
func caddyFmt(t *testing.T, input string) string {
	t.Helper()
	bin := os.Getenv("CADDY_BIN")
	if bin == "" {
		bin, _ = exec.LookPath("caddy")
	}
	if bin == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("no Caddy binary: set CADDY_BIN or put caddy on the PATH")
		}
		t.Skip("no Caddy binary: set CADDY_BIN or put caddy on the PATH")
	}
	cmd := exec.Command(bin, "fmt", "-")
	cmd.Stdin = bytes.NewBufferString(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("caddy fmt: %v", err)
	}
	return string(out)
}

func TestFormatMatchesCaddyFmt(t *testing.T) {
	for _, tc := range formatCases {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := Format(tc.input), caddyFmt(t, tc.input); got != want {
				t.Errorf("Format(%q)\n got   %q\n caddy %q", tc.input, got, want)
			}
		})
	}
}

// The differences are written down with what Caddy did. When Caddy changes, this fails and the list is revisited.
func TestCaddyFmtStillDoesWhatTheDifferencesSay(t *testing.T) {
	for _, tc := range formatDifferences {
		t.Run(tc.name, func(t *testing.T) {
			if got := caddyFmt(t, tc.input); got != tc.caddy {
				t.Errorf("caddy fmt of %q = %q, the list says %q", tc.input, got, tc.caddy)
			}
		})
	}
}

// The expectations of the unit test are what Caddy does, not what this package would like it to do.
func TestFormatCasesAreWhatCaddyDoes(t *testing.T) {
	for _, tc := range formatCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := caddyFmt(t, tc.input); got != tc.want {
				t.Errorf("caddy fmt of %q = %q, the case says %q", tc.input, got, tc.want)
			}
		})
	}
}
