package netutil

import "testing"

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"LOCALHOST", true},
		{"LocalHost", true},
		{"127.0.0.1", true},
		{"127.1.2.3", true},
		{"::1", true},
		{"[::1]", true},
		{"", false},
		{"0.0.0.0", false},
		{"::", false},
		{"[::]", false},
		{"192.168.50.10", false},
		{"example.com", false},
		{"localhost.evil.com", false},
		{"notlocalhost", false},
		{"127.0.0.1.evil.com", false},
		{"localhost:8484", false}, // a host with a port is not a host
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := IsLoopbackHost(tt.host); got != tt.want {
				t.Errorf("IsLoopbackHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}
