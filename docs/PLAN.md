# caddy-view — Project Plan

A lightweight web UI that runs next to [Caddy](https://caddyserver.com): see status, metrics and logs, edit the
Caddyfile safely, and manage reverse proxies.

Status: phase 0 complete (Admin API client, `doctor` command and CI); phase 1 is next. This document is the source
of truth for scope and design; update it when a decision changes.

## 1. Scope

**Goal:** a small, self-hosted UI that sits beside Caddy and lets you observe it and change its configuration
without ever leaving it in a broken state.

**Non-goals (v1):**
- Postgres or any external database
- Multi-instance management
- Plugin management
- Replacing Grafana / Loki

**Principles:**
- Single Go binary, SQLite by default, frontend embedded with `go:embed`.
- Caddy is the source of truth. The UI never leaves Caddy with an invalid config.
- Secure by default: bind to localhost, login required, editing disabled until enabled explicitly.
- Tests come first (see section 11).

## 2. Architecture

```
 Browser ──HTTP/SSE──► caddy-view ──unix socket──► Caddy Admin API
                          │  │                      (/config /adapt /load /metrics)
                          │  └── reads ──► access.log (JSON, shared mount)
                          ├──── reads/writes ──► Caddyfile + conf.d/caddy-view-sites.caddy
                          └──── SQLite (/data/caddy-view.db)
```

Internal packages:

| Package | Responsibility |
|---|---|
| `internal/caddy` | Admin API client (unix socket or TCP) |
| `internal/ingest` | Access log reader with a persistent cursor |
| `internal/metrics` | `/metrics` scraper, counter deltas |
| `internal/store` | SQLite storage behind a `Store` interface (`sqlite`, `memory` for tests) |
| `internal/configmgr` | Revisions, validate/apply/rollback state machine |
| `internal/auth` | Users, sessions, rate limiting |
| `internal/server` | HTTP API, SSE |
| `internal/web` | Embedded SPA handler |
| `cmd/caddy-view` | CLI: `serve`, `doctor`, `reset-password` |
| `web/` | Svelte + Vite frontend (built into `internal/web/dist`) |

## 3. How it talks to Caddy

| Need | Source |
|---|---|
| Running config (JSON) | `GET /config/` |
| Validate a Caddyfile | `POST /adapt` (`Content-Type: text/caddyfile`) |
| Apply without downtime | `POST /load` (graceful reload) |
| Upstream health | `GET /reverse_proxy/upstreams` |
| Metrics | `GET /metrics` (Prometheus format, requires `metrics` in global options) |
| Logs | JSON access log file written by Caddy (`log { output file ...; format json }`) |

Notes:
- The Admin API has no authentication. caddy-view prefers a **unix socket**
  (`admin unix//run/caddy/admin.sock`) shared by volume, so no port is exposed.
- The Admin API works with JSON and cannot turn JSON back into a Caddyfile. Editing the Caddyfile therefore
  requires read/write access to the file on disk.
- "Restart" means graceful reload. A real restart (systemd / Docker API) is an optional advanced feature.
- The Caddyfile is not YAML. Validation is done through `/adapt`.

### One-time Caddy setup

```caddyfile
{
	admin unix//run/caddy/admin.sock
	metrics
}

import conf.d/*.caddy   # caddy-view writes only to conf.d/caddy-view-sites.caddy
```

plus a `log` snippet writing JSON access logs to a shared path. `caddy-view doctor` checks all of this and
tells the user what is missing.

## 4. Data model (SQLite, WAL, versioned migrations)

| Table | Contents |
|---|---|
| `users`, `sessions` | Argon2 password hash; sessions store only a token hash, `expires_at`, `last_seen_at` |
| `sites` | User intent: hostname (unique), upstreams, tls, enabled, options (JSON). Allows regenerating `caddy-view-sites.caddy` |
| `config_revisions` | Unique number, content, sha256, trigger (`manual-edit`, `site-change`, `rollback`), status, error |
| `access_log_entries` | ts, host, method, path, status, duration, bytes (indexes on time, host, status) |
| `ingestion_cursors` | Per file: device, inode, fingerprint, offset, skipped lines, gaps |
| `traffic_rollup` | Minute and hour buckets per host and status class: count, bytes, duration sum/max, latency buckets |
| `metric_samples` | Scraped series (name, ts, value) plus last raw counter value for delta computation |

Revision status: `pending → loading → applied | failed | recovery-required`.

Retention (all configurable, cleanup runs automatically): raw logs 14 days, database size cap 1 GB, hourly
rollups 90 days.

SQLite is the default so restarting the UI never loses history. `--no-persist` selects the in-memory store
(mostly for tests and demos). The in-memory ring buffer is only a cache for the live tail.

## 5. Config backup mode

The user chooses how much the UI keeps, via `--backup-mode` (or the config file):

| Mode | Behavior |
|---|---|
| `full` (default) | Every apply stores a revision of the whole Caddyfile plus the sites snapshot; rollback restores both |
| `sites-only` | Only the UI-managed `sites` and their generated file are versioned; the hand-written Caddyfile is never copied or restored by the UI |

In both modes `conf.d/caddy-view-sites.caddy` can be regenerated from the `sites` table.

## 6. Apply flow

1. Create a revision (`pending`) with the candidate content.
2. Validate with `POST /adapt`. On failure stop and show the error with line information.
3. Back up the current file, write the new one atomically (temp file + rename).
4. Mark `loading` and call `POST /load`.
5. Success: `applied`. Failure: restore the previous file, reload it, mark `failed`. If the restore also fails:
   `recovery-required`.
6. On startup, reconcile: compare the on-disk hash with the last `applied` revision and warn if they differ
   (someone edited the file outside the UI).

Before saving, check `mtime` and hash to detect concurrent edits.

## 7. Ingestion

- **Logs:** read Caddy's JSON log from the stored cursor, in batches (about 1 s, one transaction). If the inode
  changed the file was rotated: read the new file from the start. Invalid lines are counted, never fatal.
- **Metrics:** scrape every 10 s. Deltas are computed against the last stored value; a smaller value means Caddy
  restarted, so counting restarts from zero (Prometheus semantics). The UI warns if `metrics` is not enabled.
- **Live tail:** in-memory ring buffer, streamed to the browser over SSE.

## 8. UI

Floating header with tabs.

| Tab | Contents |
|---|---|
| Dashboard | Status, version, sites with upstream health, req/s, 5xx rate, p95, certificates near expiry |
| Metrics | Historical charts by period and host |
| Caddyfile | Editor with live validation, diff, apply, history and rollback |
| Sites | Reverse proxy wizard (writes `caddy-view-sites.caddy`) |
| Logs | Live tail and historical search with filters |
| Config | Running JSON, UI settings, database usage |

Stack: Svelte + Vite (Tailwind later, if the UI needs it), CodeMirror 6 for the editor (simple Caddyfile language mode), uPlot for charts.

## 9. Security

- Listen on `127.0.0.1` by default; LAN requires explicit `--listen`.
- Initial password generated on first start and printed once in the log; `caddy-view reset-password` to reset.
- httpOnly + SameSite cookies, CSRF protection, login rate limit. Clear warning on plain HTTP
  (`--allow-insecure-http`).
- Read-only by default; editing requires `--allow-edit`.
- Docker: non-root user, least-privilege socket permissions, port 2019 never published.

## 10. Distribution

- Binaries for linux and macOS, amd64 and arm64, via GoReleaser, plus an example systemd unit.
- Small multi-arch Docker image and `docker-compose.example.yml` (Caddyfile, `conf.d`, logs, socket and `/data`
  volumes).
- Test environment: a Caddy LXC in the author's homelab.

## 11. Development workflow: tests first

Every feature, fix or improvement starts with tests. The tests are written from the requirement, **before** the
implementation exists, so they describe the intended behavior instead of the behavior the code happens to have.

1. Write the tests for the requirement and run them. Confirm they fail for the right reason.
2. Write the minimal code that makes them pass.
3. Refactor with the tests green.

Test layers:
- **Unit:** log parser, counter deltas, rotation detection, revision state machine, Caddyfile generation.
- **Integration:** real Caddy in a container covering `/adapt`, `/load`, failure and rollback.
- **Frontend:** component tests plus a small end-to-end smoke test.

Language: all code, comments, docs, commit messages and UI text are in English.

## 12. Phases and acceptance criteria

| Phase | Deliverable | Done when |
|---|---|---|
| 0 | Repo, CI, structure, `doctor` | CI green; `doctor` reports what is missing in Caddy |
| 1 | Read-only: Dashboard, Config, live Logs | Works against the test Caddy |
| 2 | SQLite, log ingestion with cursor, metrics scraper | Restarting the UI loses no logs or metrics |
| 3 | Caddyfile edit, validate, apply, rollback | An invalid config never leaves Caddy broken |
| 4 | Sites wizard, rollup charts, and the whole front end for history (log search tab, dashboard summary, design pass) | Creating a reverse proxy from the UI works |
| 5 | Full login, release, Docker, docs | Fresh-machine install takes minutes |

MVP is phases 0 to 3. Phase 0 is complete: CI runs gofmt, vet and race tests on Linux, macOS and Windows, and
`doctor` was verified against a real Caddy 2.11.

## 13. Risks

- Caddy without `metrics` or JSON logs: `doctor` detects it and the UI guides the user.
- Database growth: size cap by default, usage shown in the UI.
- UID and socket permissions in Docker: document and test in the compose example.
- Scope creep: hold the line at the MVP.

## 14. Decisions

| Decision | Choice |
|---|---|
| Name | `caddy-view` |
| Language | Go |
| License | MIT |
| Frontend | Svelte 5, plain JavaScript and CSS in phase 1 (no Tailwind yet); built assets committed so `go install` works |
| Default storage | SQLite via `modernc.org/sqlite`, pure Go (memory store only for tests / `--no-persist`); see `docs/phase-2.md` |
| Postgres | Out of scope; the `Store` interface keeps the door open |
| Config backup | User-selectable: `full` or `sites-only` (section 5) |
