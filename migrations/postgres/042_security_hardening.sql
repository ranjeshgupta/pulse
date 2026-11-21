-- Security hardening before launch.

-- Sessions end after a week without use (idle timeout), besides their fixed 14-day lifetime.
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS last_seen_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS sessions_user ON sessions (user_id);

-- SSO: an ID token without email_verified counts as unverified unless the admin says the identity
-- provider only issues verified addresses.
ALTER TABLE tenant_sso ADD COLUMN IF NOT EXISTS idp_verifies_email boolean NOT NULL DEFAULT false;
