# caddy-view

Lightweight web UI for Caddy (Go backend, Svelte frontend, SQLite). Full design in `docs/PLAN.md`.

## Rules

- **Everything in the repository is in English**: code, comments, docs, commit messages, UI text. (Conversation
  with the maintainer may be in Portuguese.)
- **Tests first, always.** For any feature, fix or improvement: write the tests from the requirement, run them and
  confirm they fail for the right reason, then write the code. Never write tests after the fact just to match
  existing code.
- **Always LF line endings**, also on Windows (enforced by `.gitattributes`). Never write CRLF.
- Do not commit or push unless asked.
- The UI build in `internal/web/dist` is committed: after changing `web/`, run `npm test` and `npm run build` in `web/`
  and keep the result in the same commit (CI checks it).
- Caddy is the source of truth; never leave it with an invalid config (validate with `/adapt` before `/load`).
- Keep `docs/PLAN.md` updated when a design decision changes.

## Go quality standards

Write and review code as a senior Go engineer would. Maintainability beats cleverness.

- **Idiomatic Go**: follow Effective Go and the Go Code Review Comments. Small, cohesive packages. Interfaces are
  defined by the consumer, kept small, and accept interfaces / return concrete types. `context.Context` is the
  first parameter of anything that does I/O. No global mutable state, no `init()` side effects, no panics for
  control flow.
- **Errors**: wrap with `%w` (use two `%w` to keep both a sentinel and the cause), check with `errors.Is` / `errors.As`,
  prefer sentinel or typed errors over matching message text, lowercase messages without trailing punctuation.
  Never ignore an error silently; if it is deliberately ignored, say why in a comment.
- **Dependencies**: standard library first. Every external dependency needs a reason.
- **Logging**: `log/slog`, injected, never `fmt.Print` in library code.
- **Tests**: table-driven, `t.Helper()`, `t.Cleanup()`, fakes of consumer-side interfaces, no `time.Sleep` for
  synchronization, no assertions on incidental wording. Error paths are tested as carefully as happy paths.
- **No duplication**: a helper needed in a second package moves to one shared place.
- **Docs**: every exported identifier has a doc comment that starts with its name.
- **Gates (all must be clean before a commit)**: `gofmt -s -l .`, `go vet ./...`, `go test ./...`, `go mod tidy -diff`.
  CI also runs staticcheck and `govulncheck`. Keep the `go` directive on a patched Go release.
