package ingest

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// accessLine is the part of a Caddy JSON access log line that is stored. Headers are deliberately not listed, so
// they are never decoded into anything.
type accessLine struct {
	TS      *float64 `json:"ts"`
	Request *struct {
		RemoteIP string `json:"remote_ip"`
		ClientIP string `json:"client_ip"`
		Method   string `json:"method"`
		Host     string `json:"host"`
		URI      string `json:"uri"`
	} `json:"request"`
	Duration float64 `json:"duration"`
	Size     int64   `json:"size"`
	Status   int     `json:"status"`
}

// ParseLine turns one Caddy JSON access log line into an entry. The query string is dropped from the path unless
// keepQuery is set. A line that is not an access log entry returns ErrBadLine.
func ParseLine(line []byte, keepQuery bool) (store.LogEntry, error) {
	var l accessLine
	if err := json.Unmarshal(line, &l); err != nil {
		return store.LogEntry{}, fmt.Errorf("%w: %w", ErrBadLine, err)
	}
	switch {
	case l.Request == nil:
		return store.LogEntry{}, fmt.Errorf("%w: no request", ErrBadLine)
	case l.Request.Host == "":
		return store.LogEntry{}, fmt.Errorf("%w: no host", ErrBadLine)
	case l.TS == nil || *l.TS <= 0:
		return store.LogEntry{}, fmt.Errorf("%w: no time", ErrBadLine)
	}

	path := l.Request.URI
	if !keepQuery {
		path, _, _ = strings.Cut(path, "?")
	}
	ip := l.Request.ClientIP
	if ip == "" {
		ip = l.Request.RemoteIP
	}
	return store.LogEntry{
		Time:     time.UnixMilli(int64(math.Round(*l.TS * 1000))).UTC(),
		Host:     l.Request.Host,
		Method:   l.Request.Method,
		Path:     path,
		Status:   l.Status,
		Duration: time.Duration(math.Round(l.Duration*1e6)) * time.Microsecond,
		Bytes:    l.Size,
		ClientIP: ip,
	}, nil
}
