-- A one-time campaign can send through a chosen provider of its channel instead of the default.
-- Removing the provider puts the campaign back on the channel's default.
ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS provider_id bigint REFERENCES channel_providers(id) ON DELETE SET NULL;
