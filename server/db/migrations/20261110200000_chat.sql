-- +goose Up

-- Chat channel (Telegram, WhatsApp, pack providers): the organization's bot
-- credentials and the links between a user and a chat account.

-- One row per (organization, provider): plain settings and the sealed secrets
-- (key -> base64 sealed value; x-secret properties of the provider's schema).
CREATE TABLE chat_config (
    org_id     uuid        NOT NULL REFERENCES organization (id),
    provider   text        NOT NULL CHECK (char_length(provider) BETWEEN 1 AND 80),
    settings   jsonb       NOT NULL DEFAULT '{}',
    secrets    jsonb       NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, provider)
);
SELECT openbekci_enable_rls('chat_config');

-- One row per (organization, user, provider). A row starts pending (code_hash,
-- no external id); the provider's webhook with the code verifies it. The
-- external id (chat id, phone number) is sealed; external_hash only keeps one
-- chat account from being linked to two users of an organization. A dead link
-- (the provider reports the chat gone) loses verified_at and stays listed.
-- last_inbound_at opens WhatsApp's 24 hour session window.
CREATE TABLE chat_link (
    org_id             uuid        NOT NULL REFERENCES organization (id),
    id                 uuid        NOT NULL,
    user_id            uuid        NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    provider           text        NOT NULL CHECK (char_length(provider) BETWEEN 1 AND 80),
    code_hash          bytea,
    code_expires_at    timestamptz,
    external_id_sealed bytea,
    external_hash      bytea,
    verified_at        timestamptz,
    last_inbound_at    timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, user_id, provider)
);
CREATE UNIQUE INDEX chat_link_code ON chat_link (org_id, provider, code_hash) WHERE code_hash IS NOT NULL;
CREATE UNIQUE INDEX chat_link_external ON chat_link (org_id, provider, external_hash) WHERE verified_at IS NOT NULL;
SELECT openbekci_enable_rls('chat_link');

-- the chat channel joins the others
ALTER TABLE notification_delivery DROP CONSTRAINT notification_delivery_channel_check;
ALTER TABLE notification_delivery ADD CONSTRAINT notification_delivery_channel_check CHECK (channel IN ('in_app', 'email', 'sms', 'push', 'chat'));
ALTER TABLE site_channel_default DROP CONSTRAINT site_channel_default_channels_check;
ALTER TABLE site_channel_default ADD CONSTRAINT site_channel_default_channels_check CHECK (channels <@ ARRAY['in_app', 'email', 'sms', 'push', 'chat']::text[]);
ALTER TABLE announcement DROP CONSTRAINT announcement_channels_check;
ALTER TABLE announcement ADD CONSTRAINT announcement_channels_check CHECK (channels <@ ARRAY['in_app', 'email', 'sms', 'push', 'chat']::text[]);

-- +goose Down
ALTER TABLE announcement DROP CONSTRAINT announcement_channels_check;
ALTER TABLE announcement ADD CONSTRAINT announcement_channels_check CHECK (channels <@ ARRAY['in_app', 'email', 'sms', 'push']::text[]);
ALTER TABLE site_channel_default DROP CONSTRAINT site_channel_default_channels_check;
ALTER TABLE site_channel_default ADD CONSTRAINT site_channel_default_channels_check CHECK (channels <@ ARRAY['in_app', 'email', 'sms', 'push']::text[]);
ALTER TABLE notification_delivery DROP CONSTRAINT notification_delivery_channel_check;
ALTER TABLE notification_delivery ADD CONSTRAINT notification_delivery_channel_check CHECK (channel IN ('in_app', 'email', 'sms', 'push'));
DROP TABLE chat_link;
DROP TABLE chat_config;
