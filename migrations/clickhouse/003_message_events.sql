-- Message outcomes and engagement (sent, delivered, opened, clicked, unsubscribed, ...) of broadcasts
-- and journeys, keyed for reports: one broadcast's or journey's rows are one narrow key range.
-- A materialized view fills it from the event log as events arrive.
--   source: broadcast | journey | other (test sends);  node: the journey step that sent the message
--   run: the broadcast run (unix seconds of its scheduled time);  variant: the A/B variant label
CREATE TABLE IF NOT EXISTS message_events
(
    tenant_id  UInt64,
    source     LowCardinality(String),
    source_id  UInt64,
    run        UInt64,
    variant    LowCardinality(String),
    node       String,
    channel    LowCardinality(String),
    kind       LowCardinality(String),
    contact_id UInt32,
    ts         DateTime64(3, 'UTC'),
    message_id String,
    url        String,
    id         String
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, source, source_id, kind, contact_id, id);

-- The materialized view that fills it is in 007_message_events_template.sql (message_events_mv4;
-- the earlier message_events_mv and _mv2 are dropped there).

-- One-time backfill of events recorded before this table existed (skipped once it has rows).
INSERT INTO message_events (tenant_id, source, source_id, run, variant, node, channel, kind, contact_id, ts, message_id, url, id)
SELECT
    tenant_id,
    multiIf(JSONExtractUInt(props, 'broadcast_id') > 0, 'broadcast', JSONExtractUInt(props, 'journey_id') > 0, 'journey', 'other'),
    if(JSONExtractUInt(props, 'broadcast_id') > 0, JSONExtractUInt(props, 'broadcast_id'), JSONExtractUInt(props, 'journey_id')),
    JSONExtractUInt(props, 'run'),
    JSONExtractString(props, 'variant'),
    if(JSONExtractUInt(props, 'broadcast_id') = 0 AND JSONExtractUInt(props, 'journey_id') > 0,
       arrayElement(splitByChar(':', JSONExtractString(props, 'message_id')), 2), ''),
    if(name = 'unsubscribed', JSONExtractString(props, 'channel'), splitByChar('_', name)[1]),
    if(name = 'unsubscribed', 'unsubscribed', splitByChar('_', name)[2]),
    contact_id, ts, JSONExtractString(props, 'message_id'), JSONExtractString(props, 'url'), id
FROM events
WHERE JSONHas(props, 'message_id')
  AND (match(name, '^(email|sms|whatsapp|rcs|push)_(sent|failed|delivered|read|fallback|opened|clicked|suppressed|capped|deferred)$')
       OR name = 'unsubscribed')
  AND (SELECT count() FROM message_events) = 0;
