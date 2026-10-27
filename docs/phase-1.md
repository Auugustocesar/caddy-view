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
| `GET /api/logs/stream` | Server-sent events, one access log line per event (slice 1c) |

A site is one `(server, host)` pair. A route without a host matcher is a catch-all and has an empty `host`.
Upstreams of several routes for the same host are merged, in config order, without duplicates.

## Slices

| Slice | Deliverable | Done when |
|---|---|---|
| 1a | Site extraction from the config and the read-only API handler | Unit tests pass for config shapes Caddy produces and for every route, method and error case |
| 1b | `serve` command: flags, loopback enforcement, graceful shutdown | Starts, serves the API, stops cleanly, refuses non-loopback |
| 1c | Log tail with rotation handling and the SSE route | New lines reach a client; rotation and truncation do not lose or repeat lines |
| 1d | Svelte UI embedded with `go:embed` | Dashboard, Config and Logs work in a browser against the test Caddy |
| 1e | Check against the test Caddy with `metrics` and JSON access log enabled | `doctor` is all green and the UI shows real sites and live logs |
