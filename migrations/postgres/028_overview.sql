-- The Overview page's summary (GET /v1/stats/summary).
--
-- contacts_created: "contacts added today / in the last 7 days" reads only the newest entries of
-- one workspace, never the whole table. Partial like the count it serves (merged-away duplicates
-- don't count), so the count is an index-only scan.
--
-- Migrations run in a transaction, so this builds the index with a lock that holds writes to
-- contacts until it is done. On a large existing database, build it first without blocking and
-- this migration then does nothing:
--   CREATE INDEX CONCURRENTLY contacts_p0_created ON contacts_p0 (tenant_id, created_at) WHERE NOT (attrs ? 'merged_into');
--   ... (contacts_p1 to contacts_p7), then
--   CREATE INDEX contacts_created ON ONLY contacts (tenant_id, created_at) WHERE NOT (attrs ? 'merged_into');
--   ALTER INDEX contacts_created ATTACH PARTITION contacts_p0_created;  ... (each partition)
CREATE INDEX IF NOT EXISTS contacts_created ON contacts (tenant_id, created_at) WHERE NOT (attrs ? 'merged_into');

-- One workspace's broadcasts by status (sending now, and every list of a workspace's campaigns).
CREATE INDEX IF NOT EXISTS broadcasts_tenant ON broadcasts (tenant_id, status);
