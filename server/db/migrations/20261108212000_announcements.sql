-- +goose Up

-- Announcements (backend plan 4.10, decisions O4 and O5): one-way notices with
-- a sender-chosen set of channels. The people an announcement reaches are
-- fixed when it is published (announcement_recipient), so a person who moves
-- in later does not receive old notices and the read receipts have a
-- denominator.

CREATE TABLE announcement (
    id               uuid        NOT NULL,
    org_id           uuid        NOT NULL REFERENCES organization (id),
    site_id          uuid        NOT NULL,
    title            text        NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    body_md          text        NOT NULL CHECK (length(body_md) <= 20000),
    status           text        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'scheduled', 'published', 'archived')),
    -- {"kind": "site|block|owners|tenants|units", "block_id": uuid, "unit_ids": [uuid]}
    audience         jsonb       NOT NULL DEFAULT '{"kind":"site"}',
    publish_at       timestamptz,
    expires_at       timestamptz,
    pinned           boolean     NOT NULL DEFAULT false,
    channels         text[]      NOT NULL DEFAULT ARRAY['in_app']::text[] CHECK (channels <@ ARRAY['in_app', 'email', 'sms', 'push']::text[]),
    recipient_count  int,
    sms_count        int,
    published_at     timestamptz,
    created_by       uuid        REFERENCES "user" (id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    version          int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    CHECK (status <> 'scheduled' OR publish_at IS NOT NULL)
);
CREATE INDEX announcement_site ON announcement (org_id, site_id, id DESC);
CREATE INDEX announcement_due ON announcement (publish_at) WHERE status = 'scheduled';
SELECT openbekci_enable_rls('announcement');

CREATE TABLE announcement_file (
    org_id          uuid NOT NULL REFERENCES organization (id),
    announcement_id uuid NOT NULL,
    file_id         uuid NOT NULL,
    position        int  NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, announcement_id, file_id),
    UNIQUE (org_id, file_id),
    FOREIGN KEY (org_id, announcement_id) REFERENCES announcement (org_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, file_id) REFERENCES file (org_id, id)
);
SELECT openbekci_enable_rls('announcement_file');

CREATE TABLE announcement_recipient (
    org_id          uuid NOT NULL REFERENCES organization (id),
    announcement_id uuid NOT NULL,
    person_id       uuid NOT NULL,
    PRIMARY KEY (org_id, announcement_id, person_id),
    FOREIGN KEY (org_id, announcement_id) REFERENCES announcement (org_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id)
);
CREATE INDEX announcement_recipient_person ON announcement_recipient (org_id, person_id);
SELECT openbekci_enable_rls('announcement_recipient');

-- Counted when the announcement is opened in the web or app; a delivered SMS
-- or email is not a read (O4).
CREATE TABLE announcement_read (
    org_id          uuid        NOT NULL REFERENCES organization (id),
    announcement_id uuid        NOT NULL,
    person_id       uuid        NOT NULL,
    read_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, announcement_id, person_id),
    FOREIGN KEY (org_id, announcement_id) REFERENCES announcement (org_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id)
);
SELECT openbekci_enable_rls('announcement_read');

-- +goose Down
DROP TABLE announcement_read;
DROP TABLE announcement_recipient;
DROP TABLE announcement_file;
DROP TABLE announcement;
