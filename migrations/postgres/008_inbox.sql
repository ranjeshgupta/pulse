-- The app inbox: messages a contact reads in the app (or on the website) through the SDKs.
-- message_id is the send request's id, so a redelivered request stores the message once.
CREATE TABLE inbox_messages (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint      NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    contact_id bigint      NOT NULL,
    message_id text        NOT NULL,
    title      text        NOT NULL,
    body       text        NOT NULL,
    image_url  text        NOT NULL DEFAULT '',
    url        text        NOT NULL DEFAULT '',
    category   text        NOT NULL DEFAULT '',
    data       jsonb       NOT NULL DEFAULT '{}',
    meta       jsonb       NOT NULL DEFAULT '{}', -- journey/broadcast ids, carried into read and click events
    created_at timestamptz NOT NULL DEFAULT now(),
    read_at    timestamptz,
    deleted_at timestamptz,
    expires_at timestamptz NOT NULL,
    UNIQUE (tenant_id, message_id)
);
CREATE INDEX inbox_messages_contact ON inbox_messages (tenant_id, contact_id, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX inbox_messages_expiry ON inbox_messages (expires_at);
