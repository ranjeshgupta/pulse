-- Batch 8b: a bounce mailbox per workspace (IMAP), read for delivery status notifications and
-- feedback-loop complaints. password is sealed with the server key and never returned.
CREATE TABLE bounce_mailboxes (
    tenant_id     bigint PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    enabled       boolean     NOT NULL DEFAULT false,
    host          text        NOT NULL,
    port          integer     NOT NULL,
    tls           text        NOT NULL DEFAULT 'tls', -- tls | starttls | none (development only)
    username      text        NOT NULL,
    password      bytea,
    folder        text        NOT NULL DEFAULT 'INBOX',
    next_check_at timestamptz,
    last_check_at timestamptz,
    last_error    text        NOT NULL DEFAULT '',
    processed     bigint      NOT NULL DEFAULT 0,
    skipped       bigint      NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bounce_mailboxes_due ON bounce_mailboxes (next_check_at) WHERE enabled;
