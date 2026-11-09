-- +goose Up

-- A site's Telegram group or channel: the bot posts there like a member
-- (announcements, meeting calls, polls). The manager gets a code, adds the bot
-- to the group and sends /link <code> there; the webhook then stores chat_id.
-- One row per (site, provider); a row without chat_id is pending.
CREATE TABLE chat_group (
    org_id          uuid        NOT NULL REFERENCES organization (id),
    id              uuid        NOT NULL,
    site_id         uuid        NOT NULL,
    provider        text        NOT NULL CHECK (provider = 'telegram'),
    chat_id         text,
    title           text,
    kind            text        CHECK (kind IN ('group', 'channel')),
    code_hash       bytea,
    code_expires_at timestamptz,
    linked_by       uuid        REFERENCES "user" (id) ON DELETE SET NULL,
    verified        boolean     NOT NULL DEFAULT false,
    verified_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, provider),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    CHECK (NOT verified OR (chat_id IS NOT NULL AND title IS NOT NULL AND kind IS NOT NULL))
);
CREATE UNIQUE INDEX chat_group_code ON chat_group (org_id, code_hash) WHERE code_hash IS NOT NULL;
CREATE UNIQUE INDEX chat_group_chat ON chat_group (org_id, provider, chat_id) WHERE verified;
SELECT openbekci_enable_rls('chat_group');

-- The Telegram poll that mirrors the non-binding poll of an agenda item.
CREATE TABLE chat_group_poll (
    org_id         uuid        NOT NULL REFERENCES organization (id),
    site_id        uuid        NOT NULL,
    agenda_item_id uuid        NOT NULL,
    chat_id        text        NOT NULL,
    message_id     bigint      NOT NULL,
    tg_poll_id     text        NOT NULL,
    closed_at      timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agenda_item_id)
);
CREATE UNIQUE INDEX chat_group_poll_tg ON chat_group_poll (org_id, tg_poll_id);
SELECT openbekci_enable_rls('chat_group_poll');

-- Telegram update ids already handled: a replayed webhook does nothing twice.
CREATE TABLE chat_update (
    org_id    uuid        NOT NULL REFERENCES organization (id),
    provider  text        NOT NULL,
    update_id bigint      NOT NULL,
    seen_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, provider, update_id)
);
CREATE INDEX chat_update_seen ON chat_update (seen_at);
SELECT openbekci_enable_rls('chat_update');

-- What the site tells residents about its chat groups: the WhatsApp group invite link.
CREATE TABLE chat_site_info (
    org_id              uuid        NOT NULL REFERENCES organization (id),
    site_id             uuid        NOT NULL,
    whatsapp_invite_url text,
    updated_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, site_id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('chat_site_info');

-- when a poll was mirrored to, and closed in, the site's group
ALTER TABLE poll ADD COLUMN group_open_posted_at timestamptz, ADD COLUMN group_close_posted_at timestamptz;

-- telegram_group is a sender-chosen channel of announcements: one post in the site's group, not per person
ALTER TABLE announcement DROP CONSTRAINT announcement_channels_check;
ALTER TABLE announcement ADD CONSTRAINT announcement_channels_check CHECK (channels <@ ARRAY['in_app', 'email', 'sms', 'push', 'chat', 'telegram_group']::text[]);

-- +goose Down
ALTER TABLE announcement DROP CONSTRAINT announcement_channels_check;
ALTER TABLE announcement ADD CONSTRAINT announcement_channels_check CHECK (channels <@ ARRAY['in_app', 'email', 'sms', 'push', 'chat']::text[]);
ALTER TABLE poll DROP COLUMN group_open_posted_at, DROP COLUMN group_close_posted_at;
DROP TABLE chat_site_info;
DROP TABLE chat_update;
DROP TABLE chat_group_poll;
DROP TABLE chat_group;
