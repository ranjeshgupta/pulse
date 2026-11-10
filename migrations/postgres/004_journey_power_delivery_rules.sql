-- Re-entry: a contact may go through a journey more than once (per the journey's re-entry rule),
-- but only one enrollment per contact can be live at a time.
ALTER TABLE journey_enrollments DROP CONSTRAINT journey_enrollments_journey_id_contact_id_key;
CREATE UNIQUE INDEX journey_enrollments_live ON journey_enrollments (journey_id, contact_id)
    WHERE status IN ('active', 'waiting');
CREATE INDEX journey_enrollments_contact ON journey_enrollments (journey_id, contact_id, created_at DESC);
CREATE INDEX journey_enrollments_live_by_contact ON journey_enrollments (tenant_id, contact_id)
    WHERE status IN ('active', 'waiting');

-- A timer remembers which wait it belongs to, so a resume only happens if the enrollment is still
-- parked there (an event may have resumed it first).
ALTER TABLE journey_timers ADD COLUMN wait_node text;

-- "Wait for event" steps: when the contact does the event, resume at yes_node and drop the timeout.
CREATE TABLE journey_event_waits (
    enrollment_id bigint PRIMARY KEY REFERENCES journey_enrollments (id) ON DELETE CASCADE,
    tenant_id     bigint NOT NULL,
    contact_id    bigint NOT NULL,
    event         text   NOT NULL,
    wait_node     text   NOT NULL,
    yes_node      text   NOT NULL
);
CREATE INDEX journey_event_waits_lookup ON journey_event_waits (tenant_id, contact_id, event);

-- Templates marked transactional (OTPs, receipts) skip frequency caps and quiet hours.
ALTER TABLE templates ADD COLUMN transactional boolean NOT NULL DEFAULT false;

-- Workspace delivery rules: frequency caps and quiet hours.
CREATE TABLE tenant_settings (
    tenant_id  bigint PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    delivery   jsonb       NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now()
);
