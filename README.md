# caddy-view

[![CI](https://github.com/Auugustocesar/caddy-view/actions/workflows/ci.yml/badge.svg)](https://github.com/Auugustocesar/caddy-view/actions/workflows/ci.yml)

A lightweight web UI that runs next to [Caddy](https://caddyserver.com): dashboard, metrics, logs, Caddyfile
editor with validation and rollback, and a reverse proxy wizard.

> **Status: early development.** Done: the Admin API client, `doctor`, `serve` with a read-only web UI (Dashboard,
> Config and live Logs), a history store (access logs, metrics, rollups, retention) with its API, and editing the
> Caddyfile through the API with validation, revisions and rollback. The interface for history and editing, the sites
> wizard, login, a Docker image and releases are still to come: today editing is used through the API, and `serve`
> only listens on loopback. See [docs/PLAN.md](docs/PLAN.md) for the design and phases, and
> [docs/backlog.md](docs/backlog.md) for what is planned next.

## Goals

- Single Go binary (plus a Docker image), SQLite by default so history survives restarts.
- Talks to Caddy through its Admin API, preferably over a unix socket.
- Never leaves Caddy with an invalid configuration: validate, apply, roll back.
- Secure by default: localhost bind, login required, read-only until editing is enabled.

## What works today

### `caddy-view doctor`

Checks that a Caddy setup has what caddy-view needs and tells you how to fix what is missing:

| Check | Looks at |
|---|---|
| `admin-transport` | How the Admin API is reached. A unix socket or loopback is fine; a network address is flagged because the Admin API has no authentication |
| `admin-reachable` | Whether the Admin API answers with the running configuration |
| `metrics` | `metrics` enabled on every HTTP server |
| `access-log` | Access logs written to a file as JSON |
| `caddyfile` | The Caddyfile can be read (with `--caddyfile`) |
| `import` | The Caddyfile imports the file caddy-view manages, by a relative or an absolute path (with `--caddyfile`) |
| `relative-import` | The Caddyfile imports no file by a relative path (with `--caddyfile`). caddy-view refuses to apply such a Caddyfile, because Caddy would resolve the path from its own working directory and drop what is imported. Write it in full: `import /etc/caddy/conf.d/*.caddy` |

```
$ caddy-view doctor --admin localhost:2019
[ ok ] admin-transport: Admin API is reached over loopback TCP
[ ok ] admin-reachable: the Admin API returned the running configuration
[warn] metrics: metrics are not enabled on: srv0
       hint: Add `metrics` to the global options: `{ metrics }`.
[warn] access-log: access logging is not enabled on any HTTP server
       hint: Add `log { output file /var/log/caddy/access.log; format json }` to your sites.

2 ok, 2 warnings, 0 failures
```

Flags:

| Flag | Default | Meaning |
|---|---|---|
| `--admin` | `http://localhost:2019` | Admin API address: `unix:///path`, `unix//path` (Caddy syntax), `http://host:port` or `host:port` |
| `--caddyfile` | none | Path to the Caddyfile; enables the Caddyfile checks |
| `--managed-file` | `conf.d/caddy-view-sites.caddy` | File managed by caddy-view, relative to the Caddyfile directory |

Exit codes: `0` when no check failed (warnings do not fail the run), `1` when a check failed, `2` on a usage error.

### `caddy-view serve`

Runs the web UI and the JSON API. By default it only reads: it never changes Caddy unless you start it with
`--allow-edit` (see "Editing the Caddyfile"). It also keeps a history in a SQLite database: the access log is read into
it, Caddy's metrics are scraped into it, and both are summarised in minute and hour rollups.

```
$ caddy-view serve --admin localhost:2019
caddy-view listening on http://127.0.0.1:8484 (Admin API: http://localhost:2019)
```

Always available, read-only:

| Route | Answer |
|---|---|
| `GET /api/status` | Whether Caddy is reachable: `{"caddy":{"reachable":true,"endpoint":"..."}}` |
| `GET /api/config` | The running configuration, with secrets redacted |
| `GET /api/sites` | Sites and the live state of their upstreams |
| `GET /api/logs/stream` | Live access log as server-sent events. Optional `?backlog=N` (0 to 1000) first sends the last N lines. Returns 404 when no JSON file log is found, 429 beyond 8 open streams |
| `GET /api/logs` | Stored log entries, newest first. Filters `host`, `minStatus`, `maxStatus`, `q` (text in the path), `from`, `to`; paged with `limit` and `before` |
| `GET /api/traffic` | Requests, bytes, status classes and latency per minute or hour (`resolution`), with `from`, `to` and `host` |
| `GET /api/hosts` | The same, totalled per host over a range (`resolution`, `from`, `to`), the busiest first; `limit` (100 by default, at most 500) |
| `GET /api/summary` | Requests per second, 5xx rate, average and p95 latency, in flight and healthy upstreams over a `window` (`5m` to `7d`) |
| `GET /api/system` | How caddy-view itself is doing: whether Caddy is reachable, the ingestion of the access log (file, offset, lines skipped, gaps, newest entry), the metrics scraper (`ok`, `pending`, `disabled` when Caddy has no `metrics`, or `failing`), the database (kind, file, size, schema version), the retention in force, and editing (whether it is on, and whether the Caddyfile and Caddy agree with the revisions) |

```json
{"sites":[{"server":"srv0","host":"app.example.com","upstreams":[{"address":"10.0.0.5:8080","numRequests":3,"fails":0}]}],"upstreamsAvailable":true}
```

Only with `--allow-edit` (a 404 otherwise):

| Route | Answer |
|---|---|
| `GET /api/caddyfile` | The Caddyfile on disk and its SHA-256 |
| `POST /api/caddyfile/validate` | Whether Caddy accepts a candidate, with the line of an error and any warnings |
| `POST /api/caddyfile/format` | The text laid out as `caddy fmt` lays it out; reads nothing from Caddy or from disk |
| `POST /api/caddyfile/apply` | Validates, writes and loads a candidate; undoes it when Caddy refuses it |
| `GET /api/revisions`, `GET /api/revisions/{n}` | The history of attempts, newest first, and one with its content |
| `POST /api/revisions/{n}/rollback` | Applies an earlier revision as a new one |
| `GET /api/managed-sites`, `POST /api/managed-sites` | The sites caddy-view manages (with the managed file, whether the Caddyfile imports it, and whether an access log is known), and creating one (see "Managed sites") |
| `POST /api/managed-sites/import` | Adds the missing `import` of the managed file to the Caddyfile, as a revision of its own |
| `POST /api/managed-sites/preview` | Checks a site and shows the managed file it would produce, without applying |
| `PUT /api/managed-sites/{id}`, `DELETE /api/managed-sites/{id}` | Changing and removing a managed site |

Flags:

| Flag | Default | Meaning |
|---|---|---|
| `--admin` | `http://localhost:2019` | Admin API address, same forms as for `doctor` |
| `--listen` | `127.0.0.1:8484` | Address to listen on. Loopback only (see below) |
| `--show-secrets` | off | Return secrets in clear text from `/api/config` |
| `--log-level` | `info` | `debug`, `info`, `warn` or `error` |
| `--access-log` | from Caddy | JSON access log file to read. By default the first JSON file logger of the running configuration is used; set it when Caddy runs in a container and sees another path |
| `--data-dir` | the user data directory | Where the history database (`caddy-view.db`) lives |
| `--no-persist` | off | Keep the history in memory only; nothing is written to disk |
| `--store-query` | off | Keep query strings in the stored request paths (they often carry tokens) |
| `--retain-logs` | `14d` | How long log entries, metric samples and minute rollups are kept (`36h`, `14d`); `0` keeps them for ever |
| `--retain-rollups` | `90d` | How long hour rollups are kept; `0` keeps them for ever |
| `--max-db-size` | `1GiB` | Most the history may hold; above it the oldest raw data is deleted. `0` turns the cap off |
| `--caddyfile` | none | The Caddyfile caddy-view may edit; used with `--allow-edit` |
| `--allow-edit` | off | Let the API read and change the Caddyfile. Needs `--caddyfile` and the history database (not `--no-persist`) |
| `--managed-file` | `conf.d/caddy-view-sites.caddy` | The file the managed sites are written to, relative to the directory of the Caddyfile unless it is an absolute path. The Caddyfile must import it by its full path |

### Editing the Caddyfile

```sh
caddy-view serve --admin unix:///run/caddy/admin.sock --caddyfile /etc/caddy/Caddyfile --allow-edit
curl -s http://127.0.0.1:8484/api/caddyfile        # the content and its sha256
curl -s -X POST -H 'Content-Type: application/json' http://127.0.0.1:8484/api/caddyfile/apply \
  -d '{"content": "...the whole new Caddyfile...", "baseSha256": "...the sha256 you read..."}'
```

An apply goes through these steps, and Caddy is never left with a configuration it refused:

1. If the file on disk is not the one the change was made from (`baseSha256`), nothing is recorded or written: someone
   else edited it.
2. A revision is recorded, then Caddy checks the candidate (`/adapt`). A rejection stops here and says which line.
   A candidate that imports a file by a relative path (`import conf.d/*.caddy`) is refused too: Caddy resolves that
   path from its own working directory when it is sent the text, so the apply would drop what is imported. Write the
   path in full (`import /etc/caddy/conf.d/*.caddy`), and `doctor` tells you when a Caddyfile has one.
3. A candidate that would move or turn off Caddy's Admin API is refused, since caddy-view could then neither check nor
   undo anything.
4. The file is replaced atomically and Caddy loads it, and caddy-view reads back what Caddy runs and checks that it is
   the configuration it was given. If Caddy refuses it, or does not run it, the previous file is put back and
   reloaded. If that fails too, the revision is `recovery-required`.

Every attempt is a revision with its full content, and a rollback applies an old one as a new revision.

When `serve --allow-edit` starts, it first resolves the revisions that a crash left half done (`pending` or `loading`):
one that never reached the disk is `failed`, one whose file is on disk is `applied` if Caddy runs it and
`recovery-required` if not. It never reloads Caddy by itself. Then it warns if the Caddyfile differs from the last
applied revision, or if Caddy runs something other than the file (a reload from elsewhere).

Only one caddy-view at a time can edit a Caddyfile: `serve --allow-edit` takes a lock kept in `.caddy-view.lock` next
to it and refuses to start while another process holds it. The system frees the lock when that process ends, however it
ends, so there is no stale lock to delete. The design is in [docs/phase-3.md](docs/phase-3.md).

### Managed sites

caddy-view can keep some sites for you: a reverse proxy or a redirect, described as data and written to the managed
file, which your Caddyfile imports. Sites that are already in your Caddyfile are not touched and cannot be listed here.

```caddyfile
# in your Caddyfile, once, with the full path of the managed file's folder:
import /etc/caddy/conf.d/*.caddy
```

```sh
curl -s -X POST -H 'Content-Type: application/json' http://127.0.0.1:8484/api/managed-sites \
  -d '{"hostname": "app.example.com", "kind": "proxy", "upstreams": ["10.0.0.5:8080"], "tls": "automatic"}'
```

A site has a `hostname` (a DNS name, `localhost` or an IP; no port or wildcard), a `kind` (`proxy` or `redirect`) and:
for a proxy, one or more `upstreams` (`host`, `host:port` or `https://host:port`) and `skipTlsVerify` for a self-signed
`https://` upstream; for a redirect, a `redirectTarget` (`https://…`, which may end in `{uri}`) and a `redirectCode`
(301, 302, 307 or 308). Every site also has a `tls` mode (`automatic`, `internal` for Caddy's own certificate
authority, or `off` for plain HTTP), `accessLog` (write the site's requests to the access log caddy-view reads, when
there is one), and `enabled` (a disabled site stays in the list but is left out of the file).

A proxy may ask for a username and a password with `basicAuth`: `{"user": "bob", "password": "…"}` (a username of 1 to 64
letters, digits and `. _ - @`, a password of 8 to 72 bytes). caddy-view makes a bcrypt hash of the password and writes a
`basic_auth` block with the hash; the password itself is not stored, logged or answered, and the API only says that a site has
a password and for whom. Sending the user with an empty password keeps the password the site has; leaving `basicAuth` out
takes it away. Use it with TLS: with `tls: off` the password crosses the network in clear.

A change is applied as a whole or not at all. Caddy checks the managed file and the Caddyfile together, loads them,
and caddy-view reads back what runs; if anything fails, the managed file is put back, the list of sites is unchanged,
and the answer says that nothing was applied and which site is the problem. A hostname that your own Caddyfile already
serves is refused that way. Every change is a revision, so the history and rollback of the Caddyfile cover the managed
file and the list of sites too.

caddy-view overwrites only a managed file that is missing, empty or exactly as it wrote it last: if you edit the file
by hand, the next change is refused with `managed-file-edited` rather than lose your edit. The Caddyfile must import the
managed file (`not-imported` otherwise), and a change to the Caddyfile that would stop importing it while there are
sites is refused too.

### Security notes

- **Loopback only.** There is no login yet, and the Admin API has no authentication, so `serve` refuses to listen
  on anything but `127.0.0.1`, `localhost` or `[::1]`. To reach it from another machine, use an SSH tunnel.
  Login arrives in phase 5.
- **Host check.** Requests whose `Host` header is not a loopback name get a 403, which blocks DNS rebinding.
- **Editing is opt-in, and so is reading the Caddyfile.** The raw file and its revisions can hold secrets that cannot
  be redacted, so `/api/caddyfile` and `/api/revisions` need `--allow-edit` too. Whoever can reach the port can change
  Caddy while it is on, which `serve` says in its log.
- **Cross-site requests.** Every `POST` must be `application/json` (415 otherwise), and one that names an `Origin` must
  name caddy-view itself (403 otherwise), so a web page cannot make your browser edit Caddy. `curl` sends no `Origin`
  and is fine.
- **Secrets are redacted.** Values of keys such as `password`, `token`, `secret`, `key`, `*_token`, `*_secret`
  and credential headers become `[redacted]` in `/api/config`. References like `{env.TOKEN}` or `{file./path}` stay visible.
- **Password hashes are secrets too.** The bcrypt hash of a managed site's password is in the `sites` table, in the snapshots of
  the revisions and in the managed file, so protect the data directory and the managed file as you would a password file. The
  password itself is only ever in the request that sets it.
- **Stored paths drop the query string** unless `--store-query`, since query strings often carry tokens.
- **Logs** go to stderr as text: failures to read Caddy and rejected hosts as warnings, every request as a debug
  line.

## Install

Requires Go 1.26.10 or newer. Older releases have known standard library vulnerabilities, and `go install` fetches
the right toolchain by itself.

```sh
go install github.com/Auugustocesar/caddy-view/cmd/caddy-view@latest
caddy-view doctor --admin localhost:2019
```

Or build from a clone:

```sh
git clone https://github.com/Auugustocesar/caddy-view.git
cd caddy-view
go build -o caddy-view ./cmd/caddy-view
```

## Development

Tests come first: every feature starts with tests written from the requirement, confirmed failing, and only then
the implementation. The code follows the standards in [CLAUDE.md](CLAUDE.md).

```sh
gofmt -s -l .
go vet ./...
go test -race ./...    # -race needs cgo, so a C compiler (gcc) on Windows
go mod tidy -diff
```

Integration tests run a real Caddy and need its binary in `CADDY_BIN` or on the `PATH`; they carry a build tag, so the
command above does not need Caddy. CI runs them with a pinned, checksummed release.

```sh
CADDY_BIN=/path/to/caddy go test -tags integration ./internal/configmgr
```

The UI is a Svelte app in `web/` (Node 22.12+ or 24). Its build is committed in `internal/web/dist`, because the
binary embeds it and `go install` cannot run `npm`. After changing anything in `web/`, rebuild and commit the result;
CI fails when the two differ.

```sh
cd web
npm ci
npm test
npm run build   # writes ../internal/web/dist
npm run dev     # hot reload, proxies /api to `caddy-view serve` on 127.0.0.1:8484
```

CI also runs `staticcheck`, `govulncheck` (weekly too, because new vulnerabilities appear without code changes)
and the tests with `-race` and shuffled order on Linux, macOS and Windows, plus the UI tests and `npm audit`.

## Roadmap

| Phase | Deliverable | State |
|---|---|---|
| 0 | Repo, structure, `doctor`, CI | Done |
| 1 | Read-only UI: dashboard, config, live logs | Done |
| 2 | SQLite, log ingestion, metrics history | Planned |
| 3 | Caddyfile editing with validation and rollback | Planned |
| 4 | Reverse proxy wizard and traffic charts | Planned |
| 5 | Login hardening, releases, Docker image | Planned |

## License

[MIT](LICENSE)
