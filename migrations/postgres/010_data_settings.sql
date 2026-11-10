-- Workspace data settings.
-- tracking: {"anonymous": true, "geoip": true}  - accept anonymous visitors; locate visitors by IP.
-- retention: {"anonymous_days": 0, "event_days": 0, "audit_days": 365} - 0 keeps forever.
ALTER TABLE tenant_settings ADD COLUMN tracking jsonb NOT NULL DEFAULT '{"anonymous": true, "geoip": true}';
ALTER TABLE tenant_settings ADD COLUMN retention jsonb NOT NULL DEFAULT '{"anonymous_days": 0, "event_days": 0, "audit_days": 365}';

-- Finding idle anonymous visitors for cleanup.
CREATE INDEX contacts_anonymous_idle ON contacts (tenant_id, updated_at) WHERE external_id LIKE 'anon-%';
