-- Forms: fields mapped to profile attributes, served on a hosted page (/f/{public_key}) and by the web SDK.
-- A submission is a form_submitted event, so it lands on the timeline and drives segments and journeys.
CREATE TABLE forms (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    public_key text        NOT NULL UNIQUE,
    fields     jsonb       NOT NULL DEFAULT '[]',
    settings   jsonb       NOT NULL DEFAULT '{}',
    status     text        NOT NULL DEFAULT 'draft', -- draft | active
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX forms_tenant ON forms (tenant_id);

-- Preference center: lists a contact may join or leave themselves, with the text they see.
ALTER TABLE segments ADD COLUMN public boolean NOT NULL DEFAULT false;
ALTER TABLE segments ADD COLUMN description text NOT NULL DEFAULT '';

-- Finding a contact by email (form submissions) or by phone (inbound SMS: STOP and START replies).
CREATE INDEX contacts_email ON contacts (tenant_id, lower(email)) WHERE email <> '';
CREATE INDEX contacts_phone ON contacts (tenant_id, (attrs ->> 'phone')) WHERE attrs ? 'phone';

-- Broadcast A/B tests. ab: {variants: [{template_id, label}], test_percent, metric, wait_hours}.
-- A run sends the variants to a test group first; the winner goes to everyone else later.
ALTER TABLE broadcasts ADD COLUMN ab jsonb;
-- Reports count a conversion when a recipient does this event within conversion_days of the send.
ALTER TABLE broadcasts ADD COLUMN conversion_event text NOT NULL DEFAULT '';
ALTER TABLE broadcasts ADD COLUMN conversion_days integer NOT NULL DEFAULT 3;

ALTER TABLE broadcast_runs ADD COLUMN test_recipients integer NOT NULL DEFAULT 0;
ALTER TABLE broadcast_runs ADD COLUMN winner_due_at timestamptz;     -- automatic pick (opens or clicks)
ALTER TABLE broadcast_runs ADD COLUMN winner text;                   -- chosen variant label
ALTER TABLE broadcast_runs ADD COLUMN winner_reason text NOT NULL DEFAULT '';
ALTER TABLE broadcast_runs ADD COLUMN winner_claimed_at timestamptz;
ALTER TABLE broadcast_runs ADD COLUMN winner_sent_at timestamptz;
ALTER TABLE broadcast_runs ADD COLUMN winner_recipients integer NOT NULL DEFAULT 0;
CREATE INDEX broadcast_runs_winner_due ON broadcast_runs (winner_due_at) WHERE winner_sent_at IS NULL;

-- Funnels: ordered steps (events, optionally filtered by properties) completed within a window.
CREATE TABLE funnels (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name        text        NOT NULL,
    steps       jsonb       NOT NULL,
    window_days integer     NOT NULL DEFAULT 7,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX funnels_tenant ON funnels (tenant_id);
