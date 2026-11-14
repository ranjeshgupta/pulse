-- A broadcast's subscription topic (Settings → Bounces & unsubscribes): contacts who left the topic
-- are skipped, and the unsubscribe link in its emails leaves that topic only.
ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS topic text NOT NULL DEFAULT '';
