// Package netutil holds small networking helpers shared by several packages.
package netutil

import (
	"net"
	"strings"
)

// IsLoopbackHost reports whether host names this machine: "localhost" (any case) or a loopback IP address, with
// or without the brackets of an IPv6 literal. The host must not include a port.
func IsLoopbackHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
