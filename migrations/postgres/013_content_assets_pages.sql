-- Assets: files for gated or open downloads (/a/{public_key}) and email attachments. The file bytes
-- live in Postgres (asset_files, at most 25 MB each) so every API instance can serve them without a
-- shared disk. form_id, when set, gates the download behind that form.
CREATE TABLE assets (
    id             bigserial PRIMARY KEY,
    tenant_id      bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    public_key     text        NOT NULL UNIQUE,
    title          text        NOT NULL,
    description    text        NOT NULL DEFAULT '',
    filename       text        NOT NULL,
    content_type   text        NOT NULL,
    size           bigint      NOT NULL,
    sha256         text        NOT NULL,
    form_id        bigint,     -- no foreign key: a gated asset whose form is gone (or a draft) is not served, never opened up
    download_count bigint      NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX assets_tenant ON assets (tenant_id);

-- The bytes, apart from the metadata so lists and counter updates never touch them. Files are
-- usually compressed already (PDF, images, Office, zip): store them out of line without trying again.
CREATE TABLE asset_files (
    asset_id bigint PRIMARY KEY REFERENCES assets (id) ON DELETE CASCADE,
    data     bytea  NOT NULL
);
ALTER TABLE asset_files ALTER COLUMN data SET STORAGE EXTERNAL;

-- Landing pages, served at /p/{workspace slug}/{slug} while published and inside their schedule
-- (start required, end optional: empty means until unpublished).
CREATE TABLE landing_pages (
    id               bigserial PRIMARY KEY,
    tenant_id        bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    slug             text        NOT NULL,
    title            text        NOT NULL,
    meta_description text        NOT NULL DEFAULT '',
    html             text        NOT NULL,
    builder          jsonb,
    status           text        NOT NULL DEFAULT 'draft', -- draft | published
    start_at         timestamptz NOT NULL DEFAULT now(),
    end_at           timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);
