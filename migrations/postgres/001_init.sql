-- Mutable state. History and analytics live in ClickHouse.
-- tenant_id leads every key so a tenant's rows cluster together and can later move to its own shard.

CREATE TABLE tenants (
    id           bigserial PRIMARY KEY,
    name         text        NOT NULL,
    api_key_hash text        NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- Dense per-tenant contact ids (1, 2, 3, ...) so a segment fits in a Redis bitmap.
CREATE TABLE tenant_counters (
    tenant_id    bigint PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    next_contact bigint NOT NULL DEFAULT 1
);

CREATE TABLE contacts (
    tenant_id   bigint      NOT NULL,
    id          bigint      NOT NULL,
    external_id text        NOT NULL,
    email       text        NOT NULL DEFAULT '',
    attrs       jsonb       NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, external_id)
) PARTITION BY HASH (tenant_id);

CREATE TABLE contacts_p0 PARTITION OF contacts FOR VALUES WITH (MODULUS 8, REMAINDER 0);
CREATE TABLE contacts_p1 PARTITION OF contacts FOR VALUES WITH (MODULUS 8, REMAINDER 1);
CREATE TABLE contacts_p2 PARTITION OF contacts FOR VALUES WITH (MODULUS 8, REMAINDER 2);
CREATE TABLE contacts_p3 PARTITION OF contacts FOR VALUES WITH (MODULUS 8, REMAINDER 3);
CREATE TABLE contacts_p4 PARTITION OF contacts FOR VALUES WITH (MODULUS 8, REMAINDER 4);
CREATE TABLE contacts_p5 PARTITION OF contacts FOR VALUES WITH (MODULUS 8, REMAINDER 5);
CREATE TABLE contacts_p6 PARTITION OF contacts FOR VALUES WITH (MODULUS 8, REMAINDER 6);
CREATE TABLE contacts_p7 PARTITION OF contacts FOR VALUES WITH (MODULUS 8, REMAINDER 7);

-- status: new -> building -> ready. The segment engine claims 'new' rows for backfill.
CREATE TABLE segments (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    rule       jsonb       NOT NULL,
    has_window boolean     NOT NULL DEFAULT false,
    status     text        NOT NULL DEFAULT 'new',
    built_at   timestamptz,
    swept_at   timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX segments_tenant ON segments (tenant_id);
CREATE INDEX segments_status ON segments (status);

CREATE TABLE templates (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    subject    text        NOT NULL,
    html       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- status: draft -> active -> paused
CREATE TABLE journeys (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    graph      jsonb       NOT NULL,
    status     text        NOT NULL DEFAULT 'draft',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX journeys_status ON journeys (status);

-- One enrollment per (journey, contact): Phase 1 journeys are enter-once.
CREATE TABLE journey_enrollments (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL,
    journey_id bigint      NOT NULL REFERENCES journeys (id) ON DELETE CASCADE,
    contact_id bigint      NOT NULL,
    node       text        NOT NULL,
    status     text        NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (journey_id, contact_id)
);

-- Durable waits. Pollers claim due rows with FOR UPDATE SKIP LOCKED, so workers never double-fire.
CREATE TABLE journey_timers (
    enrollment_id bigint      PRIMARY KEY REFERENCES journey_enrollments (id) ON DELETE CASCADE,
    node          text        NOT NULL,
    due_at        timestamptz NOT NULL
);
CREATE INDEX journey_timers_due ON journey_timers (due_at);
