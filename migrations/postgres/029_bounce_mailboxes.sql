-- Several bounce mailboxes per workspace, each with its own name and folders; a folder says what
-- it holds (bounces, spam complaints, or either: read each message to tell). The one mailbox a
-- workspace had becomes its first, its folder read as "either".
ALTER TABLE bounce_mailboxes ADD COLUMN IF NOT EXISTS id bigserial;
ALTER TABLE bounce_mailboxes ADD COLUMN IF NOT EXISTS name text NOT NULL DEFAULT 'Bounce mailbox';
ALTER TABLE bounce_mailboxes ADD COLUMN IF NOT EXISTS folders jsonb NOT NULL DEFAULT '[]';
UPDATE bounce_mailboxes SET folders = jsonb_build_array(jsonb_build_object('name', folder, 'kind', 'auto')) WHERE folders = '[]'::jsonb;
ALTER TABLE bounce_mailboxes DROP CONSTRAINT IF EXISTS bounce_mailboxes_pkey;
ALTER TABLE bounce_mailboxes ADD PRIMARY KEY (id);
CREATE INDEX IF NOT EXISTS bounce_mailboxes_tenant ON bounce_mailboxes (tenant_id);
ALTER TABLE bounce_mailboxes DROP COLUMN IF EXISTS folder;
