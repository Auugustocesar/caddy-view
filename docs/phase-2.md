# Phase 2 — History: SQLite, log ingestion, metrics

Goal: restarting caddy-view loses nothing. Access logs and metrics are stored, searchable, and summarised in
through an API. The interface for all of it (history tab, dashboard summary, charts and the design pass) is built
once, in phase 4, so it is not written twice.

Everything here is built tests first (see `docs/PLAN.md`, section 11).

## Scope

| In | Out (later phase) |
|---|---|
| SQLite storage with versioned migrations, plus an in-memory store for tests and `--no-persist` | Users, sessions, login (phase 5) |
| Access log ingestion from a persistent cursor, surviving restarts, rotation and truncation | Editing Caddy, revisions, rollback (phase 3) |
| `/metrics` scraper with counter deltas, stored as samples | Sites wizard (phase 4) |
| Minute and hour rollups, retention and a database size cap | Per-site charts and rollup drill-down (phase 4) |
| Log history search API and dashboard summary API (req/s, 5xx rate, p95) | History search tab, dashboard tiles and charts, design pass (phase 4) |
| | Postgres (out of scope; the store boundary keeps the door open) |

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
  from the new value (Prometheus semantics). Details are in the metrics section.
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

## Ingestion (slice 2b)

`internal/ingest` reads one JSON access log file into the store. It does not reuse `logtail`: that package follows
a file from "now" and keeps no position, while ingestion must resume from a stored offset.

- **One poll, `Once`.** Load the cursor, stat the file, decide whether it is the same file, read from the offset up
  to the last complete line, parse, and store in batches (default 500 entries). Each batch carries the cursor that
  follows its last line, so the store commits entries and position together. A failing batch stops the poll with the
  store's error; the cursor stays where the last stored batch ended and the next poll continues from there.
  `Run` repeats `Once` every poll interval (default 1 s), logs failures and keeps going.
- **First start.** With no cursor the file is read from its start: the point of the history is to include what
  Caddy already logged.
- **Same file or not.** The cursor keeps a file ID (inode, or the Windows file index) and a fingerprint, which is the
  length and SHA-256 of the first `min(offset, 256)` bytes read. The file is new when the ID differs, when the
  fingerprint of the same prefix differs (a file replaced in place), or when it is smaller than the offset
  (truncation). A new file is read from its start and `Gaps` grows by one, because lines written between the last
  poll and the rotation may be gone. Growth alone never counts as rotation, even for a tiny file.
- **Lines.** Only complete lines (ending in `
`) are consumed. A line that is not JSON, has no request, host or
  time, or is longer than `MaxLineBytes` (default 1 MiB) is skipped and counted in `SkippedLines`.
- **What is kept.** Time (milliseconds), `request.host`, `method`, the path of `request.uri` (query string dropped
  unless `KeepQuery`), `status`, `duration`, `size`, and `request.client_ip` (or `remote_ip`). Headers are never read
  into the entry.
- **Wiring.** `serve` starts one ingester, into whichever store is open (with `--no-persist` it fills the memory
  store, which the UI reads the same way), for the log file found at startup: the `--access-log` flag, otherwise the
  first file from the Caddy configuration. Without a log file nothing is ingested and the doctor already says why.

## Metrics (slice 2c)

`internal/promtext` parses the Prometheus text format; `internal/scrape` turns Caddy's `/metrics` into stored samples.
Looking at the real output of the test Caddy settled the open question about label cardinality: 733 lines, of
which 554 are `caddy_http_*` histogram series split by `server`, `handler`, `code` and `method`. Storing every
label set would be thousands of rows per scrape for charts that need a few lines, so a fixed set is kept and
aggregated while scraping.

| Series stored | Kind | Labels kept |
|---|---|---|
| `caddy_http_requests_total` | counter | `server` |
| `caddy_http_request_duration_seconds_count`, `..._sum` | counter | `server`, `code` as a class (`1xx` to `5xx`, else `other`) |
| `caddy_http_requests_in_flight` | gauge | `server` |
| `caddy_reverse_proxy_upstreams_healthy` | gauge | `upstream` |
| `caddy_config_last_reload_successful` | gauge | none |

- **Counters are stored as increases.** A `MetricSample` has `Value` (the increase since the previous sample, or the
  reading for a gauge) and `Raw` (the counter itself). Counters are summed over the labels that are dropped before
  the increase is computed. The increase is `raw - previous`, and when the raw value went down, Caddy restarted and
  the increase is the new raw value (Prometheus semantics).
- **Baselines.** The very first scrape against an empty store has nothing to compare with, so counters get an increase
  of 0 and only record `Raw`. After that a series seen for the first time (a new server, the first 5xx) counts from
  zero: all it has counted is new. After a restart of caddy-view the previous raw values come from the store
  (`LastMetrics`), so counting continues instead of starting over.
- **A failed write moves nothing.** The previous raw values only advance after the samples are stored, so a lost
  scrape is not forgotten: the next increase covers both intervals.
- **Not stored:** NaN and infinite values, and every series outside the table. Caddy without the `metrics` option
  yields no `caddy_*` lines; that is not an error, the doctor already says why.
- **Store.** `AppendMetrics` (atomic), `QueryMetrics` (by name, optional exact label string, `[From, To)`, oldest
  first, at most `MaxMetricLimit` = 10,000 samples) and `LastMetrics` (newest by time per series). The SQLite table
  is `metric_samples` (migration 2) with an index on name, labels and time. Labels are one canonical string from
  `promtext.FormatLabels`, which the store treats as opaque.
- **Wiring.** `serve` runs one scraper every 15 s against the same Admin API client, into the same store.

## Rollups, retention and the size cap (slice 2d)

- **Rollups are written with the entries.** `AppendLog` also adds each entry to a minute bucket and an hour bucket,
  in the same transaction as the entries and the cursor. A rollup therefore always equals the raw entries it was
  made from: a restart, a rotation or a failing batch cannot make them drift, and nothing has to be recomputed.
  A bucket is keyed by resolution, start time (UTC, aligned to the minute or the hour), host and status class
  (`1` to `5` for 1xx to 5xx, `0` for anything else). It holds the request count, bytes, the sum and the maximum of
  the duration, and a latency histogram.
- **Latency histogram.** Fixed upper bounds of 5, 10, 25, 50, 100, 250, 500 ms and 1, 2.5, 5, 10 s plus one open
  bucket, 12 counters in all. Counters add up across buckets, hosts and classes, so a p95 over any range is the
  upper bound of the bucket that holds the 95th percentile (`store.LatencyQuantile`); for the open bucket the answer
  is the last bound, meaning "at least". Rollups are not backfilled: no release exists yet, so a database from the
  earlier slices is simply deleted.
- **`QueryTraffic`** returns buckets of one resolution in `[From, To)` (by bucket start), optionally for one host,
  summed over hosts, one row per start and status class, oldest first, at most `MaxTrafficLimit` = 10,000 rows.
- **What retention removes.** Raw data (log entries, metric samples and minute rollups) older than `--retain-logs`
  (default 14 days) and hour rollups older than `--retain-rollups` (default 90 days). `internal/retain` does it
  once at start and then every ten minutes. `--retain-logs` and `--retain-rollups` take Go durations plus a `d`
  suffix for days (`36h`, `14d`).
- **The size cap.** `--max-db-size` (default `1GiB`, `0` turns it off; plain bytes or `KiB`, `MiB`, `GiB`) bounds the
  bytes in use, meaning pages that hold data, not the file length: SQLite keeps the free pages for reuse and the
  file never grows past what was used at its peak. While the use is over the cap, the oldest 10,000 log entries are
  deleted, then, once there are no logs, the oldest 10,000 metric samples. Rollups are never trimmed by the cap,
  because they are tiny and carry the long history. If the cap still cannot be met, the problem is logged and the
  next round tries again.
- **Memory store.** `--no-persist` applies the same retention. Its size is an estimate of the bytes held, enough for
  the cap to bound memory.

## History API (slice 2e, part 1)

The server gets one new dependency, `Options.History`, an interface of the three read methods it needs
(`QueryLogs`, `QueryTraffic`, `QueryMetrics`). Without it the three routes below are a 404, as before. `Options.Now`
makes time testable. Every route is GET only, behind the same Host check, with JSON errors as `{"error": "..."}`.
A malformed parameter is a 400 that names it; a store failure is a 500 with a generic message, the cause goes to the
log.

- **`GET /api/logs`**: `limit`, `before` (an entry ID, to page backwards), `host`, `minStatus`, `maxStatus`, `q`
  (literal, case-insensitive text in the path) and `from` / `to` (RFC 3339). Answers
  `{"entries": [...], "next": ID | null}`, newest first; an entry is `id`, `time`, `host`, `method`, `path`,
  `status`, `durationMs`, `bytes`, `clientIp`. `next` is set when the page is full, and is the `before` of the next
  page.
- **`GET /api/traffic`**: `resolution` (`minute`, the default, or `hour`), `from` and `to` (RFC 3339, default the
  last hour for minutes and the last 24 hours for hours, ending now) and an optional `host`. Answers one bucket for
  every step of the range, empty ones included so a chart needs no gap filling: `start`, `requests`, `bytes`,
  `status` (counts for `1xx` to `5xx` and `other`), `avgMs`, `p95Ms` and `maxMs` (the three are `null` without
  requests). The range may hold at most 3,000 buckets. `p95Ms` is the upper bound of the latency bucket holding the
  95th percentile.
- **`GET /api/summary`**: `window` (`5m`, `1h` default, `6h`, `24h`, `7d`). One object for the dashboard tiles:
  `requests`, `requestsPerSecond` (over the whole window), `errors5xx`, `errorRate` (a fraction), `avgMs`, `p95Ms`,
  `bytes`, `status` as above, `inFlight` and `upstreams` (`healthy` and `total`). Windows up to 6 hours are read from
  the minute rollups, longer ones from the hour rollups; the bucket that contains the start of the window counts
  whole, so a summary can include up to one bucket of older traffic. `errorRate`, `avgMs` and `p95Ms` are `null`
  without requests. `inFlight` and `upstreams` come from the newest metric sample of each series within the last two
  minutes, and are `null` when the scraper has stored nothing that recent.

The interface that uses these routes (history tab, dashboard tiles and charts, the design pass) is deliberately left
to phase 4, where it is designed together with the sites wizard, so the front end is not built twice. It will be
written against an API that already exists and is tested.

## Slices

| Slice | Deliverable | Done when |
|---|---|---|
| 2a | `internal/store`: types, migrations, `SQLite` and `Memory`, the contract suite, `serve --data-dir` and `--no-persist` opening the store | The contract suite passes on both stores; data survives a reopen; migration, permission and newer-schema tests pass |
| 2b | `internal/ingest`: cursor-based log reader feeding the store (resumable `logtail` core, batching, query-string stripping, invalid lines counted) | Restarts, rotation, truncation, partial lines and failing batches never repeat or lose a line |
| 2c | `internal/promtext` (a small Prometheus text parser) and the scraper with counter deltas | Deltas, counter resets and a missing `metrics` option are handled; samples are stored |
| 2d | Rollups, retention, size cap | Rollups match the raw entries; retention and the cap delete the right rows |
| 2e | API (`/api/logs`, `/api/traffic`, `/api/summary`); the interface moves to phase 4 | Go tests pass; the API answers from the history after a restart |
| 2f | Check against the test Caddy over a few days of real traffic, including a real log rotation | Numbers from the API match the log file; nothing repeats or disappears across a restart and a rotation |

## Open questions

- **Charts library (phase 4).** `docs/PLAN.md` names uPlot (small, fast, no framework). Phase 4 confirms it by checking its
  size and licence before adding the dependency, and keeps the markup accessible (a table alternative for each
  chart).
- **Metrics label cardinality.** Settled in slice 2c: a fixed set of aggregated series (see the metrics section).
