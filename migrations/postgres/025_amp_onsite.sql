-- M6 AMP and on-site. Web push settings (package webcheck): the no-code opt-in prompt the web SDK
-- shows, the site the install check opens and the service worker path, as one JSON document;
-- '{}' means the defaults (prompt off). AMP parts live in templates.content (amp, amp_blocks) and
-- content variants' audiences in dwc_slots.variants / focus_items.targeting, so they need no columns.
ALTER TABLE tenant_settings ADD COLUMN IF NOT EXISTS webpush jsonb NOT NULL DEFAULT '{}';
