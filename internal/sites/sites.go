// Package sites turns the sites the user manages into a Caddyfile: it normalises and validates them, and generates
// the text of the managed file. It touches no file and no Caddy; what is written is checked here first.
package sites

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// FieldError is one problem with one field of a site, in words for the user.
type FieldError struct {
	// Field is the JSON name of the field: hostname, kind, upstreams, skipTlsVerify, tls, redirectTarget,
	// redirectCode or accessLog.
	Field   string
	Message string
}

// Options says what the generated file may refer to.
type Options struct {
	// AccessLog is the access log file that sites with AccessLog write to. Empty means none is known, which makes
	// AccessLog invalid.
	AccessLog string
}

// Normalize puts a site in its canonical form: a lower-case hostname without brackets, upstreams without the default
// http:// and with a lower-case host, the default TLS mode and the default redirect code. It does not judge the site,
// and shares nothing with its argument.
func Normalize(s store.Site) store.Site {
	s.Hostname = strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s.Hostname)), "["), "]")
	if s.Upstreams != nil {
		ups := make([]string, len(s.Upstreams))
		for i, u := range s.Upstreams {
			ups[i] = normalizeUpstream(u)
		}
		s.Upstreams = ups
	}
	if s.TLS == "" {
		s.TLS = store.TLSAutomatic
	}
	if s.Kind == store.SiteRedirect && s.RedirectCode == 0 {
		s.RedirectCode = 301
	}
	return s
}

func normalizeUpstream(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	return strings.TrimPrefix(u, "http://")
}

// ---- validation

type problems struct {
	list []FieldError
	seen map[string]bool
}

// add keeps the first problem of a field: one is enough to know what to fix, and the next one shows after that.
func (p *problems) add(field, format string, args ...any) {
	if p.seen[field] {
		return
	}
	if p.seen == nil {
		p.seen = map[string]bool{}
	}
	p.seen[field] = true
	p.list = append(p.list, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
}

// Validate returns every problem of a site, or nil. others are the other managed sites, for the uniqueness of the
// hostname; a site with the same ID as s is s itself and is ignored. Each field is reported at most once.
func Validate(s store.Site, others []store.Site, opts Options) []FieldError {
	n := Normalize(s)
	var p problems

	switch {
	case n.Hostname == "":
		p.add("hostname", "Enter a hostname, such as app.example.com.")
	case !validHost(n.Hostname):
		p.add("hostname", "%q is not a hostname: use letters, digits and hyphens in labels separated by dots, or an IP address, with no port, path or wildcard.", n.Hostname)
	default:
		for _, o := range others {
			if o.ID != n.ID && Normalize(o).Hostname == n.Hostname {
				p.add("hostname", "Another managed site already uses %s.", n.Hostname)
			}
		}
	}

	switch n.Kind {
	case store.SiteProxy:
		validateProxy(&p, n)
	case store.SiteRedirect:
		validateRedirect(&p, n)
	default:
		p.add("kind", "Choose what the site does: proxy or redirect.")
	}

	switch n.TLS {
	case store.TLSAutomatic, store.TLSInternal, store.TLSOff:
	default:
		p.add("tls", "Choose how the certificate is obtained: automatic, internal or off.")
	}

	if n.AccessLog && opts.AccessLog == "" {
		p.add("accessLog", "No access log file is known: Caddy has no JSON file log configured, and none was given with --access-log.")
	}
	return p.list
}

func validateProxy(p *problems, n store.Site) {
	if n.RedirectTarget != "" {
		p.add("redirectTarget", "Only a redirect has a target.")
	}
	if n.RedirectCode != 0 {
		p.add("redirectCode", "Only a redirect has a redirect code.")
	}
	if len(n.Upstreams) == 0 {
		p.add("upstreams", "A proxy needs at least one upstream, such as 10.0.0.5:8080.")
	}
	https := false
	seen := map[string]bool{}
	for _, u := range n.Upstreams {
		if why := checkUpstream(u); why != "" {
			p.add("upstreams", "%q is not a usable upstream: %s.", u, why)
			continue
		}
		if seen[u] {
			p.add("upstreams", "The upstream %s is listed twice.", u)
		}
		seen[u] = true
		https = https || strings.HasPrefix(u, "https://")
	}
	if n.SkipTLSVerify && !https {
		p.add("skipTlsVerify", "Skipping TLS verification only applies to an https:// upstream.")
	}
}

func validateRedirect(p *problems, n store.Site) {
	if len(n.Upstreams) > 0 {
		p.add("upstreams", "A redirect has no upstreams.")
	}
	if n.SkipTLSVerify {
		p.add("skipTlsVerify", "A redirect has no upstream to verify.")
	}
	if why := checkRedirectTarget(n.RedirectTarget); why != "" {
		p.add("redirectTarget", "%s", why)
	}
	switch n.RedirectCode {
	case 301, 302, 307, 308:
	default:
		p.add("redirectCode", "The redirect code must be 301, 302, 307 or 308, not %d.", n.RedirectCode)
	}
}

// validHost reports whether s is a DNS name made of valid labels, or an IP address. s is already lower case.
func validHost(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

// checkUpstream returns why an upstream, already normalised, cannot be used, or "" when it can.
func checkUpstream(u string) string {
	rest := strings.TrimPrefix(u, "https://")
	switch {
	case rest == "":
		return "it is empty"
	case strings.Contains(rest, "://"):
		return "only http:// and https:// are supported"
	case strings.ContainsAny(rest, " \t\r\n{}?#@/\\\"'`"):
		return "it must be a host with an optional port, with no path, query, user or placeholder"
	}
	host, port := rest, ""
	switch {
	case strings.HasPrefix(rest, "["):
		end := strings.Index(rest, "]")
		if end < 0 {
			return "the bracket is not closed"
		}
		host, port = rest[1:end], rest[end+1:]
		if port != "" && !strings.HasPrefix(port, ":") {
			return "after the bracket only a port may follow"
		}
		port = strings.TrimPrefix(port, ":")
		if addr, err := netip.ParseAddr(host); err != nil || !addr.Is6() {
			return "inside brackets there must be an IPv6 address"
		}
		if strings.HasSuffix(rest, ":") {
			return "the port is empty"
		}
	case strings.Count(rest, ":") > 1:
		return "an IPv6 address needs brackets, such as [::1]:8080"
	case strings.Contains(rest, ":"):
		host, port, _ = strings.Cut(rest, ":")
		if port == "" {
			return "the port is empty"
		}
	}
	if !validHost(host) {
		return "the host is not a hostname or an IP address"
	}
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return "the port must be a number from 1 to 65535"
		}
	}
	return ""
}

// checkRedirectTarget returns why a redirect target cannot be used, or "" when it can. The one placeholder allowed is
// {uri}, which keeps the path and query of the request.
func checkRedirectTarget(t string) string {
	lower := strings.ToLower(t)
	switch {
	case t == "":
		return "Enter where the redirect goes, such as https://new.example.com{uri}."
	case !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://"):
		return "The target must be a full URL that starts with http:// or https://."
	case strings.ContainsAny(t, " \t\r\n\"'`\\"):
		return "The target must not contain spaces, quotes or backslashes."
	case strings.ContainsAny(strings.ReplaceAll(t, "{uri}", ""), "{}"):
		return "The only placeholder a target may use is {uri}."
	}
	host := t[strings.Index(t, "://")+3:]
	if i := strings.IndexAny(host, "/?#{"); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return "The target has no host."
	}
	return ""
}

// ---- generation

const (
	fileHeader    = "# Managed by caddy-view. Changes made here are overwritten: edit the sites in caddy-view."
	accessLogName = "caddy-view-access-log"
)

// Block is the part of the generated text that belongs to a site: its lines, counting from 1, both ends included.
type Block struct {
	SiteID     string
	Start, End int
}

// Output is the generated managed file.
type Output struct {
	Text   string
	Blocks []Block
}

// SiteAt returns the site whose block holds the line, and false for a line that belongs to none (the header, the
// snippet, the blank lines between blocks).
func (o Output) SiteAt(line int) (string, bool) {
	for _, b := range o.Blocks {
		if line >= b.Start && line <= b.End {
			return b.SiteID, true
		}
	}
	return "", false
}

// Generate writes the managed file for the sites that are enabled, sorted by hostname. The sites are normalised first
// and are expected to be valid.
func Generate(all []store.Site, opts Options) Output {
	var enabled []store.Site
	logging := false
	for _, s := range all {
		if s.Enabled {
			n := Normalize(s)
			enabled = append(enabled, n)
			logging = logging || (n.AccessLog && opts.AccessLog != "")
		}
	}
	slices.SortFunc(enabled, func(a, b store.Site) int { return strings.Compare(a.Hostname, b.Hostname) })

	lines := []string{fileHeader}
	if logging {
		lines = append(lines, "", "("+accessLogName+") {", "\tlog {", "\t\toutput file "+opts.AccessLog, "\t\tformat json", "\t}", "}")
	}
	var blocks []Block
	for _, s := range enabled {
		lines = append(lines, "")
		start := len(lines) + 1
		lines = append(lines, siteBlock(s, opts)...)
		blocks = append(blocks, Block{SiteID: s.ID, Start: start, End: len(lines)})
	}
	return Output{Text: strings.Join(lines, "\n") + "\n", Blocks: blocks}
}

func siteBlock(s store.Site, opts Options) []string {
	host := s.Hostname
	if strings.Contains(host, ":") { // an IPv6 address is written in brackets
		host = "[" + host + "]"
	}
	if s.TLS == store.TLSOff {
		host = "http://" + host
	}
	lines := []string{host + " {"}
	if s.AccessLog && opts.AccessLog != "" {
		lines = append(lines, "\timport "+accessLogName)
	}
	if s.TLS == store.TLSInternal {
		lines = append(lines, "\ttls internal")
	}
	switch {
	case s.Kind == store.SiteRedirect:
		lines = append(lines, fmt.Sprintf("\tredir %s %d", s.RedirectTarget, s.RedirectCode))
	case s.SkipTLSVerify:
		lines = append(lines, "\treverse_proxy "+strings.Join(s.Upstreams, " ")+" {", "\t\ttransport http {", "\t\t\ttls_insecure_skip_verify", "\t\t}", "\t}")
	default:
		lines = append(lines, "\treverse_proxy "+strings.Join(s.Upstreams, " "))
	}
	return append(lines, "}")
}

// NewID returns a new site ID: 32 random hexadecimal characters.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("the system has no source of randomness: " + err.Error()) // rand.Read does not fail on supported systems
	}
	return hex.EncodeToString(b[:])
}
