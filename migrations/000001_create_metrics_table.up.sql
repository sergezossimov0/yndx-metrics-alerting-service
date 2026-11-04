-- Gauges and counters live in one table keyed by (id, mtype), so the same
-- name may exist as both a gauge and a counter. The CHECK constraint
-- guarantees exactly one of delta/value is set for the row's type.
-- IF NOT EXISTS keeps the migration safe for databases where the table was
-- already created by the server before migrations were introduced.
CREATE TABLE IF NOT EXISTS metrics (
    id    TEXT             NOT NULL,
    mtype TEXT             NOT NULL CHECK (mtype IN ('gauge', 'counter')),
    delta BIGINT,
    value DOUBLE PRECISION,
    PRIMARY KEY (id, mtype),
    CHECK (
        (mtype = 'gauge'   AND value IS NOT NULL AND delta IS NULL) OR
        (mtype = 'counter' AND delta IS NOT NULL AND value IS NULL)
    )
);
