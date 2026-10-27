CREATE TABLE access_log_entries (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          INTEGER NOT NULL, -- Unix milliseconds, UTC
    host        TEXT    NOT NULL,
    method      TEXT    NOT NULL,
    path        TEXT    NOT NULL,
    status      INTEGER NOT NULL,
    duration_us INTEGER NOT NULL,
    bytes       INTEGER NOT NULL,
    client_ip   TEXT    NOT NULL
);

CREATE INDEX access_log_entries_ts ON access_log_entries (ts);
CREATE INDEX access_log_entries_host_ts ON access_log_entries (host, ts);
CREATE INDEX access_log_entries_status ON access_log_entries (status);

CREATE TABLE ingestion_cursors (
    path          TEXT    PRIMARY KEY,
    file_id       TEXT    NOT NULL,
    fingerprint   TEXT    NOT NULL,
    byte_offset   INTEGER NOT NULL,
    skipped_lines INTEGER NOT NULL,
    gaps          INTEGER NOT NULL
);
