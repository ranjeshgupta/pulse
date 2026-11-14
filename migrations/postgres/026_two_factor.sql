-- Two-step sign-in with an authenticator app (password sign-ins only; SSO leaves it to the identity
-- provider). One row per user who has started setting it up. The secrets are sealed with the
-- server key (PULSE_SECRET_KEY), like provider credentials; recovery codes are kept as SHA-256
-- hashes and removed as they are used.
CREATE TABLE IF NOT EXISTS user_two_factor (
    user_id         bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    tenant_id       bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    secret_enc      bytea,                        -- the confirmed secret; NULL until set up
    pending_enc     bytea,                        -- a new secret shown as a QR code, not yet confirmed
    enabled_at      timestamptz,
    last_step       bigint      NOT NULL DEFAULT 0, -- last 30-second step accepted: a code works once
    recovery_hashes text[]      NOT NULL DEFAULT '{}',
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS user_two_factor_tenant ON user_two_factor (tenant_id);

-- Who must use it: {"mode": "optional" | "everyone" | "some", "roles": [...], "users": [...]}.
-- '{}' is optional for everyone.
ALTER TABLE tenant_settings ADD COLUMN IF NOT EXISTS two_factor jsonb NOT NULL DEFAULT '{}';
