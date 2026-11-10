-- Schedules: every journey, focus item and content slot runs from start_at (required) until end_at;
-- a NULL end_at means it runs until stopped.
ALTER TABLE journeys    ADD COLUMN start_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN end_at timestamptz;
ALTER TABLE focus_items ADD COLUMN start_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN end_at timestamptz;
ALTER TABLE dwc_slots   ADD COLUMN start_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN end_at timestamptz;

-- Broadcasts: one message to everyone in a segment, once or on a repeat.
-- status: draft -> scheduled -> (running -> scheduled)* -> completed | cancelled
CREATE TABLE broadcasts (
    id                   bigserial PRIMARY KEY,
    tenant_id            bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name                 text        NOT NULL,
    channel              text        NOT NULL,
    template_id          bigint      NOT NULL,
    fallback_template_id bigint,
    segment_id           bigint      NOT NULL,
    start_at             timestamptz NOT NULL,
    end_at               timestamptz,
    repeat               text        NOT NULL DEFAULT 'none',  -- none | daily | weekly | monthly
    next_run_at          timestamptz,
    status               text        NOT NULL DEFAULT 'draft',
    claimed_at           timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX broadcasts_due ON broadcasts (next_run_at) WHERE status = 'scheduled';

CREATE TABLE broadcast_runs (
    id           bigserial PRIMARY KEY,
    broadcast_id bigint      NOT NULL REFERENCES broadcasts (id) ON DELETE CASCADE,
    scheduled_at timestamptz NOT NULL,
    started_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    recipients   integer     NOT NULL DEFAULT 0,
    UNIQUE (broadcast_id, scheduled_at)
);
