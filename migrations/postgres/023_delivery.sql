-- M4, delivery truth.

-- What each email service's webhooks are verified with (bounces.Keys: SendGrid's verification key,
-- Mailgun's signing key, basic auth for Postmark / SparkPost / generic posts), sealed with the
-- server key.
ALTER TABLE tenants ADD COLUMN email_webhook_keys bytea;

-- The audience of a broadcast run when it started: reach.Counts (audience, after exclusions, after
-- consent, reachable) for the broadcast's channel, for the report's funnel.
ALTER TABLE broadcast_runs ADD COLUMN audience jsonb;

-- The contact page lists a contact's journeys (current and past).
CREATE INDEX IF NOT EXISTS journey_enrollments_tenant_contact ON journey_enrollments (tenant_id, contact_id, updated_at DESC);
