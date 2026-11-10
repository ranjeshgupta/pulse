-- Custom roles: a level (none | view | edit | publish) per area. Users hold a built-in role
-- (admin | editor | viewer) or "role:<id>".
CREATE TABLE roles (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name        text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    permissions jsonb       NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

-- Audit log: every change made in the app or through the API, and every sign-in.
CREATE TABLE audit_log (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    at          timestamptz NOT NULL DEFAULT now(),
    user_id     bigint,
    actor       text        NOT NULL, -- the user's email, "API key" or "SSO"
    action      text        NOT NULL, -- e.g. "PUT /v1/broadcasts/{id}" or "sign-in"
    object_type text        NOT NULL DEFAULT '',
    object_id   text        NOT NULL DEFAULT '',
    status      integer     NOT NULL DEFAULT 0,
    ip          text        NOT NULL DEFAULT '',
    detail      jsonb
);
CREATE INDEX audit_log_tenant_at ON audit_log (tenant_id, at DESC);

-- Outbound webhooks: events posted to the customer's URL, signed with the webhook's secret.
CREATE TABLE webhooks (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    url        text        NOT NULL,
    events     text[]      NOT NULL, -- event names; "*" for every event
    secret     bytea       NOT NULL, -- sealed with the server key
    active     boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX webhooks_tenant ON webhooks (tenant_id);

-- Recent delivery attempts, for the webhook's page. Old rows are pruned.
CREATE TABLE webhook_deliveries (
    id          bigserial PRIMARY KEY,
    webhook_id  bigint      NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    event_id    text        NOT NULL,
    event_name  text        NOT NULL,
    at          timestamptz NOT NULL DEFAULT now(),
    attempt     integer     NOT NULL,
    status      integer     NOT NULL DEFAULT 0, -- HTTP status; 0 when no response
    error       text        NOT NULL DEFAULT '',
    duration_ms integer     NOT NULL DEFAULT 0,
    final       boolean     NOT NULL DEFAULT false -- delivered, or given up
);
CREATE INDEX webhook_deliveries_hook ON webhook_deliveries (webhook_id, at DESC);

-- Background CSV imports. The upload is stored in chunks; a worker streams them through the
-- normal pipeline and reports progress.
CREATE TABLE imports (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id     bigint,
    filename    text        NOT NULL DEFAULT '',
    list_id     bigint,
    status      text        NOT NULL DEFAULT 'queued', -- queued | running | done | failed | cancelled
    bytes       bigint      NOT NULL DEFAULT 0,
    rows_done   bigint      NOT NULL DEFAULT 0,
    rows_failed bigint      NOT NULL DEFAULT 0,
    errors      jsonb       NOT NULL DEFAULT '[]', -- the first problems, for the UI
    message     text        NOT NULL DEFAULT '',
    claimed_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    started_at  timestamptz,
    finished_at timestamptz
);
CREATE INDEX imports_tenant ON imports (tenant_id, id DESC);
CREATE INDEX imports_queued ON imports (id) WHERE status IN ('queued', 'running');

CREATE TABLE import_chunks (
    import_id bigint  NOT NULL REFERENCES imports (id) ON DELETE CASCADE,
    seq       integer NOT NULL,
    data      bytea   NOT NULL,
    PRIMARY KEY (import_id, seq)
);

-- Web push (VAPID): each workspace signs push requests with its own key pair.
ALTER TABLE tenants ADD COLUMN vapid_public text NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN vapid_private bytea; -- sealed

-- Email bounces and complaints arrive at a per-workspace URL with this secret in it.
ALTER TABLE tenants ADD COLUMN bounce_token text NOT NULL
    DEFAULT replace(gen_random_uuid()::text, '-', '');
