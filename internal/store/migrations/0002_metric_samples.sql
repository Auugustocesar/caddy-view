CREATE TABLE metric_samples (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    name   TEXT    NOT NULL,
    labels TEXT    NOT NULL, -- canonical label string, opaque to the store
    ts     INTEGER NOT NULL, -- Unix milliseconds, UTC
    value  REAL    NOT NULL, -- the increase for a counter, the reading for a gauge
    raw    REAL    NOT NULL  -- the counter itself, or the reading for a gauge
);

CREATE INDEX metric_samples_series_ts ON metric_samples (name, labels, ts);
CREATE INDEX metric_samples_ts ON metric_samples (ts);
