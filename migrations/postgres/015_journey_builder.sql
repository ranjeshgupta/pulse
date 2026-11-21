-- Batch 7b, the journey builder's extra decisions, actions and conditions.
-- jumps counts an enrollment's jump steps (capped at 25); step keys carry it so a step repeated
-- after a jump runs again. form is the form submission that started or resumed the enrollment
-- (props of form_submitted), read by "form field value" conditions.
ALTER TABLE journey_enrollments ADD COLUMN jumps integer NOT NULL DEFAULT 0;
ALTER TABLE journey_enrollments ADD COLUMN form jsonb;

-- Side effects that aren't idempotent by themselves (adding to a company's score) record their
-- step key here, so a retried journey step applies them once.
CREATE TABLE journey_effects (
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    key        text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, key)
);
CREATE INDEX journey_effects_age ON journey_effects (created_at);
