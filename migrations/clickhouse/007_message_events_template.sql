-- Batch O4, template reports: message_events gets the template and the provider each message
-- went through (props.template_id, props.provider_id), so a template's outcomes can be read
-- across every campaign without scanning the event log. Provider ids exist from O4 on (0 before).

ALTER TABLE message_events ADD COLUMN IF NOT EXISTS template_id UInt64 DEFAULT 0;
ALTER TABLE message_events ADD COLUMN IF NOT EXISTS provider_id UInt64 DEFAULT 0;
ALTER TABLE message_events ADD INDEX IF NOT EXISTS idx_template template_id TYPE set(0) GRANULARITY 4;
-- (new parts get the index; older parts are read without it)

-- The view gains the two columns: the new one starts before the old one stops (a row seen by both
-- is the same row twice, which ReplacingMergeTree and uniqExact absorb).
CREATE MATERIALIZED VIEW IF NOT EXISTS message_events_mv4 TO message_events AS
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
    id,
    JSONExtractString(props, 'reason_code') AS reason_code,
    JSONExtractUInt(props, 'template_id') AS template_id,
    JSONExtractUInt(props, 'provider_id') AS provider_id
FROM events
WHERE JSONHas(props, 'message_id')
  AND (match(name, '^(email|sms|whatsapp|rcs|push|inbox)_(sent|failed|delivered|read|fallback|opened|clicked|suppressed|capped|deferred|bounced|complained)$')
       OR name = 'unsubscribed');

DROP VIEW IF EXISTS message_events_mv3;

-- One-time backfill of the template of rows recorded before (skipped once any row has one): the
-- same rows again with the column filled; the template-less copies only ever count as template 0.
INSERT INTO message_events (tenant_id, source, source_id, run, variant, node, channel, kind, contact_id, ts, message_id, url, id, reason_code, template_id, provider_id)
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
    contact_id, ts,
    JSONExtractString(props, 'message_id'),
    JSONExtractString(props, 'url'),
    id,
    JSONExtractString(props, 'reason_code'),
    JSONExtractUInt(props, 'template_id'),
    JSONExtractUInt(props, 'provider_id')
FROM events
WHERE JSONHas(props, 'message_id') AND JSONExtractUInt(props, 'template_id') > 0
  AND (match(name, '^(email|sms|whatsapp|rcs|push|inbox)_(sent|failed|delivered|read|fallback|opened|clicked|suppressed|capped|deferred|bounced|complained)$')
       OR name = 'unsubscribed')
  AND (SELECT count() FROM message_events WHERE template_id > 0) = 0;
