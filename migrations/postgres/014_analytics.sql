-- Batch 7, analytics.
-- Automatic A/B winners of journey split steps: {"<step id>": {"path": 1, "next": "b", "label": "B",
-- "metric": "clicked", "decided_at": "...", "stats": [...]}}. Set once per step by the journey workers.
ALTER TABLE journeys ADD COLUMN split_winners jsonb NOT NULL DEFAULT '{}';

-- Revenue: which event is a purchase and which of its properties holds the amount.
ALTER TABLE tenant_settings ADD COLUMN revenue jsonb NOT NULL DEFAULT '{"event": "purchase", "amount_prop": "amount", "currency": ""}';

-- Saved analytics reports: cohort (retention) reports and custom reports. Results are computed on
-- demand in ClickHouse; spec is the report definition (analytics.CohortSpec or analytics.ReportSpec).
CREATE TABLE saved_reports (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    kind       text        NOT NULL CHECK (kind IN ('cohort', 'custom')),
    name       text        NOT NULL,
    spec       jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX saved_reports_tenant ON saved_reports (tenant_id, kind);
