-- O6 approvals: someone who may edit a campaign asks for it to be launched; someone who may
-- publish approves (it launches) or sends it back. version is a hash of the campaign as asked:
-- an approval doesn't launch a campaign that changed since.
CREATE TABLE IF NOT EXISTS approval_requests (
    id            bigserial PRIMARY KEY,
    tenant_id     bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    kind          text        NOT NULL,             -- broadcast | journey
    campaign_id   bigint      NOT NULL,
    version       text        NOT NULL,
    requested_by  bigint      NOT NULL DEFAULT 0,   -- user id (0: an API key)
    requester     text        NOT NULL DEFAULT '',  -- name or email, as shown
    note          text        NOT NULL DEFAULT '',
    status        text        NOT NULL DEFAULT 'pending', -- pending | approved | rejected | withdrawn
    decided_by    bigint      NOT NULL DEFAULT 0,
    decider       text        NOT NULL DEFAULT '',
    decision_note text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    decided_at    timestamptz
);
CREATE INDEX IF NOT EXISTS approval_requests_open ON approval_requests (tenant_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS approval_requests_campaign ON approval_requests (tenant_id, kind, campaign_id, created_at DESC);
