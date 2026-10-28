# Backlog from the comparison with caddy-visualizer

After phase 3, caddy-view was compared with [caddy-visualizer](https://github.com/dree007/caddy-visualizer), the
reference project named in the first plan (a source of good practices, not copied). The comparison was made from its
README and source files; the project was not run. Everything it turned up that is not already scheduled in
`docs/PLAN.md` is here, with the phase where it goes, so that nothing depends on memory.

Each item is built tests first, like everything else (`docs/PLAN.md`, section 11). Size is a rough guess: S is under a
day, M a few days, L about a week.

## Order

| When | Item | Size |
|---|---|---|
| Now (docs) | **B0** README and PLAN were out of date | S, done with this document |
| Slice 3f, before phase 4 | **B1** a relative `import` makes an apply drop the imported sites: refuse it, and fix `doctor` (spike done, confirmed; implemented) | M |
| | **B2** resolve revisions left unfinished by a crash (done) | M |
| | **B3** verify the running config after `/load`, and check drift against the running Caddy (spike done, stable; done) | M |
| | **B4** one apply at a time across processes (done) | M |
| Phase 4, slice 4a | **B5** a system status API, before any UI that shows it | M |
| Phase 4 design | **B6** the options of a site, decided before the wizard | S (a decision, then code) |
| Phase 5 | **B7** a health route and a `healthcheck` command for Docker | S |
| | **B8** `doctor` checks permissions (its relative-import check is done in B1) | S |
| Phase 5, release | Rewrite the README for an open source audience (see "Decided not to do") | M |

Slice 3f comes before phase 4 on purpose: B1 to B4 are about the promise of phase 3, that Caddy is never left broken,
and the interface of phase 4 should be built on an apply flow that has them.

## B0. Documentation had drifted (done)

The README still said the API was read-only and pointed at phase 2 as "next"; the PLAN header said phase 1 was next
and its package table listed `internal/metrics`, which became `internal/promtext` and `internal/scrape`. Both are
fixed in the same change as this document. From here the README is kept up to date by hand with each change of flags
or routes, until it is rewritten for the first release.

## B1. A relative `import` makes an apply drop the imported sites (confirmed, done)

**What the spike found.** The manager sends the Caddyfile text to `/adapt` and `/load`, and Caddy gives that text no file
name. A relative `import` (the very one `doctor` recommends: `import conf.d/*.caddy`) is then resolved against Caddy's
working directory instead of the Caddyfile's directory, which is what Caddy does when it starts from the file. The
integration test `TestRealCaddyResolvesRelativeImportsFromTheCaddyfilesDirectory` ran this against Caddy 2.11.7, with
Caddy started from another directory: at start the imported site was served; after a harmless change applied through
the manager, **the apply reported success and the imported site was gone**. Caddy only logs "No files matching import
glob pattern" and loads a configuration without it. It is the exact failure the manager exists to prevent, and it would
hit every setup that follows `doctor`'s advice, including the managed `conf.d/caddy-view-sites.caddy` of phase 4.

**What was tried and does not work.** A `filename` parameter on the `Content-Type` of `/adapt` (`text/caddyfile;
filename=/etc/caddy/Caddyfile`) has no effect: Caddy's admin API passes no options to the adapter. There is no admin
endpoint that loads "the Caddyfile at this path".

**Decision.** caddy-view refuses what it cannot apply faithfully:
1. A new package `internal/caddyfile` finds the relative file imports of a Caddyfile text: `import` directives, outside
   comments and heredocs, whose pattern is not absolute and looks like a file (it has a path separator, a glob
   character or a dot) and is not a snippet defined in the text. A pattern that contains a placeholder (`{$DIR}/*`) is
   left alone, since it cannot be known before Caddy expands it. Imports inside an imported file are Caddy's business:
   it resolves them against that file's directory, correctly.
2. `Validate` and `Apply` (so `Rollback` too) refuse a candidate with one, with `ErrRelativeImport` and the line, after
   Caddy has accepted the syntax. The API answers `valid: false` for a validation and 422 with code `relative-import`
   for an apply. Nothing is written or loaded; the revision is `failed` with the reason. The fix is to write the path in
   full (`import /etc/caddy/conf.d/*.caddy`), which the editor can then apply.
3. `doctor` stops recommending the relative form: its `import` check also accepts an absolute import of the managed
   file (the directory comes from `--caddyfile`), its hint gives the absolute path, and a new `relative-import` check warns
   about any relative file import in the Caddyfile. This is the relative-import part of B8, done here.

**Not done, on purpose.** Rewriting relative imports to absolute ones in memory before adapting would keep such setups
working without a change to the file, but it means parsing Caddyfile text well enough to be right about snippets,
arguments and nesting, and a wrong rewrite would be another silent change of what is loaded. A refusal that names the
line is safer, and the scanner above errs towards refusing only what is certainly a file path. A relative import it
does not recognise would still be dropped silently; the scanner's tests are meant to make that as unlikely as they can,
and B3 (verify after `/load`) cannot catch it, since it would be verifying the wrong configuration.

**Tests first.** Table-driven tests of the scanner; `Validate` and `Apply` against fakes (refused, absolute accepted,
nothing written or loaded); the API mapping; the `doctor` checks; and the real-Caddy test above, which turns from
showing the loss to showing the refusal, plus one that applies a change to a Caddyfile with an absolute import and sees
the imported site still served.

**Also decided here: write order.** Today the file is written before `/load` and restored if Caddy refuses it. The
reference writes the file only after Caddy accepted the content, so a crash can never leave a file Caddy did not
accept. Our order stays for now: B2 detects and resolves a crash at start, and writing after `/load` would need Caddy to
be given the text and the file to be written afterwards, with the same refusal risks. It can be revisited after B2.

## B2. Revisions left unfinished by a crash

**Why.** A revision is `pending` or `loading` while an apply runs. If the process dies then, the revision stays that
way for ever; `Reconcile` only compares the file with the newest applied revision. The reference has a state for this
and recovers at start.

**Plan.** `Manager.Recover(ctx)`, run by `serve` before `Reconcile`, resolves every unfinished revision:
- `pending` (nothing touched the disk): `failed`, with "interrupted before it was applied".
- `loading` and the file on disk differs from the revision: `failed`, "interrupted; the file was not changed".
- `loading` and the file equals the revision: compare the running Caddy with the adapted file (B3's fingerprint).
  Equal: `applied`. Different or unknown: `recovery-required`, "the file was written but Caddy may not run it".
The store needs a way to list unfinished revisions (a contract-suite method). Recovery never reloads Caddy by itself;
it only records what is true and logs it.

**Tests first.** Each crash point is a stored revision plus a file state, with fakes for Caddy: one table-driven test
of `Recover`; a store contract test for the listing; a `serve` test that starts with a leftover `loading` revision.

**Done.** `Manager.Recover`, `store.Unfinished` (both stores, in the contract suite) and the call in `serve --allow-edit`.
A real Caddy tells the two crash cases apart: file written and loaded (resolved `applied`), file written and not loaded
(`recovery-required`). The listing is a store method rather than a filter on `Revisions`, because an unfinished
revision could be older than any page that is read.

## B3. Verify after `/load`, and check drift against the running Caddy

**Why.** Today a `200` from `/load` is trusted, and drift is only "the file differs from the last applied revision".
The reference fingerprints the running configuration (`GET /config/`, canonical JSON, SHA-256) and compares it with
the adapted candidate, which proves that Caddy runs what was applied and also reveals a reload made by hand.

**Plan.**
1. Spike, as an integration test against Caddy 2.11: does the fingerprint of `GET /config/` equal that of the `/adapt`
   result for the same text, always? The reference says yes for 2.7.6 and 2.11.7, but Caddy may normalise things.
2. If it is stable: after a successful `/load`, verify; a mismatch is treated like a refused load (restore). Add
   `RunningMatches` to `Drift`, and show it in `GET /api/caddyfile`.
3. If it is not stable: only log the mismatch, never act on it, and keep drift to the file comparison.

**Tests first.** The spike; then fake-Caddy unit tests of verify, mismatch and unreachable Caddy; the real-Caddy
integration test of a reload made behind the manager's back.

**Spike result and what was done.** With Caddy 2.11.7 the configuration from `GET /config/` is identical (as JSON
with sorted keys) to the `/adapt` result of the same text: at start, after a load by hand, and after every apply of the
integration tests, which now verify. So the stable branch was taken: after `/load`, `Apply` reads back what Caddy runs
and a difference, or a Caddy that cannot be read, is handled like a refused load (`ErrNotRunning` inside
`ErrApplyFailed`, the previous file restored). `Reconcile` gained `Running` (`RunMatches`, `RunDiffers`, `RunUnknown`);
it is `RunUnknown` when Caddy is unreachable, the file cannot be adapted, or the file has a relative import (adapting its
text from here would resolve the import from the wrong directory). `serve` warns on `RunDiffers`. Showing it in the API
is left to B5, with the rest of the status. Only one Caddy version was tried; if another normalises differently, the
symptom is an apply refused with `ErrNotRunning`, and the check can be relaxed to a warning.

## B4. One apply at a time across processes

**Why.** The mutex is inside one process. Two `caddy-view` processes on one Caddyfile can both pass the stale-base
check and then both write. The reference holds an exclusive lock for the life of the process.

**Plan.** An exclusive SQLite transaction on a small lock file, as the reference does: the kernel drops it when the
process dies, so there is no stale lock, no PID file and no timeout. It reuses the SQLite driver already in the module
(no new dependency) and works on Windows too. `serve --allow-edit` takes it at start and exits with code 1 and a clear
message when another process holds it. Open question: where the file lives. Next to the Caddyfile (`.caddy-view.lock`)
covers every process that edits that Caddyfile and needs only the directory the atomic write already needs; in the data
directory it would cover only processes sharing that directory. The first is proposed.

**Tests first.** Two lockers on one path (the second is refused, and succeeds after the first releases); a lock held by
a process that was killed is free; a `serve` test that refuses to start while the lock is held.

**Done.** `internal/instancelock`, with the lock in `.caddy-view.lock` next to the Caddyfile as proposed. The test binary
runs itself as the holding process, so "held by another process" and "freed when that process is killed" are tested for
real, on Windows here and on Linux and macOS in CI. `store.SQLiteURI` is the one place that turns a path into a SQLite
URI, shared with the history store. It refuses a symbolic link at the lock path. `serve --allow-edit` exits with code 1
and names the Caddyfile when the lock is taken; without `--allow-edit` nothing is locked.

## B5. A system status API (phase 4, slice 4a; done)

**Why.** The PLAN's Config tab shows database usage, and the reference has cards for Caddy and for ingestion. The data
exists (cursor with skipped lines and gaps, store size, schema version, retention policy, scraper state) but no route
returns it.

**Plan.** `GET /api/system`: Caddy (reachable, endpoint), ingestion (file, offset, skipped lines, gaps, newest entry
time), scraper (last success, last error, whether `metrics` is on), database (size, schema version, retention), editing
(enabled, Caddyfile path, drift, lock). It is written before the UI that shows it, as for the history routes.

**Tests first.** Handler tests with fakes of each source, as in `history_test.go`.

## B6. The options of a site (phase 4 design)

**Why.** The PLAN says only "options (JSON)". The reference's model gives the checklist: proxy or redirect site (with
the redirect code); TLS automatic or off; enabled; "skip TLS verification" for an `https://` upstream; basic auth with
a bcrypt hash (the password is never stored); an access-log snippet imported per site; strict hostname and upstream
validation. Multiple upstreams with load balancing is ours, not theirs.

**Plan.** `docs/phase-4.md` (proposed, to review) decides which of these are in v1, and each decision names its generated Caddyfile text. Basic
auth would add `golang.org/x/crypto` for bcrypt: a dependency that needs its reason written down, as `CLAUDE.md` asks.

## B7. Health route and `healthcheck` command (phase 5)

**Why.** A Docker `HEALTHCHECK` needs something to call, and a minimal image has no `curl`.

**Plan.** `GET /api/health`, reachable without a login once there is one, answering `{"status":"ok"}` while the store
answers; and a `caddy-view healthcheck` command that calls it and sets the exit code, for the `HEALTHCHECK` line.

## B8. `doctor` checks permissions (phase 5, cheap enough to pull earlier)

**Why.** The reference documents the failures people actually hit: an access log Caddy created with a mode the manager
cannot read, an admin socket without write permission, a Caddyfile directory the manager cannot write.

**Plan.** New checks: the access log file is readable; the unix admin socket exists and is writable; with `--caddyfile`,
the file and its directory are writable (the atomic write needs the directory); relative `import`s (B1).

## Decided not to do

- **Certificates near expiry on the Dashboard.** Caddy obtains, renews and validates its certificates by itself, and the
  Admin API has no simple endpoint for their expiry. The Dashboard of v1 does not show it; it was removed from the PLAN.
  It can come back later if a reason appears (reading the certificate files from Caddy's storage, or probing each site
  over TLS, were the two ways considered).
- **A test that guards the README against drift.** The README will be rewritten for an open source audience (more
  didactic, with pictures) before the first release, so until then it is kept up to date by hand, and the rewrite is a
  task of the release (phase 5), not of this backlog.
