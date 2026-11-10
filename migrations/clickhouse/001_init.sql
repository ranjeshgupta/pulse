-- Immutable event log. The sort key puts (tenant, event name) first, so a segment rule like
-- "did purchase >= 3 times in 7 days" reads one narrow key range. ReplacingMergeTree with id in the key
-- collapses redelivered rows; segment queries also count uniqExact(id), so duplicates never inflate counts.
CREATE TABLE IF NOT EXISTS events
(
    tenant_id   UInt64,
    id          String,
    name        LowCardinality(String),
    contact_id  UInt32,
    ts          DateTime64(3, 'UTC'),
    props       String CODEC(ZSTD(3)),
    inserted_at DateTime DEFAULT now()
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, name, contact_id, ts, id);

-- Latest profile snapshot per contact, for batch segment evaluation. Query with FINAL.
CREATE TABLE IF NOT EXISTS contacts
(
    tenant_id   UInt64,
    id          UInt32,
    external_id String,
    attrs       Map(String, String),
    version     UInt64
)
ENGINE = ReplacingMergeTree(version)
ORDER BY (tenant_id, id);
