-- O6 multi-language templates: a template's text in other languages.
ALTER TABLE templates ADD COLUMN IF NOT EXISTS translations jsonb NOT NULL DEFAULT '[]';
