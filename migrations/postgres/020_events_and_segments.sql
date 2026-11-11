-- M1, events and segments.

-- Event schemas: the properties each event carries, checked as events arrive (see internal/eventschema).
CREATE TABLE event_schemas (
    tenant_id   bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name        text        NOT NULL,
    label       text        NOT NULL DEFAULT '',
    description text        NOT NULL DEFAULT '',
    mode        text        NOT NULL DEFAULT 'warn', -- off | warn | enforce
    props       jsonb       NOT NULL DEFAULT '[]',
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, name)
);

-- Segment refresh: NULL follows every event (live); {"mode":"daily","at":"06:30","tz":"Asia/Kolkata"}
-- is rebuilt from ClickHouse once a day, at next_refresh_at.
ALTER TABLE segments ADD COLUMN refresh jsonb;
ALTER TABLE segments ADD COLUMN next_refresh_at timestamptz;

-- Contact fields of type list (a set of values, like tags) and enum (one of options).
ALTER TABLE attribute_defs ADD COLUMN options jsonb NOT NULL DEFAULT '[]';
