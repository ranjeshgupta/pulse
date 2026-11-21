-- O6 reusable content blocks: a header, footer or offer written once and used in any template as
-- {{block.key}}; editing it changes every template that uses it, from the next send.
CREATE TABLE IF NOT EXISTS content_blocks (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    key        text        NOT NULL,
    name       text        NOT NULL,
    html       text        NOT NULL DEFAULT '',
    text       text        NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, key)
);
