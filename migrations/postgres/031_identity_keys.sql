-- Identity keys (Settings → Contacts → Identity): each contact's normalised value of every key the
-- workspace identifies people by (email, phone, user name…), so an incoming event finds the
-- contact sharing a value with one index lookup instead of searching attributes. Several contacts
-- may share a value (keys that only suggest duplicates, or values from before a key was added).
CREATE TABLE IF NOT EXISTS contact_keys (
    tenant_id  bigint NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    key        text   NOT NULL,
    value      text   NOT NULL,
    contact_id bigint NOT NULL,
    PRIMARY KEY (tenant_id, key, value, contact_id)
);
CREATE INDEX IF NOT EXISTS contact_keys_contact ON contact_keys (tenant_id, contact_id);

-- Filling contact_keys for a key added to a workspace's settings, in id order, a chunk at a time.
-- start_after gives the event pipeline time to pick up the new key before the backfill passes.
CREATE TABLE IF NOT EXISTS identity_backfills (
    tenant_id   bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    key         text        NOT NULL,
    next_id     bigint      NOT NULL DEFAULT 0,
    start_after timestamptz NOT NULL DEFAULT now() + interval '40 seconds',
    done_at     timestamptz,
    PRIMARY KEY (tenant_id, key)
);
