package cli

import (
	"testing"
	"time"
)

func TestParseDays(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"14d", 14 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"0d", 0, false},
		{"0", 0, false},
		{"36h", 36 * time.Hour, false},
		{"1h30m", 90 * time.Minute, false},
		{"90m", 90 * time.Minute, false},
		{" 7d ", 7 * 24 * time.Hour, false},
		{"", 0, true},
		{"d", 0, true},
		{"-1d", 0, true},
		{"-5h", 0, true},
		{"1.5d", 0, true},
		{"14D", 0, true},
		{"1d12h", 0, true},
		{"forever", 0, true},
		{"99999999999d", 0, true}, // does not fit in a Duration
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseDays(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseDays(%q) error = %v, want error: %v", tt.in, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("parseDays(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"1073741824", 1 << 30, false},
		{"0", 0, false},
		{"1GiB", 1 << 30, false},
		{"512MiB", 512 << 20, false},
		{"64KiB", 64 << 10, false},
		{"2TiB", 2 << 40, false},
		{"1.5GiB", 3 << 29, false},
		{"1gib", 1 << 30, false},
		{" 10 MiB ", 10 << 20, false},
		{"", 0, true},
		{"GiB", 0, true},
		{"-1", 0, true},
		{"-1MiB", 0, true},
		{"1GB", 0, true}, // ambiguous: decimal or binary
		{"1G", 0, true},
		{"abc", 0, true},
		{"1e3", 0, true},
		{"9999999999TiB", 0, true}, // does not fit in an int64
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseSize(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseSize(%q) error = %v, want error: %v", tt.in, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("parseSize(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
