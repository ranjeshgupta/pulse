-- Static lists are segments whose members are added and removed by hand (or import), not by a rule.
ALTER TABLE segments ADD COLUMN kind text NOT NULL DEFAULT 'rule'; -- rule | static
ALTER TABLE segments ALTER COLUMN rule DROP NOT NULL;

-- Attribute definitions: labels and types the app uses to offer the right comparisons.
CREATE TABLE attribute_defs (
    tenant_id   bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    key         text        NOT NULL,
    label       text        NOT NULL,
    type        text        NOT NULL DEFAULT 'string', -- string | number | boolean | date
    pii         boolean     NOT NULL DEFAULT false,
    description text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, key)
);

-- Scoring: each matching event changes the contact's points.
CREATE TABLE point_rules (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    event      text        NOT NULL,
    points     integer     NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, event)
);

-- Identity verification for the web SDK: the customer's server signs user ids with this secret.
-- gen_random_uuid() is built in and uses a cryptographic random source (two UUIDs: ~244 random bits).
ALTER TABLE tenants ADD COLUMN identity_secret text NOT NULL
    DEFAULT replace(gen_random_uuid()::text, '-', '') || replace(gen_random_uuid()::text, '-', '');
ALTER TABLE tenants ADD COLUMN require_signed_identify boolean NOT NULL DEFAULT false;
