-- O6 control groups: a one-time campaign can keep a share of its audience back to measure its effect.
ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS holdout_percent integer NOT NULL DEFAULT 0;
