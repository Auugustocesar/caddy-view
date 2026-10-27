package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Auugustocesar/caddy-view/internal/caddy"
	"github.com/Auugustocesar/caddy-view/internal/logtail"
)

const (
	defaultMaxStreams        = 8
	defaultHeartbeatInterval = 15 * time.Second
	maxBacklog               = 1000
)

// logStream follows the access log and sends each new line as a server-sent event.
func (a *api) logStream(w http.ResponseWriter, r *http.Request) {
	backlog := 0
	if q := r.URL.Query(); q.Has("backlog") {
		n, err := strconv.Atoi(q.Get("backlog"))
		if err != nil || n < 0 || n > maxBacklog {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("backlog must be a number from 0 to %d", maxBacklog))
			return
		}
		backlog = n
	}

	select {
	case a.streams <- struct{}{}:
		defer func() { <-a.streams }()
	default:
		writeError(w, http.StatusTooManyRequests, "too many open log streams")
		return
	}

	path, status, err := a.accessLogPath(r.Context())
	if err != nil {
		if status == http.StatusBadGateway {
			a.badGateway(w, r, err)
			return
		}
		writeError(w, status, err.Error())
		return
	}
	// The start position is fixed here, before the client is told it is connected, so a line written right after
	// the announcement is never missed.
	tail, err := logtail.New(path, logtail.Options{Backlog: backlog, PollInterval: a.opts.LogPollInterval})
	if err != nil {
		a.log.Warn("cannot follow the access log", "path", path, "error", err)
		writeError(w, http.StatusInternalServerError, "cannot follow the access log")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil || rc.Flush() != nil {
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	lines := make(chan string)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(lines)
		tail.Run(ctx, func(line string) {
			select {
			case lines <- line:
			case <-ctx.Done():
			}
		})
	}()
	// Wait for the tailer to stop, so the slot is only freed once nothing is reading the file any more.
	defer func() { cancel(); <-done }()

	interval := a.opts.HeartbeatInterval
	if interval <= 0 {
		interval = defaultHeartbeatInterval
	}
	heartbeat := time.NewTicker(interval)
	defer heartbeat.Stop()
	for {
		var event string
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			event = "data: " + line + "\n\n"
		case <-heartbeat.C:
			event = ": ping\n\n"
		}
		if _, err := fmt.Fprint(w, event); err != nil || rc.Flush() != nil {
			return
		}
	}
}

// accessLogPath picks the file to follow: the configured path, else the first JSON file logger of the running
// Caddy configuration. When it fails it also returns the HTTP status to answer with.
func (a *api) accessLogPath(ctx context.Context) (string, int, error) {
	if a.opts.AccessLogPath != "" {
		return a.opts.AccessLogPath, http.StatusOK, nil
	}
	cfg, err := a.caddy.Config(ctx)
	if err != nil {
		return "", http.StatusBadGateway, err
	}
	files := caddy.AccessLogFiles(cfg)
	if len(files) == 0 {
		return "", http.StatusNotFound, errNoAccessLog
	}
	return files[0], http.StatusOK, nil
}
