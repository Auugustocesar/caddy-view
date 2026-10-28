-- Caddyfile revisions: one row per attempt to change the Caddyfile, with the full content that was tried. The number
-- is never reused, so a revision keeps its identity for as long as the database lives.
CREATE TABLE config_revisions (
    number  INTEGER PRIMARY KEY AUTOINCREMENT,
    ts      INTEGER NOT NULL, -- Unix milliseconds, UTC
    cause   TEXT    NOT NULL, -- manual-edit, site-change or rollback
    status  TEXT    NOT NULL, -- pending, loading, applied, failed or recovery-required
    content TEXT    NOT NULL,
    sha256  TEXT    NOT NULL,
    error   TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX config_revisions_status ON config_revisions (status, number);
