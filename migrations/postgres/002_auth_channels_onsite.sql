-- Phase 2: sign-in (users, sessions, SSO), channels (templates per channel, providers, devices,
-- sandbox inbox) and on-site content (focus items, DWC).

-- Workspaces get a slug (used at login and for SSO) and a public site key for browser calls.
ALTER TABLE tenants ADD COLUMN slug text UNIQUE;
ALTER TABLE tenants ADD COLUMN site_key text UNIQUE;
UPDATE tenants SET slug = 'workspace-' || id WHERE slug IS NULL;
UPDATE tenants SET site_key = 'sk_' || md5(random()::text || id) WHERE site_key IS NULL;
ALTER TABLE tenants ALTER COLUMN slug SET NOT NULL;
ALTER TABLE tenants ALTER COLUMN site_key SET NOT NULL;

-- role: admin (everything, incl. settings and team) | editor (marketing objects only)
CREATE TABLE users (
    id            bigserial PRIMARY KEY,
    tenant_id     bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    email         text        NOT NULL,
    name          text        NOT NULL DEFAULT '',
    password_hash text,                 -- NULL for SSO-only users
    sso_subject   text,
    role          text        NOT NULL DEFAULT 'editor',
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    UNIQUE (tenant_id, email),
    UNIQUE (tenant_id, sso_subject)
);

-- Only a hash of the session token is stored.
CREATE TABLE sessions (
    token_hash text PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    tenant_id  bigint      NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_expires ON sessions (expires_at);

-- Per-workspace OIDC (Keycloak, Azure AD, Google, Okta...). discovery_url is optional: set it when the
-- server reaches the IdP on a different address than browsers do (the issuer stays the public URL).
CREATE TABLE tenant_sso (
    tenant_id         bigint PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    enabled           boolean NOT NULL DEFAULT false,
    issuer            text    NOT NULL,
    discovery_url     text    NOT NULL DEFAULT '',
    client_id         text    NOT NULL,
    client_secret_enc bytea   NOT NULL,
    auto_create_users boolean NOT NULL DEFAULT true,
    default_role      text    NOT NULL DEFAULT 'editor',
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- Templates per channel. email keeps subject/html; other channels keep their fields in content.
ALTER TABLE templates ADD COLUMN channel text NOT NULL DEFAULT 'email';
ALTER TABLE templates ADD COLUMN content jsonb NOT NULL DEFAULT '{}';
ALTER TABLE templates ADD COLUMN builder jsonb;            -- GrapesJS project data for the email editor
ALTER TABLE templates ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE templates ALTER COLUMN subject SET DEFAULT '';
ALTER TABLE templates ALTER COLUMN html SET DEFAULT '';
CREATE INDEX templates_tenant_channel ON templates (tenant_id, channel);

-- Delivery providers per tenant and channel; secrets are encrypted with the server key.
CREATE TABLE channel_providers (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    channel     text        NOT NULL,
    provider    text        NOT NULL,
    name        text        NOT NULL,
    config      jsonb       NOT NULL DEFAULT '{}',
    secrets_enc bytea,
    is_default  boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX channel_providers_default ON channel_providers (tenant_id, channel) WHERE is_default;

-- Push tokens (registered by the SDKs).
CREATE TABLE contact_devices (
    tenant_id  bigint      NOT NULL,
    token      text        NOT NULL,
    contact_id bigint      NOT NULL,
    platform   text        NOT NULL,  -- android | ios | web
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, token)
);
CREATE INDEX contact_devices_contact ON contact_devices (tenant_id, contact_id);

-- Messages "sent" through the sandbox provider, shown in the app's test inbox.
CREATE TABLE sandbox_messages (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL,
    channel    text        NOT NULL,
    recipient  text        NOT NULL,
    message_id text        NOT NULL,
    payload    jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sandbox_messages_tenant ON sandbox_messages (tenant_id, id DESC);

-- Journeys can be edited while draft or paused.
ALTER TABLE journeys ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- On-site: focus items (modal / bar / slide-in) and Dynamic Web Content slots.
CREATE TABLE focus_items (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    kind       text        NOT NULL,             -- modal | bar | slide
    content    jsonb       NOT NULL,
    targeting  jsonb       NOT NULL DEFAULT '{}',
    status     text        NOT NULL DEFAULT 'draft',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE dwc_slots (
    id           bigserial PRIMARY KEY,
    tenant_id    bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    slot         text        NOT NULL,
    name         text        NOT NULL,
    default_html text        NOT NULL DEFAULT '',
    variants     jsonb       NOT NULL DEFAULT '[]',  -- [{segment_id, html}], first match wins
    status       text        NOT NULL DEFAULT 'draft',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slot)
);
