// Package upstreamcheck finds out whether the upstreams of a reverse proxy answer.
//
// Caddy itself does not know: without health checks in its configuration it reports every upstream as healthy until a
// visitor hits the error. This package asks each upstream directly. Any HTTP answer counts as answering, whatever the
// status, because the check asks "is it there", not "is it well".
package upstreamcheck

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// State is what a check found out.
type State string

// The states of an upstream.
const (
	// Answering means the upstream answered an HTTP request, with any status.
	Answering State = "answering"
	// Down means the address was reached but nothing answered: the connection was refused or dropped, or no answer came in time.
	Down State = "down"
	// Unknown means the check cannot tell: the address cannot be checked, or this machine cannot reach the network it is on.
	// It is never reported as Down, because a wrong "down" is worse than no answer.
	Unknown State = "unknown"
)

// DefaultTimeout is how long an upstream has to answer.
const DefaultTimeout = 2 * time.Second

// userAgent names the check, so the owner of the upstream can tell it apart from visitors in their own logs.
const userAgent = "caddy-view-upstream-check"

// Result is the outcome of one check.
type Result struct {
	State State
	// Status is the HTTP status of the answer, zero unless State is Answering. It is what a health check of Caddy's own
	// would judge, and by default Caddy counts only a 2xx as healthy.
	Status int
	// Latency is the time until the answer. It is zero unless State is Answering.
	Latency time.Duration
	// CheckedAt is when the check finished.
	CheckedAt time.Time
}

// Checker asks upstreams whether they answer. The zero value is ready to use.
type Checker struct {
	// Timeout is how long an upstream has to answer. Zero means DefaultTimeout.
	Timeout time.Duration
	// Dial opens the connections. Nil means a plain net.Dialer. Tests use it to simulate network failures.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// Now gives the time recorded in Result.CheckedAt. Nil means time.Now.
	Now func() time.Time
}

// Check sends one GET / to the upstream. It follows no redirects and sends no credentials. An address with an https://
// scheme is asked over TLS without verifying the certificate; any other is asked over plain HTTP.
func (c *Checker) Check(ctx context.Context, address string) Result {
	target, ok := checkURL(address)
	if !ok {
		return c.result(Unknown, 0, 0)
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	dial := c.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:       dial,
			DisableKeepAlives: true,
			// The check asks whether the upstream is there, not whether its certificate is good.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // see above
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return c.result(Unknown, 0, 0)
	}
	req.Header.Set("User-Agent", userAgent)

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return c.result(Unknown, 0, 0) // we were stopped; that says nothing about the upstream
		}
		return c.result(classify(err), 0, 0)
	}
	latency := time.Since(start)
	_ = resp.Body.Close() // nothing to do with the body; the answer is what counts
	return c.result(Answering, latency, resp.StatusCode)
}

func (c *Checker) result(state State, latency time.Duration, status int) Result {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	return Result{State: state, Status: status, Latency: latency, CheckedAt: now()}
}

// checkURL turns the address of an upstream, as Caddy writes it in its configuration, into the URL to ask. It reports
// false for what cannot be checked: unix sockets, placeholders, port ranges and addresses without a port.
func checkURL(address string) (string, bool) {
	scheme := "http"
	rest := address
	switch {
	case strings.HasPrefix(rest, "https://"):
		scheme, rest = "https", strings.TrimPrefix(rest, "https://")
	case strings.HasPrefix(rest, "http://"):
		rest = strings.TrimPrefix(rest, "http://")
	}
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" || strings.ContainsAny(rest, "{}/") {
		return "", false
	}
	host, port, err := net.SplitHostPort(rest)
	if err != nil || host == "" {
		return "", false
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", false
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port), Path: "/"}
	return u.String(), true
}

// classify says what a failed request means. Only a failure that shows the address was reached, and nobody answered, is
// Down. Everything else, such as an unreachable network or a name that does not resolve, is Unknown.
func classify(err error) State {
	var netErr net.Error
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr): // even a lookup that timed out: the upstream was never reached
		return Unknown
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return Unknown
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return Down
	case errors.As(err, &netErr) && netErr.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return Down
	default:
		return Unknown
	}
}
