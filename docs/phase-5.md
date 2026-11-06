# Phase 5 — Login, then release

Goal: caddy-view can be reached from more than the local machine without handing Caddy's configuration to whoever can
open the port. Login comes first; the Docker image, the `healthcheck` command and the release follow (`docs/PLAN.md`,
sections 9 and 10; `docs/backlog.md`, B7 and B8). The design pass of the interface stays last, after login, so the
login screen is designed once with the rest.

Everything here is built tests first (`docs/PLAN.md`, section 11). **This document was written before any code; slices 5a and 5b
are done.**

## Decisions: login

| Decision | Choice | Why |
|---|---|---|
| Users | One account, `admin`, kept in the `users` table (a username column, so more can come) | v1 is a single-operator tool; the table shape costs nothing |
| Password hash | argon2id (`golang.org/x/crypto/argon2`, already in the module for bcrypt), stored as a PHC string | The PLAN names Argon2; the PHC string carries its own parameters, so they can be raised later without a migration |
| First start | With no user, `serve` creates `admin` with a random password and prints it once to stderr | The PLAN: "printed once in the log"; nothing is ever stored in clear |
| Reset | `caddy-view reset-password` sets a new random password, prints it once and ends every session | Works with the process stopped, on the same data directory |
| Sessions | A random 32-byte token in a cookie; the database holds only its SHA-256, `created_at`, `last_seen_at` and `expires_at` | A leaked database cannot be used to log in |
| Lifetime | 7 days at most, 24 hours idle | Both are constants of the auth package, tested with an injected clock |
| Cookie | `HttpOnly`, `SameSite=Strict`, `Path=/`, `Secure` when the request came over TLS | The browser never exposes it to scripts and never sends it cross-site |
| CSRF | `SameSite=Strict`, plus the checks that already exist: `Origin` must match `Host` and the body must be `application/json` | A cross-site form cannot send JSON, and a foreign `Origin` is refused; no token to carry |
| Rate limit | 5 failed logins per client address in 15 minutes, then 429 with `Retry-After`; a success clears the count | In memory: it is a brake on guessing, not an audit log |
| Timing | An unknown user costs one hash check as well | The answer and its time do not reveal which usernames exist |
| Always on | The API needs a login unless `--no-auth` is passed, which is accepted only with a loopback `--listen` | The PLAN says secure by default; `--no-auth` keeps a local-only setup possible |
| `--no-persist` | Works, with a new password at each start | Users live in the store like everything else |

### What stays open without a login

`POST /api/login`, `GET /api/health` (slice 5d), and the static UI files, so the login screen can load. Everything else
under `/api/` answers 401 without a valid session.

### Listening beyond loopback (slice 5c)

`ValidateListen` today refuses anything but loopback because there is no login. With login it accepts any address, with
two conditions:
- plain HTTP on a non-loopback address needs `--allow-insecure-http`, and logs a warning at start, since the password
  and the cookie would cross the network in clear text. The expected setup is caddy-view behind Caddy itself;
- the `Host` check (DNS rebinding) is kept: it accepts loopback names plus each `--allowed-host`, so a Caddy site in
  front of caddy-view can name its public hostname.

## Slices

| Slice | What | Done when |
|---|---|---|
| 5a (done) | `internal/auth` (hashing, login, sessions, rate limit) and the users and sessions in both stores | The service and the store contract pass, with an injected clock and cheap hash parameters |
| 5b (done) | The server: `/api/login`, `/api/logout`, `/api/me`, the middleware, the cookie | Every route but the open ones answers 401 without a session |
| 5c (done) | The CLI: first-start password, `reset-password`, `--no-auth`, `--allowed-host`, `--allow-insecure-http`, a non-loopback `--listen` | `serve` is safe by default; the flags are checked together |
| 5d (done) | `GET /api/health` and `caddy-view healthcheck` (B7), `doctor` permission checks (B8) | A Docker `HEALTHCHECK` needs nothing else |
| 5e (done) | The UI: login screen, logout, a 401 sends the user to login | Component tests; the build is committed |
| 5f (done, see the notes) | Docker image and compose example, GoReleaser, systemd unit, README for open source | A fresh-machine install takes minutes |

## Slice 5a in detail

**Store.** Two new tables (migration `0007_auth.sql`): `users` (`id`, `username` unique ignoring case, `password_hash`,
`created_at`) and `sessions` (`token_hash` primary key, `user_id` with `ON DELETE CASCADE`, `created_at`,
`last_seen_at`, `expires_at`). Methods on both stores, in the shared contract suite:

- `SaveUser(ctx, User) (User, error)`: creates the user, or replaces the hash of the one with that username (the ID is
  kept); a missing username or hash is `ErrInvalidUser`.
- `UserByName(ctx, name)` and `UserByID(ctx, id)`: `ErrNotFound` when there is none.
- `CountUsers(ctx)`.
- `AddSession(ctx, Session)`, `Session(ctx, tokenHash)` (`ErrNotFound`), `TouchSession(ctx, tokenHash, at)`,
  `DeleteSession(ctx, tokenHash)`, `DeleteUserSessions(ctx, userID)`.
- `DeleteSessionsBefore(ctx, expiredBefore, idleBefore)`: removes sessions whose `expires_at` is not after the first
  time, or whose `last_seen_at` is not after the second; returns how many.

**Auth package.** `auth.Service` over a consumer-side `Store` interface:

- `HashPassword` and `VerifyPassword` (argon2id, PHC string, constant-time compare, a malformed hash is an error and
  never a match).
- `Login(ctx, username, password, client string) (token string, err error)`: `ErrInvalidCredentials`,
  `ErrRateLimited` (carrying how long to wait).
- `Authenticate(ctx, token) (User, error)`: `ErrNoSession` for an unknown, expired or idle session; it extends
  `last_seen_at`.
- `Logout(ctx, token)`.
- `EnsureAdmin(ctx) (password string, created bool, err error)`: the first-start step.
- `ResetPassword(ctx, username) (password string, err error)`: new hash, every session of the user deleted.
- `Prune(ctx)`: removes expired sessions; `serve` runs it with retention.

Options: the clock, the hash parameters (tests use the cheapest valid ones) and the random source for generated
passwords, so tests are deterministic where it matters and never sleep.

**Tests first.** The store contract (both stores) and the service tests against a fake store and a fake clock: right and
wrong passwords, unknown user takes the same path, rate limit and its window passing, a success clearing the count,
session expiry and idle expiry, `Authenticate` extending the session, logout, reset ending sessions, `EnsureAdmin`
creating once and never again, a stored hash that is not a PHC string.

## What was decided while building, and what is not verified

**Decided on the way** (each has its tests):
- `server.ValidateListen` checks only the syntax now; `server.RequireLoopback` is the old rule, and the CLI decides
  between them from `--no-auth` and `--allow-insecure-http` (slice 5c). The three tests that said "never beyond
  loopback" were rewritten to say what holds now.
- The host of a specific `--listen` address is accepted by the `Host` check without `--allowed-host`; a wildcard
  address (`0.0.0.0`) names no host, so it needs one.
- `--secure-cookie`, which was not in the first proposal: behind a proxy that ends TLS the request reaches caddy-view as
  plain HTTP, and the cookie would not be marked `Secure`.
- `caddy-view version`, so that a release says what it is (`-X ...internal/cli.version`, set by GoReleaser and the
  Dockerfile; `go install` reads the module version).
- `doctor` gained `--access-log`, `admin-socket`, `access-log-readable` and `caddyfile-writable` (B8). The unix socket
  path parser of the `caddy` package is exported (`caddy.UnixPath`) rather than copied.
- The UI: the former `App.svelte` is `Shell.svelte` and is created only with a session, so none of its requests, polls
  or streams starts before the login. A 401 from any request, or from the log stream (which an `EventSource` cannot see,
  so the stream asks the server once it has given up), returns to the login screen.
- The health route is open and answers only `ok` or `unavailable`.

**Verified by running it** (Docker 29.9.3, GoReleaser 2.20.0, Caddy 2 from the official image, on 2026-11-06):
- `goreleaser check` passes, and `goreleaser release --snapshot` makes the four archives and `checksums.txt`; the archive holds
  the binary, `LICENSE`, `README.md`, the compose example and `deploy/*`, and the binary reports its version.
- The image builds (25 MB, runs as 65532, entrypoint and default arguments as intended), `version` runs, and both
  `caddy-view healthcheck` and Docker's own `HEALTHCHECK` (`healthy`) pass with no Caddy around.
- `docker-compose.example.yml` and `deploy/Caddyfile.example`, with only the image, the host ports and the site name changed,
  run against a real Caddy: `doctor` run inside the container gives 10 ok, 0 warnings (so the socket mode `|0666` and the log
  `mode 0644` work for a non-root caddy-view, and `/etc/caddy` is writable by it); the first password is printed once, is not
  printed again after a restart and still logs in; a redirect site made through the API is applied, written to `conf.d` and
  served by Caddy; the access log is ingested and the metrics scraped; `reset-password` as written in the README works
  and ends the old password; port 2019 has no host mapping; logout ends the session.
- The unit file passes `systemd-analyze verify`. It was not started under systemd.

**Not verified:** the release workflow itself (it needs a tag and a registry, so its first real run is the first release),
and the versions of the GitHub actions in it (`goreleaser-action@v6`, the Docker actions), which should be checked
against what is current when the first tag is pushed. The `chown -R 65532:65532 caddy` step of the compose example needs
root, so the test gave the directory mode 777 instead, which is the same access for it.

**Known limits:** the count of failed logins is per connection address, so behind a proxy on the same machine all
clients share one; there is one user; a session cannot be listed or ended one by one from the UI.
