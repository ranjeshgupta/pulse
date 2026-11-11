-- Product properties of events as columns, for product reports and dashboards. MATERIALIZED
-- columns are computed on insert (older parts get them when merged, or on read).
ALTER TABLE events ADD COLUMN IF NOT EXISTS product_code String MATERIALIZED
    multiIf(JSONType(props, 'product_code') = 'String', JSONExtractString(props, 'product_code'),
            JSONType(props, 'product_code') IN ('Int64', 'UInt64', 'Double'), toString(JSONExtractFloat(props, 'product_code')), '');
ALTER TABLE events ADD COLUMN IF NOT EXISTS product_category String MATERIALIZED JSONExtractString(props, 'product_category');
