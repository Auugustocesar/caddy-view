# caddy-view

Lightweight web UI for Caddy (Go backend, Svelte frontend, SQLite). Full design in `docs/PLAN.md`.

## Rules

- **Everything in the repository is in English**: code, comments, docs, commit messages, UI text. (Conversation
  with the maintainer may be in Portuguese.)
- **Tests first, always.** For any feature, fix or improvement: write the tests from the requirement, run them and
  confirm they fail for the right reason, then write the code. Never write tests after the fact just to match
  existing code.
- Do not commit or push unless asked.
- Caddy is the source of truth; never leave it with an invalid config (validate with `/adapt` before `/load`).
- Keep `docs/PLAN.md` updated when a design decision changes.
