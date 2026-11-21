-- M4, delivery truth and dashboards.

-- message_events v3: the normalised failure reason (props.reason_code, package reasons) and spam
-- complaints (email_complained). Linked bounces, complaints and email_delivered now carry
-- message_id, so they land here too. Rows recorded before M4 have reason_code ''.
ALTER TABLE message_events ADD COLUMN IF NOT EXISTS reason_code LowCardinality(String) DEFAULT '';

DROP VIEW IF EXISTS message_events_mv2;

-- message_events_mv3 lived here; 007_message_events_template.sql replaces it with message_events_mv4
-- (migrations run on every start, so it is no longer created here).

-- Daily active users, for DAU / WAU / MAU at any size: one uniq state per (workspace, UTC day,
-- platform, source). WAU and MAU merge 7 and 30 days of states. Active means an event the person
-- did (page and screen views, purchases, form submissions, server events about their actions), not
-- Pulse's own message, bookkeeping or profile-only (identify) events: analytics.ActiveCondition.
CREATE TABLE IF NOT EXISTS active_users_daily
(
    tenant_id UInt64,
    day       Date,
    platform  LowCardinality(String),
    source    LowCardinality(String),
    users     AggregateFunction(uniq, UInt32)
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (tenant_id, day, platform, source);

-- Each contact's first active day (new vs returning users).
CREATE TABLE IF NOT EXISTS first_active
(
    tenant_id  UInt64,
    contact_id UInt32,
    first      SimpleAggregateFunction(min, Date)
)
ENGINE = AggregatingMergeTree
ORDER BY (tenant_id, contact_id);

-- One-time backfill of the last 400 days (skipped once the tables have rows), before the views
-- start filling them.
INSERT INTO active_users_daily
SELECT tenant_id, toDate(ts, 'UTC') AS day,
    multiIf(JSONExtractString(props, 'platform') IN ('web', 'android', 'ios'), JSONExtractString(props, 'platform'), 'other') AS platform,
    substring(lower(JSONExtractString(props, 'utm_source')), 1, 64) AS source,
    uniqState(contact_id)
FROM events
WHERE contact_id > 0 AND name NOT IN ('identify', 'tag_added', 'tag_removed', 'points_changed', 'list_joined', 'list_left', 'contact_merged', 'contact_merged_into', 'merge_requested', 'stage_changed', 'owner_changed', 'company_linked', 'company_unlinked', 'company_updated', 'journey_entered', 'journey_completed', 'journey_split', 'journey_change', 'journey_jump_limit', 'dwc_requested', 'webhook_failed', 'unsubscribed', 'resubscribed', 'message_skipped', 'message_unreachable') AND NOT match(name, '^(email|sms|whatsapp|rcs|push|inbox|notification)_')
  AND ts >= now() - INTERVAL 400 DAY
  AND (SELECT count() FROM active_users_daily) = 0
GROUP BY tenant_id, day, platform, source;

INSERT INTO first_active
SELECT tenant_id, contact_id, min(toDate(ts, 'UTC'))
FROM events
WHERE contact_id > 0 AND name NOT IN ('identify', 'tag_added', 'tag_removed', 'points_changed', 'list_joined', 'list_left', 'contact_merged', 'contact_merged_into', 'merge_requested', 'stage_changed', 'owner_changed', 'company_linked', 'company_unlinked', 'company_updated', 'journey_entered', 'journey_completed', 'journey_split', 'journey_change', 'journey_jump_limit', 'dwc_requested', 'webhook_failed', 'unsubscribed', 'resubscribed', 'message_skipped', 'message_unreachable') AND NOT match(name, '^(email|sms|whatsapp|rcs|push|inbox|notification)_')
  AND (SELECT count() FROM first_active) = 0
GROUP BY tenant_id, contact_id;

CREATE MATERIALIZED VIEW IF NOT EXISTS active_users_daily_mv TO active_users_daily AS
SELECT tenant_id, toDate(ts, 'UTC') AS day,
    multiIf(JSONExtractString(props, 'platform') IN ('web', 'android', 'ios'), JSONExtractString(props, 'platform'), 'other') AS platform,
    substring(lower(JSONExtractString(props, 'utm_source')), 1, 64) AS source,
    uniqState(contact_id) AS users
FROM events
WHERE contact_id > 0 AND name NOT IN ('identify', 'tag_added', 'tag_removed', 'points_changed', 'list_joined', 'list_left', 'contact_merged', 'contact_merged_into', 'merge_requested', 'stage_changed', 'owner_changed', 'company_linked', 'company_unlinked', 'company_updated', 'journey_entered', 'journey_completed', 'journey_split', 'journey_change', 'journey_jump_limit', 'dwc_requested', 'webhook_failed', 'unsubscribed', 'resubscribed', 'message_skipped', 'message_unreachable') AND NOT match(name, '^(email|sms|whatsapp|rcs|push|inbox|notification)_')
GROUP BY tenant_id, day, platform, source;

CREATE MATERIALIZED VIEW IF NOT EXISTS first_active_mv TO first_active AS
SELECT tenant_id, contact_id, min(toDate(ts, 'UTC')) AS first
FROM events
WHERE contact_id > 0 AND name NOT IN ('identify', 'tag_added', 'tag_removed', 'points_changed', 'list_joined', 'list_left', 'contact_merged', 'contact_merged_into', 'merge_requested', 'stage_changed', 'owner_changed', 'company_linked', 'company_unlinked', 'company_updated', 'journey_entered', 'journey_completed', 'journey_split', 'journey_change', 'journey_jump_limit', 'dwc_requested', 'webhook_failed', 'unsubscribed', 'resubscribed', 'message_skipped', 'message_unreachable') AND NOT match(name, '^(email|sms|whatsapp|rcs|push|inbox|notification)_')
GROUP BY tenant_id, contact_id;
