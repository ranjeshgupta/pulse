-- Lifecycle stages (Lead -> MQL -> SQL -> Customer). A contact's stage is the profile attribute
-- "stage" (the name), changed only through stage_changed events. min_points, when set, moves a
-- contact forward to this stage once a points change reaches it.
CREATE TABLE stages (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name        text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    sort_order  integer     NOT NULL DEFAULT 0,
    min_points  integer,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX stages_name ON stages (tenant_id, lower(name));

-- Companies (B2B accounts). Contacts link to one through company_linked events; the link and a copy
-- of the company's attributes live on the contact (company_id, company_name, company_<key>).
CREATE TABLE companies (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    domain     text        NOT NULL DEFAULT '',
    attrs      jsonb       NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX companies_name ON companies (tenant_id, lower(name));

-- Members of a company and of a stage, for counts, member lists and fan-out of changes.
CREATE INDEX contacts_company ON contacts (tenant_id, (attrs ->> 'company_id')) WHERE attrs ? 'company_id';
CREATE INDEX contacts_stage ON contacts (tenant_id, (attrs ->> 'stage')) WHERE attrs ? 'stage';
