-- In-app campaigns: shown by the app SDK when their trigger event or screen happens.
CREATE TABLE inapp_messages (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    layout     text        NOT NULL,
    content    jsonb       NOT NULL,
    targeting  jsonb       NOT NULL,
    priority   integer     NOT NULL DEFAULT 0,
    status     text        NOT NULL DEFAULT 'draft', -- draft | active
    start_at   timestamptz NOT NULL DEFAULT now(),
    end_at     timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX inapp_messages_tenant ON inapp_messages (tenant_id);
