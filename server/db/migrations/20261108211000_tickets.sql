-- +goose Up

-- Tickets (backend plan 4.9, decisions O1 to O3). A site starts with the
-- default workflow and the starter categories, created lazily the first time
-- the site's tickets are touched; editing the workflow only changes rows.

-- One row per site that has a workflow; version covers the status list.
CREATE TABLE ticket_workflow (
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    -- still the built-in open, in progress, done flow
    is_default boolean     NOT NULL DEFAULT true,
    version    int         NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, site_id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('ticket_workflow');

CREATE TABLE ticket_status (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    name       text        NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
    -- open-like statuses count as open work, closed-like ones as finished (reports and counts)
    semantic   text        NOT NULL CHECK (semantic IN ('open_like', 'closed_like')),
    position   int         NOT NULL,
    -- stable key of the built-in statuses: open, in_progress, done
    key        text        CHECK (key IN ('open', 'in_progress', 'done')),
    -- removed from the workflow; kept because tickets and history refer to it
    archived   boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
CREATE UNIQUE INDEX ticket_status_key ON ticket_status (org_id, site_id, key) WHERE key IS NOT NULL;
CREATE INDEX ticket_status_site ON ticket_status (org_id, site_id, position);
SELECT openbekci_enable_rls('ticket_status');

CREATE TABLE ticket_category (
    id                    uuid        NOT NULL,
    org_id                uuid        NOT NULL REFERENCES organization (id),
    site_id               uuid        NOT NULL,
    name                  text        NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    default_visibility    text        NOT NULL DEFAULT 'private' CHECK (default_visibility IN ('private', 'common')),
    default_assignee_role text        CHECK (default_assignee_role IN ('site_manager', 'staff', 'board_member')),
    position              int         NOT NULL DEFAULT 0,
    archived              boolean     NOT NULL DEFAULT false,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    version               int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
CREATE UNIQUE INDEX ticket_category_name ON ticket_category (org_id, site_id, lower(name)) WHERE NOT archived;
SELECT openbekci_enable_rls('ticket_category');

CREATE TABLE ticket (
    id                   uuid        NOT NULL,
    org_id               uuid        NOT NULL REFERENCES organization (id),
    site_id              uuid        NOT NULL,
    unit_id              uuid,
    reporter_person_id   uuid        NOT NULL,
    category_id          uuid        NOT NULL,
    title                text        NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    description          text        NOT NULL DEFAULT '',
    status_id            uuid        NOT NULL,
    -- private: the unit and management; common: the whole site (O3)
    visibility           text        NOT NULL CHECK (visibility IN ('private', 'common')),
    -- a staff person or a vendor label, never both (O2)
    assignee_person_id   uuid,
    assignee_vendor_id   uuid,
    merged_into_id       uuid,
    closed_at            timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    version              int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, reporter_person_id) REFERENCES person (org_id, id),
    FOREIGN KEY (org_id, site_id, category_id) REFERENCES ticket_category (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, status_id) REFERENCES ticket_status (org_id, site_id, id),
    FOREIGN KEY (org_id, assignee_person_id) REFERENCES person (org_id, id),
    FOREIGN KEY (org_id, assignee_vendor_id) REFERENCES vendor (org_id, id),
    FOREIGN KEY (org_id, site_id, merged_into_id) REFERENCES ticket (org_id, site_id, id),
    CHECK (assignee_person_id IS NULL OR assignee_vendor_id IS NULL),
    CHECK (merged_into_id IS NULL OR merged_into_id <> id),
    -- a private ticket belongs to a unit
    CHECK (visibility = 'common' OR unit_id IS NOT NULL)
);
CREATE INDEX ticket_site ON ticket (org_id, site_id, id DESC);
CREATE INDEX ticket_reporter ON ticket (org_id, reporter_person_id);
CREATE INDEX ticket_unit ON ticket (org_id, unit_id) WHERE unit_id IS NOT NULL;
CREATE INDEX ticket_assignee ON ticket (org_id, assignee_person_id) WHERE assignee_person_id IS NOT NULL;
CREATE INDEX ticket_status_idx ON ticket (org_id, site_id, status_id);
SELECT openbekci_enable_rls('ticket');

CREATE TABLE ticket_comment (
    id               uuid        NOT NULL,
    org_id           uuid        NOT NULL REFERENCES organization (id),
    site_id          uuid        NOT NULL,
    ticket_id        uuid        NOT NULL,
    author_person_id uuid        NOT NULL,
    body             text        NOT NULL CHECK (length(body) BETWEEN 1 AND 10000),
    -- internal notes are never shown to residents
    visibility       text        NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'internal')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, ticket_id, id),
    FOREIGN KEY (org_id, site_id, ticket_id) REFERENCES ticket (org_id, site_id, id),
    FOREIGN KEY (org_id, author_person_id) REFERENCES person (org_id, id)
);
CREATE INDEX ticket_comment_ticket ON ticket_comment (org_id, ticket_id, id);
SELECT openbekci_enable_rls('ticket_comment');

CREATE TABLE ticket_attachment (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    ticket_id  uuid        NOT NULL,
    comment_id uuid,
    file_id    uuid        NOT NULL,
    mime       text        NOT NULL,
    size       bigint      NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, file_id),
    FOREIGN KEY (org_id, site_id, ticket_id) REFERENCES ticket (org_id, site_id, id),
    FOREIGN KEY (org_id, ticket_id, comment_id) REFERENCES ticket_comment (org_id, ticket_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, file_id) REFERENCES file (org_id, id)
);
CREATE INDEX ticket_attachment_ticket ON ticket_attachment (org_id, ticket_id);
SELECT openbekci_enable_rls('ticket_attachment');

-- "+1" on a common ticket: one per person.
CREATE TABLE ticket_vote (
    org_id     uuid        NOT NULL REFERENCES organization (id),
    ticket_id  uuid        NOT NULL,
    person_id  uuid        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, ticket_id, person_id),
    FOREIGN KEY (org_id, ticket_id) REFERENCES ticket (org_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id)
);
SELECT openbekci_enable_rls('ticket_vote');

-- Append-only status history.
CREATE TABLE ticket_status_history (
    id             uuid        NOT NULL,
    org_id         uuid        NOT NULL REFERENCES organization (id),
    site_id        uuid        NOT NULL,
    ticket_id      uuid        NOT NULL,
    from_status_id uuid,
    to_status_id   uuid        NOT NULL,
    changed_by     uuid        REFERENCES "user" (id),
    -- display name of the actor when the change was made (the user may leave later)
    changed_by_name text,
    comment        text,
    -- an internal comment is shown only to staff and management
    comment_internal boolean   NOT NULL DEFAULT false,
    at             timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id, ticket_id) REFERENCES ticket (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, to_status_id) REFERENCES ticket_status (org_id, site_id, id)
);
CREATE INDEX ticket_status_history_ticket ON ticket_status_history (org_id, ticket_id, id);
SELECT openbekci_enable_rls('ticket_status_history');

-- +goose Down
DROP TABLE ticket_status_history;
DROP TABLE ticket_vote;
DROP TABLE ticket_attachment;
DROP TABLE ticket_comment;
DROP TABLE ticket;
DROP TABLE ticket_category;
DROP TABLE ticket_status;
DROP TABLE ticket_workflow;
