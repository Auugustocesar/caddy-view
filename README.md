# caddy-view

[![CI](https://github.com/Auugustocesar/caddy-view/actions/workflows/ci.yml/badge.svg)](https://github.com/Auugustocesar/caddy-view/actions/workflows/ci.yml)

A lightweight web UI that runs next to [Caddy](https://caddyserver.com): dashboard, metrics, logs, Caddyfile
editor with validation and rollback, and a reverse proxy wizard.

> **Status: early development.** The foundation and the read-only API are done: Admin API client, `doctor`,
> `serve` (JSON API only) and CI on Linux, macOS and Windows. The web UI is not built yet.
> See [docs/PLAN.md](docs/PLAN.md) for the full design and [docs/phase-1.md](docs/phase-1.md) for what is next.

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
| `import` | The Caddyfile imports the file caddy-view manages (with `--caddyfile`) |

```
$ caddy-view doctor --admin localhost:2019
[ ok ] admin-transport: Admin API is reached over loopback TCP
[ ok ] admin-reachable: the Admin API returned the running configuration
[warn] metrics: metrics are not enabled on: srv0
       hint: Add `metrics` to the servers global option: `{ servers { metrics } }`.
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

Runs the read-only JSON API. It never changes Caddy: every route accepts `GET` only.

```
$ caddy-view serve --admin localhost:2019
caddy-view listening on http://127.0.0.1:8484 (Admin API: http://localhost:2019)
```

| Route | Answer |
|---|---|
| `GET /api/status` | Whether Caddy is reachable: `{"caddy":{"reachable":true,"endpoint":"..."}}` |
| `GET /api/config` | The running configuration, with secrets redacted |
| `GET /api/sites` | Sites and the live state of their upstreams |

```json
{"sites":[{"server":"srv0","host":"app.example.com","upstreams":[{"address":"10.0.0.5:8080","numRequests":3,"fails":0}]}],"upstreamsAvailable":true}
```

Flags:

| Flag | Default | Meaning |
|---|---|---|
| `--admin` | `http://localhost:2019` | Admin API address, same forms as for `doctor` |
| `--listen` | `127.0.0.1:8484` | Address to listen on. Loopback only (see below) |
| `--show-secrets` | off | Return secrets in clear text from `/api/config` |
| `--log-level` | `info` | `debug`, `info`, `warn` or `error` |

Security notes:

- **Loopback only.** There is no login yet, and the Admin API has no authentication, so `serve` refuses to listen
  on anything but `127.0.0.1`, `localhost` or `[::1]`. To reach it from another machine, use an SSH tunnel.
  Login arrives in phase 5.
- **Host check.** Requests whose `Host` header is not a loopback name get a 403, which blocks DNS rebinding.
- **Secrets are redacted.** Values of keys such as `password`, `token`, `secret`, `key`, `*_token`, `*_secret`
  and credential headers become `[redacted]`. References like `{env.TOKEN}` or `{file./path}` stay visible.
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
go test ./...
go mod tidy -diff
```

CI also runs `staticcheck`, `govulncheck` (weekly too, because new vulnerabilities appear without code changes)
and the tests with `-race` and shuffled order on Linux, macOS and Windows.

## Roadmap

| Phase | Deliverable | State |
|---|---|---|
| 0 | Repo, structure, `doctor`, CI | Done |
| 1 | Read-only UI: dashboard, config, live logs | In progress: API and `serve` done; log tail and UI next |
| 2 | SQLite, log ingestion, metrics history | Planned |
| 3 | Caddyfile editing with validation and rollback | Planned |
| 4 | Reverse proxy wizard and traffic charts | Planned |
| 5 | Login hardening, releases, Docker image | Planned |

## License

[MIT](LICENSE)
