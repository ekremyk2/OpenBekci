-- +goose Up

-- Notifications (backend plan 4.12, section 12): in-app rows per person, one
-- delivery record per channel, the site's defaults for automatic messages
-- (decision O5), per-person mutes and the monthly SMS counter of a site.
-- Keyed by person, not user: a person can be reached by email or SMS without
-- an account, and the same user has a separate person in every organization.

CREATE TABLE notification (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid,
    person_id  uuid        NOT NULL,
    type       text        NOT NULL CHECK (type ~ '^[a-z][a-z0-9_.]{0,63}$'),
    payload    jsonb       NOT NULL DEFAULT '{}',
    -- app path the notification opens, e.g. /tickets/<id>
    link       text,
    -- notifications.deliver jobs may retry: (job key, person) makes the insert idempotent
    dedupe_key text,
    created_at timestamptz NOT NULL DEFAULT now(),
    read_at    timestamptz,
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
CREATE UNIQUE INDEX notification_dedupe ON notification (org_id, person_id, dedupe_key) WHERE dedupe_key IS NOT NULL;
CREATE INDEX notification_person ON notification (org_id, person_id, created_at DESC, id DESC);
CREATE INDEX notification_person_unread ON notification (org_id, person_id) WHERE read_at IS NULL;
SELECT openbekci_enable_rls('notification');

CREATE TABLE notification_delivery (
    id              uuid        NOT NULL,
    org_id          uuid        NOT NULL REFERENCES organization (id),
    notification_id uuid        NOT NULL,
    channel         text        NOT NULL CHECK (channel IN ('in_app', 'email', 'sms', 'push')),
    -- skipped: the person has no address for the channel, or muted the type
    status          text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed', 'skipped')),
    attempts        int         NOT NULL DEFAULT 0,
    last_error      text,
    provider_id     text,
    -- SMS segments billed (the counter of the site)
    sms_segments    int         NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, notification_id, channel),
    FOREIGN KEY (org_id, notification_id) REFERENCES notification (org_id, id) ON DELETE CASCADE
);
SELECT openbekci_enable_rls('notification_delivery');

-- Site overrides of the default channels of automatic messages; a type without
-- a row uses the built-in default (O5). version belongs to the whole set.
CREATE TABLE site_notification_config (
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    version    int         NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, site_id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('site_notification_config');

CREATE TABLE site_channel_default (
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    type       text        NOT NULL CHECK (type ~ '^[a-z][a-z0-9_.]{0,63}$'),
    channels   text[]      NOT NULL CHECK (channels <@ ARRAY['in_app', 'email', 'sms', 'push']::text[]),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, site_id, type),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('site_channel_default');

CREATE TABLE notification_mute (
    org_id     uuid        NOT NULL REFERENCES organization (id),
    person_id  uuid        NOT NULL,
    type       text        NOT NULL CHECK (type ~ '^[a-z][a-z0-9_.]{0,63}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, person_id, type),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id)
);
SELECT openbekci_enable_rls('notification_mute');

-- SMS messages and billed segments per site and month (the manager's counter).
CREATE TABLE sms_usage (
    org_id   uuid NOT NULL REFERENCES organization (id),
    site_id  uuid NOT NULL,
    month    date NOT NULL CHECK (month = date_trunc('month', month)::date),
    messages int  NOT NULL DEFAULT 0,
    segments int  NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, site_id, month),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('sms_usage');

-- +goose Down
DROP TABLE sms_usage;
DROP TABLE notification_mute;
DROP TABLE site_channel_default;
DROP TABLE site_notification_config;
DROP TABLE notification_delivery;
DROP TABLE notification;
