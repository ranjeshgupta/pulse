-- O6 best time to send: a one-time campaign can send each message at its contact's best hour.
ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS best_time boolean NOT NULL DEFAULT false;
