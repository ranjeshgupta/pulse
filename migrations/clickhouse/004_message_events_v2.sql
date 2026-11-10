-- message_events_mv v2: also the app inbox (inbox_sent, inbox_opened, inbox_clicked) and email
-- bounces. A materialized view's query can't be changed in place, so v1 is replaced.
DROP VIEW IF EXISTS message_events_mv;

CREATE MATERIALIZED VIEW IF NOT EXISTS message_events_mv2 TO message_events AS
SELECT
    tenant_id,
    multiIf(JSONExtractUInt(props, 'broadcast_id') > 0, 'broadcast', JSONExtractUInt(props, 'journey_id') > 0, 'journey', 'other') AS source,
    if(JSONExtractUInt(props, 'broadcast_id') > 0, JSONExtractUInt(props, 'broadcast_id'), JSONExtractUInt(props, 'journey_id')) AS source_id,
    JSONExtractUInt(props, 'run') AS run,
    JSONExtractString(props, 'variant') AS variant,
    if(JSONExtractUInt(props, 'broadcast_id') = 0 AND JSONExtractUInt(props, 'journey_id') > 0,
       arrayElement(splitByChar(':', JSONExtractString(props, 'message_id')), 2), '') AS node,
    if(name = 'unsubscribed', JSONExtractString(props, 'channel'), splitByChar('_', name)[1]) AS channel,
    if(name = 'unsubscribed', 'unsubscribed', splitByChar('_', name)[2]) AS kind,
    contact_id,
    ts,
    JSONExtractString(props, 'message_id') AS message_id,
    JSONExtractString(props, 'url') AS url,
    id
FROM events
WHERE JSONHas(props, 'message_id')
  AND (match(name, '^(email|sms|whatsapp|rcs|push|inbox)_(sent|failed|delivered|read|fallback|opened|clicked|suppressed|capped|deferred|bounced)$')
       OR name = 'unsubscribed');
