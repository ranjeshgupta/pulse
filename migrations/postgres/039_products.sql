-- O6 product catalog: a workspace's products, for recommendations in messages.
CREATE TABLE IF NOT EXISTS products (
    tenant_id  bigint           NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    code       text             NOT NULL,
    name       text             NOT NULL,
    url        text             NOT NULL DEFAULT '',
    image_url  text             NOT NULL DEFAULT '',
    price      double precision,
    sale_price double precision,
    currency   text             NOT NULL DEFAULT '',
    category   text             NOT NULL DEFAULT '',
    in_stock   boolean          NOT NULL DEFAULT true,
    attrs      jsonb            NOT NULL DEFAULT '{}',
    updated_at timestamptz      NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, code)
);
CREATE INDEX IF NOT EXISTS products_name ON products (tenant_id, lower(name) text_pattern_ops);
