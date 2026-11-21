-- O6 public archive and RSS-to-email: a campaign can be listed in the workspace's public archive,
-- and a repeating campaign can send what is new in an RSS/Atom feed (each run keeps its items).
ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS archive_public boolean NOT NULL DEFAULT false;
ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS feed_url text NOT NULL DEFAULT '';
ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS feed_seen_at timestamptz;
ALTER TABLE broadcast_runs ADD COLUMN IF NOT EXISTS feed_html text NOT NULL DEFAULT '';
ALTER TABLE broadcast_runs ADD COLUMN IF NOT EXISTS feed_text text NOT NULL DEFAULT '';
