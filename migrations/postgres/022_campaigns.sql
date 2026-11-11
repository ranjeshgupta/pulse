-- M3, campaigns.

-- Free tags on broadcasts and journeys (the Campaigns hub filters by them).
ALTER TABLE broadcasts ADD COLUMN tags text[] NOT NULL DEFAULT '{}';
ALTER TABLE journeys ADD COLUMN tags text[] NOT NULL DEFAULT '{}';

-- Extra audience segments of a broadcast (members of any of them, as well as segment_id's).
ALTER TABLE broadcasts ADD COLUMN include_segment_ids bigint[] NOT NULL DEFAULT '{}';

-- Send deadline of a broadcast: {"at": "HH:MM", "hours": N, "timezone": "..."}; messages that would
-- go out later are dropped as <channel>_failed with reason "deadline".
ALTER TABLE broadcasts ADD COLUMN deadline jsonb;

-- A template written inside a one-time campaign: hidden from template lists and pickers until it is
-- saved as a reusable template.
ALTER TABLE templates ADD COLUMN hidden boolean NOT NULL DEFAULT false;

-- Per-provider sending limits: {"rate_per_second": N, "daily_quota": N, "timezone": "..."}.
ALTER TABLE channel_providers ADD COLUMN limits jsonb;

-- Saved test contacts: who "Send a test" can send to (an existing contact, or bare addresses).
CREATE TABLE test_contacts (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name        text        NOT NULL,
    external_id text        NOT NULL DEFAULT '',
    email       text        NOT NULL DEFAULT '',
    phone       text        NOT NULL DEFAULT '',
    push_token  text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX test_contacts_tenant ON test_contacts (tenant_id, id);
