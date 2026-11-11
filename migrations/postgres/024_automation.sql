-- M5, Settings → Automation & limits (package automation): retention per log type and per event
-- name, retries, schedules, inbox expiry and default provider limits, as one JSON document.
-- Fields missing from it take their defaults (automation.Defaults), which equal what Pulse did
-- before, so '{}' means "unchanged behaviour".
ALTER TABLE tenant_settings ADD COLUMN IF NOT EXISTS automation jsonb NOT NULL DEFAULT '{}';

-- Retention now lives in automation.retention. Carry the Data & privacy settings over; message
-- events and page views were deleted with the other events, so they keep following event_days
-- (message_event_days and web_view_days are left out: null means "same as events").
UPDATE tenant_settings SET automation = jsonb_build_object('retention', jsonb_build_object(
        'anonymous_days', coalesce((retention->>'anonymous_days')::int, 0),
        'event_days', coalesce((retention->>'event_days')::int, 0),
        'audit_days', coalesce((retention->>'audit_days')::int, 365)))
WHERE automation = '{}';

-- When each workspace's daily cleanup last ran (one run per workspace per day, see maintenance).
CREATE TABLE IF NOT EXISTS retention_runs (
    tenant_id bigint PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    ran_at    timestamptz NOT NULL
);

-- Retention deletes of Postgres logs by workspace and age (webhook_deliveries and audit_log have
-- theirs: (webhook_id, at), (tenant_id, at)).
CREATE INDEX IF NOT EXISTS imports_tenant_created ON imports (tenant_id, created_at);
CREATE INDEX IF NOT EXISTS sandbox_messages_tenant_created ON sandbox_messages (tenant_id, created_at);

-- Workspaces that never saved any setting had no tenant_settings row, so the old cleanup never
-- pruned their audit log (only rows were read). Keep that: they get a row saying "keep the audit
-- log forever"; workspaces created from now on start with the 365-day default the app has always
-- shown.
INSERT INTO tenant_settings (tenant_id, automation)
SELECT id, '{"retention": {"audit_days": 0}}'::jsonb FROM tenants
ON CONFLICT (tenant_id) DO NOTHING;
