-- Current segment membership, mirrored from segment.membership, so batch queries can evaluate
-- "in segment X" rules and static lists. The latest version per (segment, contact) wins.
CREATE TABLE IF NOT EXISTS segment_members
(
    tenant_id  UInt64,
    segment_id UInt64,
    contact_id UInt32,
    member     UInt8,
    version    UInt64
)
ENGINE = ReplacingMergeTree(version)
ORDER BY (tenant_id, segment_id, contact_id);
