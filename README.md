# caddy-view

A lightweight web UI that runs next to [Caddy](https://caddyserver.com): dashboard, metrics, logs, Caddyfile
editor with validation and rollback, and a reverse proxy wizard.

> **Status:** planning. No code yet. See [docs/PLAN.md](docs/PLAN.md).

## Goals

- Single Go binary (plus a Docker image), SQLite by default so history survives restarts.
- Talks to Caddy through its Admin API, preferably over a unix socket.
- Never leaves Caddy with an invalid configuration: validate, apply, roll back.
- Secure by default: localhost bind, login required, read-only until editing is enabled.

## License

MIT
