-- Segments can be unpublished and given a publish window (start required, end optional), like
-- on-site content. A segment is live while published and inside its window; otherwise the segment
-- engine stops recomputing it (its members stay as they were) and it can't be chosen for new sends.
ALTER TABLE segments ADD COLUMN IF NOT EXISTS published boolean NOT NULL DEFAULT true;
ALTER TABLE segments ADD COLUMN IF NOT EXISTS start_at timestamptz;
UPDATE segments SET start_at = created_at WHERE start_at IS NULL;
ALTER TABLE segments ALTER COLUMN start_at SET DEFAULT now();
ALTER TABLE segments ALTER COLUMN start_at SET NOT NULL;
ALTER TABLE segments ADD COLUMN IF NOT EXISTS end_at timestamptz;
