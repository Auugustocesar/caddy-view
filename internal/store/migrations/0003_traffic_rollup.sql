-- Traffic rollups: one row per resolution (1 minute, 2 hour), bucket start, host and status class. They are written
-- by the same transaction as the log entries they sum, so they always match them.
CREATE TABLE traffic_rollup (
    resolution      INTEGER NOT NULL, -- 1 per minute, 2 per hour
    bucket          INTEGER NOT NULL, -- start of the bucket, Unix milliseconds, UTC
    host            TEXT    NOT NULL,
    class           INTEGER NOT NULL, -- 1 to 5 for 1xx to 5xx, 0 for anything else
    requests        INTEGER NOT NULL,
    bytes           INTEGER NOT NULL,
    duration_sum_us INTEGER NOT NULL,
    duration_max_us INTEGER NOT NULL,
    lat0            INTEGER NOT NULL,
    lat1            INTEGER NOT NULL,
    lat2            INTEGER NOT NULL,
    lat3            INTEGER NOT NULL,
    lat4            INTEGER NOT NULL,
    lat5            INTEGER NOT NULL,
    lat6            INTEGER NOT NULL,
    lat7            INTEGER NOT NULL,
    lat8            INTEGER NOT NULL,
    lat9            INTEGER NOT NULL,
    lat10            INTEGER NOT NULL,
    lat11            INTEGER NOT NULL,
    PRIMARY KEY (resolution, bucket, host, class)
) WITHOUT ROWID;
