-- Batch 8b: files uploaded through forms are kept with the assets (assets + asset_files, within the
-- workspace's storage cap) but are private: upload_form_id marks them, the Assets page, public
-- downloads and email attachments never see them, and only signed-in users with contacts access
-- download them (GET /v1/forms/{id}/uploads/{upload}). Deleting the form deletes its uploads.
ALTER TABLE assets ADD COLUMN upload_form_id bigint;
ALTER TABLE assets ADD COLUMN upload_external_id text NOT NULL DEFAULT '';
CREATE INDEX assets_uploads ON assets (tenant_id, upload_form_id) WHERE upload_form_id IS NOT NULL;
