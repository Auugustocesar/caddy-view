# Phase 1 — Read-only UI

Goal: a web UI that observes Caddy and never changes it. It works against the test Caddy.

Everything here is built tests first (see `docs/PLAN.md`, section 11).

## Scope

| In | Out (later phase) |
|---|---|
| `serve` command that runs the UI | Login and sessions (phase 5) |
| Read-only JSON API: status, running config, sites, upstream state | Editing, applying, rollback (phase 3) |
| Live access log tail over SSE | SQLite, history, log search (phase 2) |
| Svelte UI: Dashboard, Config, Logs tabs, embedded in the binary | Metrics charts, req/s, p95 (phase 2) |

The phase 1 dashboard shows Caddy status, the list of sites and the state of their upstreams. Traffic numbers need
metrics history and arrive with phase 2.

## Decisions

- **Loopback only until authentication exists.** `serve` refuses to listen on a non-loopback address in phase 1.
  The Admin API has no authentication and the config can hold secrets, so exposing it before login exists would be
  a regression from "secure by default". To use the UI from another machine, tunnel over SSH or put it behind a
  reverse proxy that authenticates. Real login lands in phase 5.
- **Host header check.** Requests whose `Host` is not a loopback name are rejected with 403. This blocks DNS
  rebinding, where a web page on another origin reads a local service through a rebound hostname.
- **Read-only by construction.** Every API route accepts GET only; any other method is 405. There is no code path
  that calls `/load` in this phase.
- **The API never invents data.** Sites and upstreams are derived from `GET /config/` and
  `GET /reverse_proxy/upstreams`. If upstream state is unavailable, sites are still returned and the response says
  so.
- **Secrets are redacted by default.** `GET /api/config` replaces the value of sensitive keys (`password`, `token`,
  `secret`, `key`, anything ending in `_key`, `_token`, `_secret` or `_password`, and credential headers such as
  `Authorization`) with `[redacted]`. Placeholders like `{env.TOKEN}` or `{file./path}` are references and stay
  visible. `serve --show-secrets` turns the redaction off.
- **Logging.** `log/slog` text logs go to stderr. Failures to read Caddy and rejected hosts are warnings, every
  request is a debug line, and polling `/api/status` while Caddy is down stays out of the warning level so it cannot
  flood the log. `serve --log-level` picks the verbosity.
- **Caddy version is not shown yet.** The Admin API has no version endpoint. Revisit when we know a reliable source.

## API

All responses are JSON, `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`.

| Route | Answer |
|---|---|
| `GET /api/status` | `{"caddy":{"reachable":bool,"endpoint":"...","error":"..."}}`, always 200 |
| `GET /api/config` | The running configuration; 502 if Caddy cannot be read |
| `GET /api/sites` | `{"sites":[{"server","host","upstreams":[{"address","numRequests","fails"}]}],"upstreamsAvailable":bool}`; 502 if the config cannot be read |
| `GET /api/logs/stream` | Server-sent events (`text/event-stream`), one access log line per `data:` event. See "Live log stream" |

A site is one `(server, host)` pair. A route without a host matcher is a catch-all and has an empty `host`.
Upstreams of several routes for the same host are merged, in config order, without duplicates.

### Live log stream

`GET /api/logs/stream[?backlog=N]` follows the JSON access log like `tail -F` and sends each complete line as an SSE
event. The history of old lines belongs to phase 2 (SQLite); this route is only the live view.

- **Which file.** `serve --access-log PATH` wins. Without it, the file is discovered from the running config: the
  `filename` of the first logger (sorted by name) that writes JSON to a file. In Docker the path inside Caddy's
  container differs from the one caddy-view sees, so the flag is how to point at the mounted file. If there is no
  file, the route answers 404; if Caddy cannot be read and no flag was given, 502.
- **Start position.** New lines only. `backlog=N` (0 to 1000, default 0) first sends the last N complete lines. A
  value outside that range is a 400.
- **Wire format.** The first event is the comment `: connected`, sent once the file is being followed, so a client
  knows the stream is live. A `: ping` comment every 15 s keeps proxies from closing an idle stream. Lines are sent
  as they are in the file (trailing `\r\n` removed, empty lines skipped, lines over 1 MiB cut and marked
  ` [truncated]` on a valid UTF-8 boundary). A client must treat them as untrusted text.
- **Limits.** At most 8 streams at once, then 429. A stream ends when the client disconnects.
- **Following the file.** The tailer polls (250 ms) and opens the file only while reading it, so it never keeps
  Caddy's log rotation from renaming the file on Windows. It handles: a file that does not exist yet, partial lines
  (held until the newline arrives), truncation, and rotation (a different file at the same path is read from its
  start). Lines written to the old file in the last poll interval before a rotation can be missed. That is
  acceptable for a live view, and phase 2 ingestion keeps a cursor with the inode and offset to do better.

## Slices

| Slice | Deliverable | Done when |
|---|---|---|
| 1a | Site extraction from the config and the read-only API handler | Unit tests pass for config shapes Caddy produces and for every route, method and error case |
| 1b | `serve` command: flags, loopback enforcement, graceful shutdown | Starts, serves the API, stops cleanly, refuses non-loopback |
| 1c | Log tailer (`internal/logtail`), log file discovery (`caddy.AccessLogFiles`), the SSE route and `serve --access-log` | New lines reach a client; partial lines, truncation and rotation are handled without repeating lines; limits and disconnects are enforced |
| 1d | Svelte UI embedded with `go:embed` | Dashboard, Config and Logs work in a browser against the test Caddy |
| 1e | Check against the test Caddy with `metrics` and JSON access log enabled | `doctor` is all green and the UI shows real sites and live logs |
