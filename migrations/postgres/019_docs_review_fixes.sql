-- Fixes and gaps found while writing the docs.
-- "Send test" on an outbound webhook is listed with its deliveries, marked as a test.
ALTER TABLE webhook_deliveries ADD COLUMN test boolean NOT NULL DEFAULT false;

-- Broadcasts: segments whose members are left out, and sending at the start's clock time in each
-- contact's own time zone (timezone: the zone the start was chosen in).
ALTER TABLE broadcasts ADD COLUMN exclude_segment_ids bigint[] NOT NULL DEFAULT '{}';
ALTER TABLE broadcasts ADD COLUMN local_time boolean NOT NULL DEFAULT false;
ALTER TABLE broadcasts ADD COLUMN timezone text NOT NULL DEFAULT '';

-- Journeys that have run can be archived (hidden from the list, enrollments kept) instead of deleted.
ALTER TABLE journeys ADD COLUMN archived_at timestamptz;
