-- Managed sites: what the user asked caddy-view to serve. The managed Caddyfile is generated from these rows. The
-- hostname is unique whatever its case, and upstreams hold a JSON array.
CREATE TABLE sites (
    id              TEXT    PRIMARY KEY,
    hostname        TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    kind            TEXT    NOT NULL CHECK (kind IN ('proxy', 'redirect')),
    upstreams       TEXT    NOT NULL,
    skip_tls_verify INTEGER NOT NULL,
    tls             TEXT    NOT NULL CHECK (tls IN ('automatic', 'internal', 'off')),
    redirect_target TEXT    NOT NULL,
    redirect_code   INTEGER NOT NULL,
    access_log      INTEGER NOT NULL,
    enabled         INTEGER NOT NULL,
    created_at      INTEGER NOT NULL, -- Unix milliseconds, UTC
    updated_at      INTEGER NOT NULL
);

-- A revision now also holds the managed file and the rows it was generated from. NULL means the revision was made
-- before there were managed sites, or without them.
ALTER TABLE config_revisions ADD COLUMN sites_content  TEXT;
ALTER TABLE config_revisions ADD COLUMN sites_sha256   TEXT;
ALTER TABLE config_revisions ADD COLUMN sites_snapshot TEXT;
