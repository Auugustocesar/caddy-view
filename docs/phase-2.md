# Phase 2 — History: SQLite, log ingestion, metrics

Goal: restarting caddy-view loses nothing. Access logs and metrics are stored, searchable, and summarised in
charts, and the UI gets the design pass that phase 1 deliberately left for the moment there is data to show.

Everything here is built tests first (see `docs/PLAN.md`, section 11).

## Scope

| In | Out (later phase) |
|---|---|
| SQLite storage with versioned migrations, plus an in-memory store for tests and `--no-persist` | Users, sessions, login (phase 5) |
| Access log ingestion from a persistent cursor, surviving restarts, rotation and truncation | Editing Caddy, revisions, rollback (phase 3) |
| `/metrics` scraper with counter deltas, stored as samples | Sites wizard (phase 4) |
| Minute and hour rollups, retention and a database size cap | Per-site charts and rollup drill-down (phase 4) |
| Log history search API and tab | Postgres (out of scope; the store boundary keeps the door open) |
| Dashboard summary (req/s, 5xx rate, p95), first charts, design pass | |

## Decisions

- **SQLite driver: `modernc.org/sqlite`.** It is pure Go, so `go install` keeps working without a C toolchain, and
  the CI matrix (Linux, macOS, Windows) needs nothing extra. Measured in a throwaway program (200,000 inserts in one
  transaction plus a count, WAL): `modernc.org/sqlite` 0.62 s and a 10.0 MB binary; `github.com/ncruces/go-sqlite3`
  (WebAssembly) 0.64 s and 13.1 MB. Speed is a tie, so the smaller binary and the more widely used driver win. A
  cgo driver (`mattn/go-sqlite3`) was rejected because it breaks `go install` on machines without a compiler and
  complicates the Windows and macOS CI. It is the project's first third-party dependency; its transitive modules are
  pinned by `go.sum`, `govulncheck` covers them in CI, and the cache in CI is turned on at the same time.
- **The store is a concrete type, not a framework.** `internal/store` offers `store.SQLite` and `store.Memory`. Each
  consumer (ingestion, API, retention) declares the small interface it needs, as the Go standard practice. A shared
  contract suite (`internal/store/storetest`) runs against both, so the memory store cannot drift from the real one.
- **Timestamps are Unix milliseconds in UTC, durations are microseconds.** Both stores round to that precision, and
  the contract says so.
- **What is stored from a log line.** Time, host, method, path, status, duration, response size and client IP. The
  query string is dropped from the path (`/login?token=…` becomes `/login`) unless `serve --store-query` is given,
  because query strings routinely carry tokens. Request and response headers are never stored. The raw log file stays
  Caddy's.
- **Where the data lives.** `serve --data-dir DIR`, default the user data directory (`$XDG_DATA_HOME` or
  `~/.local/share` on Linux, `~/Library/Application Support` on macOS, `%LocalAppData%` on Windows) plus
  `caddy-view`. The directory is created `0700` and the database file is `0600`. `serve --no-persist` keeps
  everything in memory.
- **Writes and the cursor commit together.** A batch of log entries and the new cursor position go into one
  transaction. A crash or a failing insert either stores both or neither, so a restart never repeats or skips lines.
- **Rotation is detected by file identity and content.** Where the file system gives a stable identity (inode on
  Unix, file index on Windows) a different identity means a new file. Independently, the first bytes of the file
  are fingerprinted, so a file replaced in place is noticed too. A file smaller than the stored offset is a
  truncation. In all three cases the new file is read from its start, and the gap is recorded on the cursor.
- **Counters restart.** A sample lower than the previous raw value means Caddy restarted, and counting starts again
  from the new value (Prometheus semantics). The first sample after a start has no delta.
- **Retention is automatic and configurable.** Raw log entries 14 days, hourly rollups 90 days, a 1 GiB cap on the
  database file. When the cap is exceeded the oldest raw entries are deleted in batches until it fits. The settings
  are flags (`--retain-logs`, `--retain-rollups`, `--max-db-size`).
- **Read-only toward Caddy still holds.** Nothing in this phase writes to Caddy or to its files.

## The store

`internal/store` is created in slice 2a with the tables needed by ingestion, and grows one slice at a time.

| Table | Slice | Contents |
|---|---|---|
| `schema_migrations` (via `PRAGMA user_version`) | 2a | The number of the last applied migration |
| `access_log_entries` | 2a | ts, host, method, path, status, duration, bytes, client IP; indexes on ts, (host, ts), status |
| `ingestion_cursors` | 2a | Per file: file id, fingerprint, offset, skipped line count, gap count |
| `metric_samples` | 2c | Series name, labels, ts, value, last raw counter value |
| `traffic_rollup` | 2d | Minute and hour buckets per host and status class: count, bytes, duration sum and max, latency buckets |

Migrations are numbered SQL files embedded with `go:embed`, applied in order inside a transaction, and recorded in
`PRAGMA user_version`. Opening a database whose version is newer than the binary knows is an error
(`store.ErrSchemaTooNew`), never a silent downgrade. WAL mode, `synchronous=NORMAL`, `busy_timeout`, and foreign
keys are set on every connection.

### Store contract (slice 2a)

- `AppendLog(ctx, LogBatch)` stores the entries and the cursor atomically. An invalid entry (zero time, empty host)
  makes the whole batch fail with `store.ErrInvalidEntry` and changes nothing.
- `QueryLogs(ctx, LogQuery)` returns entries newest first, meaning highest ID first (insertion order). Filters: time range `[From, To)`, exact host, status range,
  and a literal, case-insensitive substring of the path (`%` and `_` are ordinary characters). `Limit` defaults to
  100, is capped at 1000, and a negative value is `store.ErrInvalidQuery`. `BeforeID` pages backwards.
- `LoadCursor(ctx, path)` returns the saved cursor and whether there was one.
- `DeleteLogsBefore(ctx, t)` removes older entries and returns how many.
- Every method honours context cancellation, and the stores are safe for concurrent use.

## Slices

| Slice | Deliverable | Done when |
|---|---|---|
| 2a | `internal/store`: types, migrations, `SQLite` and `Memory`, the contract suite, `serve --data-dir` and `--no-persist` opening the store | The contract suite passes on both stores; data survives a reopen; migration, permission and newer-schema tests pass |
| 2b | `internal/ingest`: cursor-based log reader feeding the store (resumable `logtail` core, batching, query-string stripping, invalid lines counted) | Restarts, rotation, truncation, partial lines and failing batches never repeat or lose a line |
| 2c | `internal/promtext` (a small Prometheus text parser) and the scraper with counter deltas | Deltas, counter resets and a missing `metrics` option are handled; samples are stored |
| 2d | Rollups, retention, size cap | Rollups match the raw entries; retention and the cap delete the right rows |
| 2e | API (`/api/logs`, `/api/traffic`, `/api/summary`), history search tab, dashboard summary and charts, design pass | Go and JavaScript tests pass; the UI shows history after a restart |
| 2f | Check against the test Caddy over a few days of real traffic, including a real log rotation | Numbers in the UI match the log file; nothing repeats or disappears across a restart and a rotation |

## Open questions

- **Charts library.** `docs/PLAN.md` names uPlot (small, fast, no framework). Slice 2e confirms it by checking its
  size and licence before adding the dependency, and keeps the markup accessible (a table alternative for each
  chart).
- **Metrics label cardinality.** Caddy labels most series by `server` and `handler`. Slice 2c decides whether to store
  every label set or only the ones the UI uses, after looking at real `/metrics` output.
