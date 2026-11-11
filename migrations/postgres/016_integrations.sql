-- Batch 8a: integrations (CRM, Shopify, Zapier-style REST hooks, ad audiences) and audience sync.

-- One connector of a workspace. config holds settings that may be shown (base URLs, a shop
-- domain, an ad account); secrets is a JSON object sealed with the server key and never returned
-- in full. mapping is the field mapping [{remote, attr, dir}] of CRM connectors.
CREATE TABLE integrations (
    id           bigserial PRIMARY KEY,
    tenant_id    bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    kind         text        NOT NULL, -- hubspot | salesforce | shopify | rest_hooks | facebook_audiences | google_ads
    name         text        NOT NULL,
    config       jsonb       NOT NULL DEFAULT '{}',
    secrets      bytea,
    mapping      jsonb       NOT NULL DEFAULT '[]',
    active       boolean     NOT NULL DEFAULT true,
    -- scheduled pulls (CRM connectors)
    sync_minutes integer     NOT NULL DEFAULT 60, -- 0: never pull
    next_sync_at timestamptz,
    cursor       text        NOT NULL DEFAULT '', -- the last remote modification time pulled
    -- status for the UI
    status       text        NOT NULL DEFAULT 'new', -- new | ok | error
    last_error   text        NOT NULL DEFAULT '',
    last_sync_at timestamptz,
    -- REST hooks: the connector's own API key (SHA-256) and its last characters for display
    key_hash     text UNIQUE,
    key_hint     text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX integrations_tenant ON integrations (tenant_id);
CREATE INDEX integrations_due ON integrations (next_sync_at) WHERE active AND sync_minutes > 0;

-- What each connector did: pulls, pushes, inbound webhooks, audience uploads, tests. Pruned to a week.
CREATE TABLE integration_log (
    id             bigserial PRIMARY KEY,
    integration_id bigint      NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    at             timestamptz NOT NULL DEFAULT now(),
    action         text        NOT NULL, -- pull | push | webhook | audience | reconcile | test
    ok             boolean     NOT NULL,
    count          integer     NOT NULL DEFAULT 0,
    message        text        NOT NULL DEFAULT ''
);
CREATE INDEX integration_log_conn ON integration_log (integration_id, id DESC);
CREATE INDEX integration_log_age ON integration_log (at);

-- REST hook subscriptions (Zapier, Make) are outbound webhooks owned by a connector. A hook can
-- narrow segment_entered / segment_exited to one segment and form_submitted to one form.
ALTER TABLE webhooks ADD COLUMN integration_id bigint REFERENCES integrations (id) ON DELETE CASCADE;
ALTER TABLE webhooks ADD COLUMN segment_id bigint NOT NULL DEFAULT 0;
ALTER TABLE webhooks ADD COLUMN form_id bigint NOT NULL DEFAULT 0;

-- A segment linked to an ad platform audience (Facebook Custom Audience, Google Ads Customer Match list).
CREATE TABLE audience_syncs (
    id             bigserial PRIMARY KEY,
    tenant_id      bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    integration_id bigint      NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    segment_id     bigint      NOT NULL,
    audience_id    text        NOT NULL, -- the platform's id (Facebook) or resource name (Google)
    audience_name  text        NOT NULL DEFAULT '',
    active         boolean     NOT NULL DEFAULT true,
    status         text        NOT NULL DEFAULT 'new', -- new | syncing | ok | error
    last_error     text        NOT NULL DEFAULT '',
    uploaded       bigint      NOT NULL DEFAULT 0, -- contacts in the audience now
    added          bigint      NOT NULL DEFAULT 0, -- uploads since linking
    removed        bigint      NOT NULL DEFAULT 0,
    skipped        bigint      NOT NULL DEFAULT 0, -- members left out at the last reconcile (no consent or no email/phone)
    last_sync_at   timestamptz,
    next_full_at   timestamptz NOT NULL DEFAULT now(), -- the next full reconcile
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (integration_id, audience_id, segment_id)
);
CREATE INDEX audience_syncs_tenant ON audience_syncs (tenant_id, segment_id);

-- The identifiers uploaded for each contact, so a removal sends exactly what was added (even after
-- the contact's email changed or the contact was deleted).
CREATE TABLE audience_members (
    sync_id    bigint NOT NULL REFERENCES audience_syncs (id) ON DELETE CASCADE,
    contact_id bigint NOT NULL,
    email_hash text   NOT NULL DEFAULT '',
    phone_hash text   NOT NULL DEFAULT '',
    PRIMARY KEY (sync_id, contact_id)
);

-- Contacts whose audience membership must be checked again (segment entry or exit, consent or
-- address changes). The flusher claims rows in batches; one row per contact collapses flapping.
CREATE TABLE audience_queue (
    sync_id    bigint      NOT NULL REFERENCES audience_syncs (id) ON DELETE CASCADE,
    contact_id bigint      NOT NULL,
    queued_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (sync_id, contact_id)
);
