# Phase 4 — The interface, and managing sites

Goal: everything the backend of phases 2 and 3 can already do becomes usable from the browser, and a reverse proxy can
be created from it. The interface is designed and built once, here, with the design pass, so it is not written twice
(`docs/PLAN.md`, phase 4; the decision is in `docs/backlog.md`).

Everything here is built tests first (`docs/PLAN.md`, section 11). **This document was written before any code and has
been reviewed; one question is still open (3, at the end).** Nothing in it is implemented yet.

## What exists, what is missing

| Already there | Missing |
|---|---|
| UI: Dashboard (status, sites, upstreams), Config (running JSON), live Logs. Svelte 5, plain JavaScript and CSS, 578 lines, 74 tests | Charts, history search, the Caddyfile tab, the Sites tab, a design system, a status page |
| API for history (`/api/logs`, `/api/traffic`, `/api/summary`) and editing (`/api/caddyfile`, `/api/revisions`) | The status API (B5), and everything about managed sites |
| The apply flow with revisions, recovery, verification and a lock (phase 3) | An apply that also writes a second file, the managed sites file |

## Scope

| In | Out (later) |
|---|---|
| Tabs: Dashboard, Metrics, Logs (live and history), Sites, Caddyfile, Config (status and running JSON) | Login, users, sessions (phase 5) |
| A design system: tokens, light and dark, responsive, accessible charts | A Docker image, releases (phase 5) |
| Managed sites: proxy and redirect, TLS modes, upstream options, enable and disable, a wizard | Wildcard hostnames and DNS challenges, importing sites that already live in the Caddyfile |
| The status API (B5) and the `--backup-mode` flag | Multiple Caddy instances, plugins (non-goals of v1) |

## Decisions

### Managed sites

- **Intent lives in SQLite, the generated file lives next to the Caddyfile.** A `sites` table holds what the user asked
  for; `caddy-view-sites.caddy` (default `conf.d/caddy-view-sites.caddy`, `--managed-file`) is generated from it and is
  the only file besides the Caddyfile that caddy-view writes. Sites already in the hand-written Caddyfile are shown
  read-only, marked "not managed". There is no import of them in v1.
- **The main Caddyfile must import the managed file by an absolute path** (B1 explains why a relative one cannot be
  applied). The Sites tab says so when it does not, and offers to add `import /abs/path/conf.d/*.caddy` as an ordinary
  Caddyfile apply that the user sees as a diff first. The `doctor` check `import` is the same test.
- **A site change is one transaction with its apply.** The row is saved, the file generated and applied; if Caddy
  refuses it, the row change is undone and the error is shown, so the table always describes what runs. The reference
  keeps a "desired state" that can be out of sync with Caddy and shows a sync status; that is more states for the user
  to understand, and we do not need it. A consequence: a site cannot be created while Caddy is unreachable.
- **A refused change says so, says why, and points at the place.** The answer states plainly that nothing was applied
  and the table is unchanged. Since the file is generated, a line number in it means nothing to the user, so the
  generator records which lines belong to which site and the error is shown against that site (and its field when
  the check that failed is ours: a hostname that is already managed, an upstream that does not parse). A problem that
  only Caddy finds, such as a hostname that the hand-written Caddyfile already serves, is reported with the line
  Caddy gave, translated to the site it falls in. In the Caddyfile editor the line is marked in the text.
- **Apply with a managed file.** The flow of phase 3 validates the candidate before writing it. With two files that is no
  longer possible for the whole, because `/adapt` of the Caddyfile reads the managed file from disk. So: check the
  managed text alone with `/adapt` (a fragment of site blocks is a valid Caddyfile), write both files atomically, adapt
  the Caddyfile again (now it sees the new sites; a duplicate hostname with the hand-written part fails here), load,
  verify, and on any failure restore both files. The stale-base check covers both files. The admin guard and the relative
  import check apply to the Caddyfile; the managed file is generated and has no imports.
- **A revision covers both files** (`--backup-mode full`, the default): the Caddyfile content, and the managed file with
  a snapshot of the `sites` rows. Migration 5 adds `sites_content`, `sites_sha256` and `sites_snapshot` to
  `config_revisions`; older rows hold `NULL`, meaning "the managed file was not part of it". Rolling back restores the
  files and the rows together, in one transaction with the apply. `sites-only` (below) versions only the managed part.
- **`--backup-mode sites-only`**: caddy-view never writes the hand-written Caddyfile; revisions hold the managed file
  and the rows; the Caddyfile tab is read-only and the Caddyfile apply routes are a 404. It needs the Caddyfile path
  only to know where the managed file and the lock are.

### The options of a site (B6)

| Option | In v1 | Generated text |
|---|---|---|
| Hostname | Yes. A lower-case DNS name, `localhost` or an IP; no scheme, port or wildcard. Unique | the site address |
| Kind | `proxy` and `redirect` | `reverse_proxy …` or `redir …` |
| Upstreams (proxy) | One or more `[http\|https://]host[:port]`; no path, query or user | `reverse_proxy a b` (Caddy's default policy) |
| Skip TLS verification | Yes, only for `https://` upstreams (self-signed ones are common at home) | `transport http { tls_insecure_skip_verify }` |
| TLS mode | `automatic` (Caddy's own), `internal` (Caddy's local CA) or `off` (plain HTTP) | nothing, `tls internal`, or the address `http://host` |
| Redirect | A target `http(s)://…`, which may use `{uri}`, and a code 301, 302, 307 or 308 | `redir <target> <code>` |
| Enabled | Yes; a disabled site stays in the table and is left out of the file | the block is omitted |
| Access log | Yes, when the access log file is known: the first JSON file logger of the running Caddy, found the way ingestion finds it (a `--access-log` given by hand wins) | `import caddy-view-access-log`, a snippet the managed file defines |
| Basic auth | Yes, in its own slice (4g). One user per site; a bcrypt hash made in-process, the password never stored | `basic_auth { user <hash> }` |

- Basic auth is a Caddy directive inside the site block, like the others, so it lands in the managed file next to
  `reverse_proxy`; only the hash is written there and kept in the `sites` table, which makes that table confidential
  like a password file (the revisions that snapshot it too). The wizard never shows a stored password, only whether
  one is set.
- It needs `golang.org/x/crypto/bcrypt`, because Caddy wants a bcrypt hash and the alternative is running
  `caddy hash-password`, which needs the Caddy binary where caddy-view runs. That is the dependency's reason, as
  `CLAUDE.md` asks.
- **Validation is strict and has one implementation**, used by the API and by the generator, so text that is not
  known to be safe never reaches the file. Values are checked, not escaped; the generator only writes what validation let
  through. Sites are written sorted by hostname, so a change is a small diff.

```caddyfile
# Managed by caddy-view. Changes made here are overwritten: edit the sites in caddy-view.
(caddy-view-access-log) {
	log {
		output file /var/log/caddy/access.log
		format json
	}
}

app.example.com {
	import caddy-view-access-log
	reverse_proxy https://10.0.0.5:8443 10.0.0.6:8443 {
		transport http {
			tls_insecure_skip_verify
		}
	}
}

http://plain.example.com {
	redir https://new.example.com{uri} 301
}
```

### The status API (B5)

`GET /api/system`, read-only and always available, answers what the Config tab and the editing warnings need:
- `caddy`: reachable, endpoint.
- `ingestion`: the file read, its offset, lines skipped, gaps, and the time of the newest stored entry.
- `scraper`: the last success, the last error, and whether `metrics` is enabled in Caddy.
- `database`: size, schema version, and the retention policy in force.
- `editing`: whether `--allow-edit` is on, the Caddyfile and managed file, drift and `Running` from `Reconcile`, whether
  the managed file is imported, the backup mode.
The answer is one JSON object; a section whose source does not exist is left out, a failure inside a section is that
section's own `error` (the cause goes to the log, the client gets a generic text), and the route itself still answers 200
(it is a status page, like `/api/status`). Times are RFC 3339 in UTC.

```json
{
  "caddy": {"reachable": true, "endpoint": "unix:///run/caddy/admin.sock"},
  "ingestion": {"state": "following", "file": "/var/log/caddy/access.log", "offset": 482113,
                "skippedLines": 0, "gaps": 1, "newestEntry": "2026-10-28T09:12:44.512Z"},
  "scraper": {"state": "ok", "lastSuccess": "2026-10-28T09:12:50Z"},
  "database": {"kind": "sqlite", "file": "/data/caddy-view.db", "sizeBytes": 18874368, "schemaVersion": 4},
  "retention": {"rawSeconds": 1209600, "rollupsSeconds": 7776000, "maxBytes": 1073741824},
  "editing": {"enabled": true, "caddyfile": "/etc/caddy/Caddyfile",
              "drift": {"known": true, "drifted": false, "revision": 12, "running": "matches"}}
}
```

- `ingestion.state` is `waiting` (nothing read yet, or the file is not there; before the log file is found `file` is
  empty), `following` or `failing` (with `error`). It exists when the history does.
- `scraper.state` is `pending`, `ok`, `disabled` (Caddy answered 404: `metrics` is not enabled) or `failing` (with
  `error`; `lastSuccess` is kept).
- `database.kind` is `sqlite` or `memory`; a memory store has no `file` and no `schemaVersion`.
- `editing.drift.running` is `matches`, `differs` or `unknown`, from `Reconcile` (phase 3, B3).
- The producers keep their own state: `ingest.Ingester.Status()` and `scrape.Scraper.Status()`, safe to call while they
  run. Until `serve` has found the access log there is no ingester, so `serve` answers `waiting` for it.

### The interface

- **Tabs.** Dashboard (tiles from `/api/summary`, a traffic chart, running sites with upstream health); Metrics (traffic,
  status classes and latency by period and host); Logs (live tail, and a history search over `/api/logs`); Sites (running
  sites, read-only, and managed sites with the wizard); Caddyfile (editor, validation as you type, diff, apply, history,
  rollback); Config (status from `/api/system`, running JSON).
- **Design system.** CSS custom properties for colour, spacing and type; light and dark following the system (the app
  already has a `prefers-color-scheme` block) with a manual switch; one set of components (table, tile, chart frame, empty
  state, error banner, confirm dialog); layouts for a phone width; keyboard use and focus order; every chart has a table
  with the same numbers.
- **Charts.** uPlot, named in the PLAN: small, MIT, no framework. Size and licence are checked before it is added.
- **Editor.** A modern code editor, CodeMirror 6, with: Caddyfile syntax colouring, line numbers, matching of `{ }` and
  `( )` (highlighted pairs, and closing brackets added as you type), indentation that follows the braces, the line
  of an error marked in the margin, a diff against the live file, and **auto-format**. Formatting is `Format` in
  `internal/caddyfile`, reached through `POST /api/caddyfile/format`, so it is tested in Go and uses the same lexer as
  the import check. Caddy's own `caddy fmt` is the reference: an integration test runs both on the same inputs and the
  goal is the same output; any difference that remains is written down. The bundle size is measured before the
  dependency is added and reported, and it is only a decision for you if the UI grows past about 500 KB compressed.
- **Tests.** Component tests with Vitest and Testing Library, as now, and every UI slice is looked at in a real browser,
  against a running `serve` and a real Caddy, in light, dark and phone width, to judge layout and ease of use; the
  screenshots are kept for the README of the release. There is no Playwright in CI for now; it can be added later.
- **Text.** All UI text is English, as the rest of the repository.

## Slices

The backend of managed sites comes first, because it is the part with real risk and it is checked against a real Caddy;
the interface then is built on APIs that exist and are tested, as for the history routes.

| Slice | Deliverable | Done when |
|---|---|---|
| 4a | The status API (`/api/system`) with the state it needs from ingestion and the scraper (done) | Handler tests with fakes pass; the numbers match the store on a real run |
| 4b | Managed sites, backend: migration 5, validation, the generator, apply with a managed file, rollback of both, `--managed-file`, `--backup-mode`, the API (`/api/managed-sites`) | A real Caddy serves a site created through the API, refuses a duplicate hostname, and rolls it back; a killed apply is recovered |
| 4c | Design system, app shell, Dashboard with tiles and the traffic chart | Tests pass; looked at in light, dark and phone width |
| 4d | Caddyfile tab: the editor (colours, line numbers, matching pairs, error line marked), `Format` and `POST /api/caddyfile/format`, live validation, diff, apply, history, rollback | `Format` matches `caddy fmt` on a real Caddy for the cases tested; an invalid Caddyfile is refused in the browser with its line marked, a good one applied and rolled back |
| 4e | Sites tab and the wizard, including "add the import" | Creating a reverse proxy from the browser works against a real Caddy |
| 4f | Metrics tab and Logs history search | Charts match `/api/traffic`; a search finds a stored request |
| 4g | Basic auth: bcrypt in-process, the field in the wizard, the generated `basic_auth` block | A real Caddy asks for the password of a site and accepts the right one; the password is stored nowhere |
| 4h | Config tab; `--backup-mode sites-only` if question 3 is answered yes | The tab shows what `/api/system` says |


## Answers to the review

1. **Transaction.** A site change is saved only if it applies. A refusal says that nothing was applied and why, and
   points at the site, or the line, that caused it (see "A refused change says so").
2. **Everything is in v1**, basic auth included, as its own slice (4g). Basic auth is a directive of the site block, so
   it is written into the managed file with the others.
4. **A modern editor**: CodeMirror 6, with colours, line numbers, matching `{ }` and `( )`, an error line in the margin and
   auto-format, as listed under "The interface".
5. **Real-browser checks** of every UI slice in light, dark and phone width; no Playwright in CI for now.
6. **The access log path is discovered from the running Caddy**, as ingestion does; `--access-log` still wins when given.

## Still open

3. **`--backup-mode sites-only`.** In the default mode, `full`, every revision keeps a copy of the whole hand-written
   Caddyfile in the database, so that a rollback can restore it. That copy can hold secrets (tokens, passwords) and it lets
   caddy-view rewrite a file the user wrote by hand. `sites-only` is for people who do not want that: caddy-view then
   only ever writes the managed file, keeps revisions of the managed file alone, and the Caddyfile tab only shows the
   file. The cost is one more code path and its tests. Proposed: keep it in this phase, as the last item (4h), so that it
   can slip past v1 without holding anything else; the default stays `full`.
