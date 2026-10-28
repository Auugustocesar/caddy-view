# Phase 3 — Edit the Caddyfile safely

Goal: change Caddy's configuration from caddy-view, and an invalid configuration never leaves Caddy broken. Every
change is a stored revision that can be restored.

Everything here is built tests first (see `docs/PLAN.md`, section 11). The apply flow is `docs/PLAN.md`, section 6;
this document pins down the parts that section leaves open.

## Scope

| In | Out (later phase) |
|---|---|
| Read the Caddyfile on disk; validate a candidate with `/adapt` | Sites wizard and the generated `caddy-view-sites.caddy` (phase 4) |
| Apply: revision, validate, atomic write, `/load`, automatic restore on failure | Login, users, sessions (phase 5) |
| Revision history and rollback | The `sites-only` backup mode (phase 4, with the sites table) |
| Detect edits made outside caddy-view (stale base, drift at start) | History tab, dashboard and design pass (phase 4) |
| Write routes behind `--allow-edit` and a `--caddyfile` path | |

## Decisions

- **One package, `internal/configmgr`.** A `Manager` owns the apply flow. It depends on two small interfaces it
  declares itself: `Caddy` (`Adapt`, `Load`, which `caddy.Client` already has) and `RevisionStore`. The Caddyfile
  is a plain file at `Options.Path`.
- **Revisions live in the store.** `store.Revision` is a number (assigned by the store, strictly increasing), a time
  (Unix ms UTC), a trigger (`manual-edit`, `site-change`, `rollback`), a status, the full content, its SHA-256 and an
  error message. Migration 4 adds the table; `SQLite` and `Memory` run one shared contract suite, as before.
  Only `full` backup mode exists in this phase.
- **Status machine.** `pending → loading → applied | failed | recovery-required`. A revision is created `pending`
  before anything touches the disk, so every attempt that got past the stale-base check leaves a trace, including a
  rejected candidate (`failed` with the Caddy error).
- **Stale base.** Apply takes the SHA-256 of the file the editor was based on. If the file on disk has another hash
  the apply is refused with `ErrConflict` and nothing is recorded. The hash alone decides; modification times add
  nothing a hash does not already say.
- **Serialised.** One apply or rollback at a time, under a mutex. A second caller with the same base then sees a
  changed file and gets `ErrConflict`.
- **Atomic write.** The new content goes to a temporary file in the same directory and is renamed over the
  Caddyfile; the file mode is kept. A crash never leaves a half-written Caddyfile.
- **Failure handling.** If `/load` fails, the previous content is written back and loaded again. The restore runs
  on a context that ignores the caller's cancellation, so a closed browser tab cannot stop it half way. If the
  restore itself fails (file or reload) the revision is `recovery-required`, the error says so and the UI must show
  it prominently. Caddy keeps serving its old configuration when `/load` is rejected, so this is the rare case.
- **Rollback is an apply.** `Rollback(n)` applies revision `n`'s content as a new revision with trigger `rollback`;
  history is never rewritten.
- **Reconcile at start.** `Reconcile` compares the file on disk with the newest `applied` revision and reports drift
  (someone edited outside caddy-view). It only reports; serve logs a warning. No history yet is not drift.
- **Errors are sentinels** wrapped with the cause: `ErrConflict`, `ErrInvalid` (wrapping the `*caddy.AdaptError`, so
  the line number survives), `ErrApplyFailed` (the previous configuration was restored), `ErrRecoveryRequired`.

## Revision storage (slice 3b)

Both stores get five methods, the four `configmgr.RevisionStore` needs plus a listing for the history API:
`AddRevision`, `UpdateRevision`, `LatestApplied`, `Revision` and `Revisions`.

- **Table `config_revisions`**, migration 4: `number` is an `INTEGER PRIMARY KEY AUTOINCREMENT`, so a number is never
  reused, even after a delete. Time is Unix ms UTC; content, digest, trigger, status and error are text.
- **Validation.** `AddRevision` refuses, with `ErrInvalidRevision` and without taking a number: an unknown or empty
  trigger or status, a digest that is not 64 lower-case hex characters, and a zero time. The store checks the shape of
  the digest, not that it matches the content; that is the manager's job. An empty content is valid (an empty
  Caddyfile). The `Number` passed in is ignored. `UpdateRevision` refuses an unknown status the same way and an unknown
  number with `ErrNotFound`; `Revision` of an unknown number is also `ErrNotFound`.
- **`LatestApplied`** is the applied revision with the highest number, not the last one updated.
- **`Revisions`** lists newest first, `Limit` (default 50, capped at 1,000, negative is `ErrInvalidQuery`) and
  `Before` (a number, to page backwards). The content is included; the API may leave it out of the list.
- **Concurrency and closing** follow the other store methods: safe to share, `ErrClosed` after `Close`.
- Revisions are never pruned by retention: they are the rollback history and are small.

## Caddyfile API and flags (slice 3c)

`serve` gets two flags: `--caddyfile PATH`, the file to edit, and `--allow-edit`, which turns the routes below on.
Without `--allow-edit` they are a 404, like the history routes without a store, and the server stays read-only.
`server.Options.Editor` is an interface of the six methods the routes need (`Current`, `Validate`, `Apply`,
`Rollback`, `Revision`, `Revisions`); `configmgr.Manager` and the store provide them.

- **Why every route needs `--allow-edit`, reads included.** The raw Caddyfile and its revisions can hold secrets that
  cannot be redacted reliably (`/api/config` redacts the JSON; a text file has no structure to redact). So reading
  them is as sensitive as changing them.
- **`--allow-edit` rules**, refused at start with exit code 2 and a message that names the flags: it needs
  `--caddyfile`; it cannot be combined with `--no-persist` (the revisions are the rollback history and must survive a
  restart); the Caddyfile must be a readable file. At start the manager reconciles and a drift is a warning in the
  log; serve also warns that `--allow-edit` lets whoever can reach the port change Caddy.
- **Loopback is already enforced.** `serve` listens on loopback only, whatever the flags say, until phase 5 adds
  authentication, so the open question about `--allow-edit` on a LAN address has no case to cover: there is none.
- **Cross-site requests.** Without a login, a page on another origin could still send a request to `127.0.0.1`. The
  Host check stops DNS rebinding but not that. So every POST must have `Content-Type: application/json` (415
  otherwise; a browser cannot send that cross-site without a CORS preflight, which is never answered), and a request
  that carries an `Origin` must carry the one of the Host it was sent to (403 otherwise). Requests without `Origin`,
  such as `curl`, pass. This applies to rollback too, which has no body.
- **Routes** (JSON; a body is at most 1 MiB, 413 above it; unknown fields are a 400):
  - `GET /api/caddyfile` answers `{"content", "sha256"}`.
  - `POST /api/caddyfile/validate` takes `{"content"}` and answers 200 `{"valid": true, "warnings": [...]}` or
    200 `{"valid": false, "error", "line"}`: a rejected Caddyfile is the expected answer to a validation, not an HTTP
    error. A Caddy that cannot be reached is a 502.
  - `POST /api/caddyfile/apply` takes `{"content", "baseSha256"}` (both required) and answers 200
    `{"revision": {...}}`.
  - `GET /api/revisions` (`limit`, `before`) answers `{"revisions": [...], "next": number | null}`, newest first,
    without the content. `GET /api/revisions/{n}` answers one revision with its `content`; 404 when unknown.
  - `POST /api/revisions/{n}/rollback` answers like apply.
  - A revision is `number`, `time` (RFC 3339, UTC), `trigger`, `status`, `sha256` and `error` (when set).
- **Failures of apply and rollback** are `{"error", "code", ...}`: `conflict` (409), `invalid` (422, with `line`),
  `apply-failed` (502, Caddy refused to load it and the previous file was restored, with the `revision`),
  `recovery-required` (500, with the `revision`) and `not-found` (404, rollback of an unknown revision). Anything else
  is a 500 with a generic message; the cause goes to the log.

## Slices

| Slice | Deliverable | Done when |
|---|---|---|
| 3a | `internal/configmgr` against fakes: `Current`, `Validate`, `Apply`, `Rollback`, `Reconcile`; `store.Revision` types | The state machine, stale base, atomic write, restore and recovery tests pass with `-race` |
| 3b | Revisions in `store`: migration 4, `SQLite` and `Memory`, a contract suite | Both stores pass the suite; numbers never repeat; data survives a reopen |
| 3c | HTTP: `GET /api/caddyfile`, `POST /api/caddyfile/validate`, `POST /api/caddyfile/apply`, `GET /api/revisions[/{n}]`, `POST /api/revisions/{n}/rollback`; `serve --caddyfile` and `--allow-edit` | Routes are a 404 without `--allow-edit`; the API tests and the serve test pass |
| 3d | Caddyfile tab: editor with validation, diff, apply, history and rollback | JavaScript tests pass; an invalid config is refused in the UI |
| 3e | Integration against a real Caddy: invalid candidate, failing `/load`, rollback, an edit made outside | An invalid config never leaves Caddy broken, checked on the test Caddy |

## Open questions

- **Security of the write routes before login exists.** Phase 5 adds authentication, but phase 3 adds routes that
  change Caddy. Proposal: they exist only with `--allow-edit`, and `--allow-edit` is refused unless `--listen` is
  loopback, plus the existing Host check. To confirm in slice 3c.
- **Editor widget.** `docs/PLAN.md` names CodeMirror 6 with a simple Caddyfile mode. Size and licence are checked
  in slice 3d before it is added. Whether the editor tab waits for the phase 4 front-end work is decided in 3d.
