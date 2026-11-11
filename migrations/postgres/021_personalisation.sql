-- M2, personalisation.

-- The properties of the event (or form submission) that started an enrollment, for {{event.x}} in
-- every message the journey sends: personal properties (per the event schema) left out, at most 16 KB.
ALTER TABLE journey_enrollments ADD COLUMN trigger jsonb;

-- What a template does when a variable has no value and no default: 'empty' renders it empty,
-- 'skip' sends nothing to that contact (recorded as <channel>_failed with props.skipped).
ALTER TABLE templates ADD COLUMN missing_values text NOT NULL DEFAULT 'empty';
