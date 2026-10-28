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

### Slice 4b in detail: managed sites, backend

**Packages and types.**
- `store.Site` is the row: `ID` (32 hex characters, made by the server), `Hostname`, `Kind` (`proxy`, `redirect`),
  `Upstreams`, `SkipTLSVerify`, `TLS` (`automatic`, `internal`, `off`), `RedirectTarget`, `RedirectCode`, `AccessLog`,
  `Enabled`, `CreatedAt`, `UpdatedAt`. Migration 5 adds the `sites` table and, to `config_revisions`, the columns
  `sites_content`, `sites_sha256` and `sites_snapshot` (JSON of the rows). `store.Revision` gets `Sites *RevisionSites`
  (`Content`, `SHA256`, `Snapshot []Site`); `nil` is a revision made before there were managed sites, or without them.
  Both stores get `Sites(ctx)` (ordered by hostname) and `ReplaceSites(ctx, []Site)`, which replaces the whole table
  atomically and refuses a duplicate ID or hostname with `ErrInvalidSite`; the contract suite covers both.
- `internal/sites` is pure: `Normalize`, `Validate` and `Generate`. `Validate(site, others)` returns every problem as a
  `FieldError{Field, Message}` (fields: `hostname`, `kind`, `upstreams`, `skipTlsVerify`, `tls`, `redirectTarget`,
  `redirectCode`, `accessLog`), so the interface can mark the field. `Generate(sites, opts)` returns the text and, for
  each block, the lines it occupies, so that a line of the managed file can be turned back into a site.
- `internal/caddyfile` gains `ImportsFile(text, dir, target)`, built on the lexer of B1: does this Caddyfile import this
  file, by a relative path from `dir` or an absolute one? `doctor`'s `import` check becomes a call to it, so there is one
  answer to the question.

**Validation rules** (all in `sites.Validate`, tested as a table):
- *Hostname*: required; lower-cased by `Normalize`; labels of letters, digits and hyphens that do not start or end with a
  hyphen, at most 63 characters each and 253 in all; `localhost`; an IPv4 or IPv6 address (written in brackets in the
  file). No scheme, port, path, wildcard, space or trailing dot. Unique among the managed sites, ignoring case.
- *Kind*: `proxy` or `redirect`; the fields of the other kind must be empty.
- *Upstreams* (proxy): at least one, no duplicates; each `[http://|https://]host[:port]` with a valid host, a port from 1
  to 65535 if present, and no path, query or user. `Normalize` drops a leading `http://`, which is the default.
- *SkipTLSVerify*: only when some upstream is `https://`.
- *Redirect*: the target is an absolute `http://` or `https://` URL without spaces, quotes or braces, except the
  placeholder `{uri}`; the code is 301, 302, 307 or 308 (`Normalize` makes 0 into 301).
- *TLS*: `automatic`, `internal` or `off`; `Normalize` makes empty into `automatic`.
- *AccessLog*: allowed only when an access log file is known (below).

**The generated file** is fixed by golden tests: a header comment, the snippet `caddy-view-access-log` when some enabled
site wants it, then the enabled sites sorted by hostname, each block with `import caddy-view-access-log`, `tls internal`,
then `reverse_proxy …` or `redir …` in that order. `tls off` writes the address as `http://host`. The access log path
comes from `Options.AccessLog`, which `serve` fills from `--access-log` or from the running Caddy.

**The state of an apply.** A revision now describes the whole state: the Caddyfile text and, when a managed file is
configured, the managed text with the rows. The three operations differ only in what they change:
- `Apply` (an edit of the Caddyfile) changes the Caddyfile and carries the managed state along unchanged;
- `ApplySites(desired)` changes the managed state and leaves the Caddyfile as it is on disk;
- `Rollback(n)` restores both, and the rows, from the revision (a revision without `Sites` restores only the Caddyfile).

**`ApplySites` step by step**, with `ManagedPath` set:
1. *Preconditions*, which record nothing: every site is valid (`ErrInvalidSite` with the field errors); the Caddyfile has no
   relative import (B1) and does import the managed file (`ErrNotImported`); the managed file is in a state caddy-view
   may overwrite: missing, empty, or equal to the text of the last applied revision that had one. Anything else is
   `ErrManagedEdited`: it was edited by hand and would be lost.
2. Record the revision `pending`, with the Caddyfile text and the candidate managed state.
3. Ask Caddy (`/adapt`) about the managed text alone; a rejection is `ErrSiteRejected` naming the site whose block holds the
   line.
4. `loading`; write the managed file atomically, remembering what it was (or that it did not exist).
5. Adapt the Caddyfile again, now that it sees the new sites. A duplicate hostname shows up here; the error is mapped to the
   site when it names the managed file and a line, or, failing that, names a managed hostname.
6. Load the Caddyfile text and verify what Caddy runs, as in phase 3.
7. Only now save the rows (`ReplaceSites`) and mark the revision `applied`. On any failure after step 4 the managed file
   is put back (removed, if it did not exist) and Caddy reloaded; the rows were never touched, which is the transaction.
`Recover` (B2) gets the same understanding: a `loading` revision with `Sites` is `applied` only if the managed file and
the Caddyfile are both what the revision says and Caddy runs them, and then the rows are set from the snapshot, which
is safe to repeat if the crash came after they were saved.

**The API** (only with `--allow-edit`; every non-`GET` needs `application/json` and the same origin, as before):

| Route | Does |
|---|---|
| `GET /api/managed-sites` | `{"sites": [...]}`, ordered by hostname |
| `POST /api/managed-sites/preview` | Validates a site as it would be added or changed and answers the generated text, without applying |
| `POST /api/managed-sites` | Creates a site and applies; `{"site", "revision"}` |
| `PUT /api/managed-sites/{id}` | Replaces a site's fields and applies; 404 when the id is unknown |
| `DELETE /api/managed-sites/{id}` | Removes a site and applies; `{"revision"}` |

A site is `{"id", "hostname", "kind", "upstreams", "skipTlsVerify", "tls", "redirectTarget", "redirectCode", "accessLog",
"enabled", "createdAt", "updatedAt"}`; a request carries the editable fields only, and unknown ones are a 400. Failures,
all saying that nothing was applied and the table is unchanged: `invalid-site` (422) with `errors` as a list of
`{"site", "field", "message"}`; `not-imported` (422); `managed-file-edited` (409); `site-rejected` (422) with the `site`
and Caddy's message and line; and the existing `relative-import`, `apply-failed`, `recovery-required` and `conflict`.

**`serve`** gets `--managed-file` (default `conf.d/caddy-view-sites.caddy`, relative to the Caddyfile's directory, as in
`doctor`) and gives the manager the access log path. `--backup-mode` waits for 4h.

**Tests.** Against fakes for the pure parts, the stores and the manager, as before; then against a real Caddy: a site
created through the manager is served and reaches a real backend, a hostname the hand-written Caddyfile already serves is
refused with the right site named and Caddy untouched, and a rollback restores both files.


**Done, and what the real Caddy taught.** Built as written, with these findings:
- Caddy reports a hostname that is served twice as `ambiguous site definition: http://hand.localhost`, **without a file or a
  line**. So the site is found by the hostname in the message (matched as a whole word, so `a.example.com` is not
  found inside `aa.example.com`), and by the line only when Caddy does give `<managed file>:<line>`; a message that
  names neither is an invalid Caddyfile, not a site.
- A change of the Caddyfile that stops importing the managed file would silently take every managed site down, so
  `Apply` refuses it with `ErrNotImported` while there are sites. This was not in the plan; it is the same promise as the
  admin guard of phase 3.
- Loading is checked as in phase 3: after `/load`, what Caddy runs must be what was adapted, and the managed file and the
  Caddyfile are put back if not. The rows are saved only after that, and a failure to save them is `recovery-required`.
- `doctor`'s `import` check now asks `caddyfile.ImportsFile`, so `doctor`, `ApplySites` and the guard above agree on what
  "imports the managed file" means, including that an import inside a site block does not count.
- The managed sites are in `Revision.Sites`, copied on the way in and out of both stores, so a caller cannot change a
  stored revision by changing what it holds.
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
- **Charts.** An SVG bar chart in the Dashboard (see slice 4c); uPlot, named in the PLAN (small, MIT, no framework), only
  if the Metrics tab needs long series, with size and licence checked before it is added.
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

### Slice 4c in detail

**Look.** Taken from the reference project's screenshots, not copied: warm neutrals (cream in light, warm dark brown in
dark), one indigo accent, cards with large radii and no heavy borders, big light-weight page titles with a one-line
subtitle, a floating pill navigation with an icon per tab, and a round theme button. System fonts only (no web font:
the UI is embedded and works offline). Colours, radii, spacing and type sizes are CSS custom properties in `app.css`;
dark is `prefers-color-scheme` unless the user picked a theme, which `data-theme` on `<html>` then forces.

**Theme.** `lib/theme.js`: `loadTheme` / `saveTheme` (localStorage, failures ignored: private windows throw), `applyTheme`
(`data-theme` on the root, `system` removes it) and `toggledTheme` (the opposite of what is shown now). The button says
what it will do ("Switch to dark theme"). No stored choice means follow the system.

**Dashboard.** Header (title and subtitle), the Caddy banner as now, a "Last 24 hours" row of tiles from
`/api/summary?window=24h` (Requests with in flight, Traffic, Server errors with the count, Avg. response time with p95,
Upstreams healthy when known), a bar chart of requests per hour from `/api/traffic?resolution=hour`, and the running
sites table as now. A value that does not exist yet is an en dash, never zero. When the history routes answer 404
(`--no-persist`) the Dashboard says traffic history is off instead of showing an error. Tile deltas against the previous
period are left out: the API has no previous-period answer and two requests per tile is not worth it yet.

**Chart.** A small SVG bar chart (`components/BarChart.svelte`, next to `Tile.svelte`) instead of uPlot. Reason: it is a few dozen bars, an SVG is
testable in jsdom (uPlot draws on a canvas, which would be untested), it needs no dependency, and it carries the
accessible name and the table with the same numbers for free. uPlot comes back in 4f only if the Metrics tab needs long
series. Helpers: `lib/format.js` (counts, bytes, percent, milliseconds, clock time) and `lib/chart.js` (`niceMax` for the
axis).

**Done when** the Vitest suite passes, `npm run build` is committed with the sources, and the Dashboard has been looked at
in a real browser against a running `serve`, in light, dark and phone width.

**Done, and what the browser showed.** 213 tests pass (139 new). Looked at against a real Caddy 2.11.7 with 24 hours of
access log, at 1280 px in light and dark and at 375 px: the first phone layout gave every tile a full row, so the tile
grid now has a smaller minimum width (two per row on a phone) and tiles are tighter there; the tab names are hidden on a
phone with a visually hidden style, not `display: none`, so the links keep their names. The bundle is 60 KB (23 KB
gzip). Known and left: the chart's first and last hour both read 14:00 (a day apart, no date shown), and the theme
button does not follow a change of the system theme made while the page is open (it reads it on load). The Logs and
Config tabs only inherit the new colours; they are redone in 4f and 4h.

## Slices

The backend of managed sites comes first, because it is the part with real risk and it is checked against a real Caddy;
the interface then is built on APIs that exist and are tested, as for the history routes.

| Slice | Deliverable | Done when |
|---|---|---|
| 4a | The status API (`/api/system`) with the state it needs from ingestion and the scraper (done) | Handler tests with fakes pass; the numbers match the store on a real run |
| 4b | Managed sites, backend: migration 5, validation, the generator, apply with a managed file, rollback of both, `--managed-file`, the API (`/api/managed-sites`), done; `--backup-mode` waits for 4h | A real Caddy serves a site created through the API, refuses a duplicate hostname, and rolls it back; a killed apply is recovered |
| 4c | Design system, app shell, Dashboard with tiles and the traffic chart (done) | Tests pass; looked at in light, dark and phone width |
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
