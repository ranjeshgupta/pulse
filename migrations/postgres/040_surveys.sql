-- O6 surveys and NPS: surveys and their responses (one per contact and message; public answers
-- have no contact).
CREATE TABLE IF NOT EXISTS surveys (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    status     text        NOT NULL DEFAULT 'draft',
    questions  jsonb       NOT NULL,
    intro      text        NOT NULL DEFAULT '',
    thank_you  text        NOT NULL DEFAULT '',
    public_key text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS surveys_tenant ON surveys (tenant_id);

CREATE TABLE IF NOT EXISTS survey_responses (
    id          bigserial PRIMARY KEY,
    survey_id   bigint      NOT NULL REFERENCES surveys (id) ON DELETE CASCADE,
    tenant_id   bigint      NOT NULL,
    external_id text        NOT NULL DEFAULT '', -- '' for a public answer
    message_id  text        NOT NULL DEFAULT '',
    answers     jsonb       NOT NULL,
    nps         smallint,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
-- A contact answers once per message (answering again updates it).
CREATE UNIQUE INDEX IF NOT EXISTS survey_responses_once ON survey_responses (survey_id, external_id, message_id) WHERE external_id <> '';
CREATE INDEX IF NOT EXISTS survey_responses_survey ON survey_responses (survey_id, created_at);
