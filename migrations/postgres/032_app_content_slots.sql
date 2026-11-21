-- App content slots (batch O3): a dynamic content slot lives on a website (web, HTML) or in a mobile
-- app (app: a card the SDK draws, or HTML). Slot ids stay unique per workspace across both.
ALTER TABLE dwc_slots ADD COLUMN IF NOT EXISTS platform text NOT NULL DEFAULT 'web';
ALTER TABLE dwc_slots ADD COLUMN IF NOT EXISTS default_card jsonb;
