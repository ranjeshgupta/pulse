-- O6 sending speed: a one-time campaign's run can be paced to at most N messages an hour.
ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS send_per_hour integer NOT NULL DEFAULT 0;
