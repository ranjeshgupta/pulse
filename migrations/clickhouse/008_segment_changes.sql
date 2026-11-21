-- Batch O5, campaign reports over time: every segment join and leave, so automation campaigns can
-- show their trigger segment's joined / left per day or hour. Filled from segment_members inserts
-- (each is a change: the pipeline publishes membership changes only; a rebuild's first fill counts
-- as joins). History starts with this migration. A redelivered change collapses on its key.
CREATE TABLE IF NOT EXISTS segment_changes
(
    tenant_id  UInt64,
    segment_id UInt64,
    contact_id UInt32,
    entered    UInt8,
    ts         DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, segment_id, ts, contact_id, entered)
TTL toDateTime(ts) + INTERVAL 400 DAY;

CREATE MATERIALIZED VIEW IF NOT EXISTS segment_changes_mv TO segment_changes AS
SELECT tenant_id, segment_id, contact_id, member AS entered, fromUnixTimestamp64Nano(toInt64(version)) AS ts
FROM segment_members;
