// Package caddy is a client for the Caddy Admin API.
package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	requestTimeout = 30 * time.Second
	maxBodyBytes   = 32 << 20
)

// ErrUnreachable means the Admin API could not be contacted at all (connection refused, missing socket).
var ErrUnreachable = errors.New("caddy admin API unreachable")

// APIError is a non-2xx answer from the Admin API.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("caddy admin API returned %d: %s", e.Status, e.Message)
}

// AdaptError means Caddy rejected a Caddyfile. Line is 0 when Caddy did not report one.
type AdaptError struct {
	Message string
	Line    int
}

func (e *AdaptError) Error() string { return e.Message }

// Warning is a non-fatal note produced while adapting a Caddyfile.
type Warning struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// AdaptResult is the JSON config produced from a Caddyfile.
type AdaptResult struct {
	Config   map[string]any
	Warnings []Warning
}

// Upstream is the live state of a reverse proxy upstream.
type Upstream struct {
	Address     string `json:"address"`
	NumRequests int    `json:"num_requests"`
	Fails       int    `json:"fails"`
}

// Client talks to one Caddy Admin API, over a unix socket or TCP.
type Client struct {
	endpoint string
	base     string
	http     *http.Client
}

// New builds a client from an address: unix:///path, unix//path (Caddy syntax), http://host:port or host:port.
func New(addr string) (*Client, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, errors.New("caddy admin address is empty")
	}

	if sock, ok := unixPath(addr); ok {
		if sock == "" {
			return nil, fmt.Errorf("unix address %q has no socket path", addr)
		}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		}
		return &Client{
			endpoint: "unix://" + sock,
			base:     "http://unix",
			http:     &http.Client{Transport: transport, Timeout: requestTimeout},
		}, nil
	}

	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid caddy admin address: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q in caddy admin address", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("caddy admin address %q has no host", addr)
	}
	base := u.Scheme + "://" + u.Host
	return &Client{endpoint: base, base: base, http: &http.Client{Timeout: requestTimeout}}, nil
}

// unixPath extracts the socket path from "unix://<path>" or Caddy's "unix/<path>" syntax.
func unixPath(addr string) (string, bool) {
	if rest, ok := strings.CutPrefix(addr, "unix://"); ok {
		return rest, true
	}
	if rest, ok := strings.CutPrefix(addr, "unix/"); ok {
		return rest, true
	}
	return "", false
}

// Endpoint returns the normalized address the client talks to.
func (c *Client) Endpoint() string { return c.endpoint }

// Config returns the running configuration (GET /config/).
func (c *Client) Config(ctx context.Context) (map[string]any, error) {
	body, err := c.do(ctx, http.MethodGet, "/config/", "", nil)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	return cfg, nil
}

var caddyfileLine = regexp.MustCompile(`Caddyfile:(\d+)`)

// Adapt converts a Caddyfile to JSON without applying it (POST /adapt).
func (c *Client) Adapt(ctx context.Context, caddyfile string) (*AdaptResult, error) {
	body, err := c.do(ctx, http.MethodPost, "/adapt", "text/caddyfile", []byte(caddyfile))
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
			adaptErr := &AdaptError{Message: apiErr.Message}
			if m := caddyfileLine.FindStringSubmatch(apiErr.Message); m != nil {
				adaptErr.Line, _ = strconv.Atoi(m[1])
			}
			return nil, adaptErr
		}
		return nil, err
	}
	var out struct {
		Result   map[string]any `json:"result"`
		Warnings []Warning      `json:"warnings"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode adapt result: %w", err)
	}
	return &AdaptResult{Config: out.Result, Warnings: out.Warnings}, nil
}

// Load applies a Caddyfile with a graceful reload (POST /load).
func (c *Client) Load(ctx context.Context, caddyfile string) error {
	_, err := c.do(ctx, http.MethodPost, "/load", "text/caddyfile", []byte(caddyfile))
	return err
}

// Metrics returns the raw Prometheus text (GET /metrics).
func (c *Client) Metrics(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodGet, "/metrics", "", nil)
}

// Upstreams returns the reverse proxy upstream states (GET /reverse_proxy/upstreams).
func (c *Client) Upstreams(ctx context.Context) ([]Upstream, error) {
	body, err := c.do(ctx, http.MethodGet, "/reverse_proxy/upstreams", "", nil)
	if err != nil {
		return nil, err
	}
	var ups []Upstream
	if err := json.Unmarshal(body, &ups); err != nil {
		return nil, fmt.Errorf("decode upstreams: %w", err)
	}
	return ups, nil
}

// do sends a request and returns the body of a 2xx answer. Other statuses become *APIError, and a failure to
// connect becomes ErrUnreachable.
func (c *Client) do(ctx context.Context, method, path, contentType string, payload []byte) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		var opErr *net.OpError
		if ctx.Err() == nil && errors.As(err, &opErr) && opErr.Op == "dial" {
			return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
		}
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &APIError{Status: resp.StatusCode, Message: errorMessage(resp, data)}
	}
	return data, nil
}

// errorMessage extracts Caddy's {"error": "..."} message, falling back to the raw body or the status text.
func errorMessage(resp *http.Response, data []byte) string {
	var parsed struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &parsed) == nil && parsed.Error != "" {
		return parsed.Error
	}
	if msg := strings.TrimSpace(string(data)); msg != "" {
		return msg
	}
	return http.StatusText(resp.StatusCode)
}
