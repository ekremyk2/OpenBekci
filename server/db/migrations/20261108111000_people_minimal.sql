-- +goose Up

-- Organization settings the identity and people modules need (backend plan 4.1).
ALTER TABLE organization
    ADD COLUMN country_code   text NOT NULL DEFAULT 'TR',
    ADD COLUMN default_locale text NOT NULL DEFAULT 'tr',
    ADD COLUMN timezone       text NOT NULL DEFAULT 'Europe/Istanbul';

-- Minimal people tables (backend plan 4.3). Phase B extends them (national id,
-- gate contact, directory consent, notes, occupancy, ...).
CREATE TABLE person (
    id           uuid        PRIMARY KEY,
    org_id       uuid        NOT NULL REFERENCES organization (id),
    user_id      uuid        REFERENCES "user" (id),
    display_name text        NOT NULL,
    contact      jsonb       NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    version      int         NOT NULL DEFAULT 1,
    CONSTRAINT person_org_id_id_key UNIQUE (org_id, id)
);
-- A user is at most one person per organization.
CREATE UNIQUE INDEX person_org_user ON person (org_id, user_id) WHERE user_id IS NOT NULL;
CREATE INDEX person_user ON person (user_id) WHERE user_id IS NOT NULL;
SELECT openbekci_enable_rls('person');

-- Role grants (portal rights come from occupancies, added in phase B).
CREATE TABLE membership (
    id          uuid        PRIMARY KEY,
    org_id      uuid        NOT NULL REFERENCES organization (id),
    person_id   uuid        NOT NULL,
    role        text        NOT NULL CHECK (role IN ('org_admin', 'site_manager', 'board_member', 'auditor', 'accountant', 'staff', 'guard')),
    scope_type  text        NOT NULL CHECK (scope_type IN ('org', 'site', 'block')),
    scope_id    uuid,
    valid_from  timestamptz NOT NULL DEFAULT now(),
    valid_to    timestamptz,
    -- board and auditor memberships belong to the site: org admins cannot remove them without a decision (D2)
    site_owned  boolean     NOT NULL DEFAULT false,
    -- the governance decision that granted it (0.2); no FK until that table exists
    decision_id uuid,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    version     int         NOT NULL DEFAULT 1,
    CONSTRAINT membership_person_fk FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id),
    CONSTRAINT membership_scope_ck CHECK ((scope_type = 'org') = (scope_id IS NULL)),
    CONSTRAINT membership_valid_ck CHECK (valid_to IS NULL OR valid_to > valid_from),
    CONSTRAINT membership_org_id_id_key UNIQUE (org_id, id)
);
CREATE INDEX membership_org_person ON membership (org_id, person_id);
SELECT openbekci_enable_rls('membership');

-- +goose Down
DROP TABLE membership;
DROP TABLE person;
ALTER TABLE organization DROP COLUMN timezone, DROP COLUMN default_locale, DROP COLUMN country_code;
