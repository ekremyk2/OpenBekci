-- +goose Up

-- People (backend plan 4.3). Ids are unique per organization (PRIMARY KEY
-- (org_id, id)) so a site's people can be handed over to another organization
-- with their identifiers preserved; see 20261108170000_tenancy.sql.
ALTER TABLE person DROP CONSTRAINT person_pkey;
ALTER TABLE person ADD PRIMARY KEY (org_id, id);
ALTER TABLE membership DROP CONSTRAINT membership_pkey;
ALTER TABLE membership ADD PRIMARY KEY (org_id, id);

ALTER TABLE person
    -- AES-256-GCM sealed (platform/crypto field encryption); never in audit diffs, masked in reads (S6)
    ADD COLUMN national_id_enc     bytea,
    ADD COLUMN national_id_country text CHECK (national_id_country IS NULL OR national_id_country ~ '^[A-Z]{2}$'),
    ADD COLUMN gate_contact_phone  text,
    ADD COLUMN notes               text,
    -- the site that registered the person: what makes them visible to that site's managers
    ADD COLUMN site_id             uuid,
    ADD CONSTRAINT person_site_fk FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    ADD CONSTRAINT person_national_id_country_ck CHECK ((national_id_enc IS NULL) = (national_id_country IS NULL));

-- Each person's choice of what the site directory shows about them (S8).
CREATE TABLE directory_consent (
    org_id      uuid        NOT NULL REFERENCES organization (id),
    site_id     uuid        NOT NULL,
    person_id   uuid        NOT NULL,
    id          uuid        NOT NULL,
    share_name  boolean     NOT NULL DEFAULT false,
    share_phone boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    version     int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, site_id, person_id),
    UNIQUE (org_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id)
);
SELECT openbekci_enable_rls('directory_consent');

-- Portal rights derive from this table only (S3). valid_to is exclusive: the
-- first day the person is no longer there (the move-out date).
CREATE TABLE occupancy (
    id               uuid         NOT NULL,
    org_id           uuid         NOT NULL REFERENCES organization (id),
    site_id          uuid         NOT NULL,
    unit_id          uuid         NOT NULL,
    person_id        uuid         NOT NULL,
    kind             text         NOT NULL CHECK (kind IN ('owner', 'tenant', 'household')),
    -- the unit side the occupancy belongs to; a household member sits on the side of the owner or tenant they live with
    side             text         NOT NULL CHECK (side IN ('owner', 'tenant')),
    -- co-owners: percentages of one unit's owner side sum to at most 100
    share_pct        numeric(6,3) CHECK (share_pct IS NULL OR (share_pct > 0 AND share_pct <= 100)),
    is_correspondent boolean      NOT NULL DEFAULT false,
    -- household members see money only when the owner or tenant grants it (S3)
    may_see_money    boolean      NOT NULL DEFAULT false,
    -- lives in the unit; an owner who lives elsewhere has false. Vacancy is derived from this.
    resides          boolean      NOT NULL DEFAULT true,
    valid_from       date         NOT NULL,
    valid_to         date,
    created_at       timestamptz  NOT NULL DEFAULT now(),
    updated_at       timestamptz  NOT NULL DEFAULT now(),
    version          int          NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id),
    CHECK (valid_to IS NULL OR valid_to > valid_from),
    CHECK (kind <> 'owner' OR side = 'owner'),
    CHECK (kind <> 'tenant' OR side = 'tenant'),
    CHECK (share_pct IS NULL OR kind = 'owner'),
    CHECK (kind <> 'household' OR NOT is_correspondent),
    CHECK (kind = 'household' OR NOT may_see_money)
);
CREATE INDEX occupancy_org_unit ON occupancy (org_id, unit_id);
CREATE INDEX occupancy_org_person ON occupancy (org_id, person_id);
-- one correspondent per unit side among the open occupancies
CREATE UNIQUE INDEX occupancy_one_correspondent ON occupancy (org_id, unit_id, side) WHERE is_correspondent AND valid_to IS NULL;
SELECT openbekci_enable_rls('occupancy');

-- The move-out / transfer use case (S7). The closing statement and the debt
-- certificate are placeholders until the money module fills them (phase C).
CREATE TABLE move_out (
    id                         uuid        NOT NULL,
    org_id                     uuid        NOT NULL REFERENCES organization (id),
    site_id                    uuid        NOT NULL,
    occupancy_id               uuid        NOT NULL,
    unit_id                    uuid        NOT NULL,
    person_id                  uuid        NOT NULL,
    as_of                      date        NOT NULL,
    -- what happens to the leaving person's open charges: carried to the person's own receivable (default), kept on the unit, or written off
    open_charges               text        NOT NULL DEFAULT 'carry_to_person' CHECK (open_charges IN ('carry_to_person', 'keep_on_unit', 'write_off')),
    -- filled by the money module (phase C): the amount carried (minor units)
    carried_amount_minor       bigint,
    carried_currency           text,
    closing_statement_file_id  uuid,
    certificate_file_id        uuid,
    closing_statement_status   text        NOT NULL DEFAULT 'pending' CHECK (closing_statement_status IN ('pending', 'ready')),
    certificate_status         text        NOT NULL DEFAULT 'pending' CHECK (certificate_status IN ('pending', 'ready')),
    successor_occupancy_id     uuid,
    decided_by                 uuid,
    notes                      text,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    version                    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, occupancy_id),
    FOREIGN KEY (org_id, site_id, occupancy_id) REFERENCES occupancy (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, successor_occupancy_id) REFERENCES occupancy (org_id, site_id, id)
);
SELECT openbekci_enable_rls('move_out');

-- Suppliers and labels for ticket assignment (site_id null: shared by the organization's sites).
CREATE TABLE vendor (
    id                         uuid        NOT NULL,
    org_id                     uuid        NOT NULL REFERENCES organization (id),
    site_id                    uuid,
    name                       text        NOT NULL,
    tax_id                     text,
    contact                    jsonb       NOT NULL DEFAULT '{}',
    -- no foreign key until the expense categories exist (phase C)
    default_expense_category_id uuid,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    version                    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
CREATE INDEX vendor_org_site ON vendor (org_id, site_id);
SELECT openbekci_enable_rls('vendor');

-- Invitations (S4, backend plan 6): manager (to a person the manager added),
-- household (from an owner or tenant, to their own unit) and site_code (the
-- site's QR / join code, multi-use, claims need approval). Tokens are
-- "<org>.<random>"; only their SHA-256 is stored, except the site code which
-- the manager must be able to show again.
CREATE TABLE invitation (
    id               uuid        NOT NULL,
    org_id           uuid        NOT NULL REFERENCES organization (id),
    site_id          uuid        NOT NULL,
    kind             text        NOT NULL CHECK (kind IN ('manager', 'household', 'site_code')),
    unit_id          uuid,
    person_id        uuid,
    -- what accepting creates (null for site_code: the claim chooses)
    occupancy_kind   text        CHECK (occupancy_kind IN ('owner', 'tenant', 'household')),
    occupancy_side   text        CHECK (occupancy_side IN ('owner', 'tenant')),
    share_pct        numeric(6,3),
    is_correspondent boolean     NOT NULL DEFAULT false,
    resides          boolean     NOT NULL DEFAULT true,
    valid_from       date,
    channel          text        NOT NULL CHECK (channel IN ('sms', 'email', 'link')),
    email            text,
    phone            text,
    locale           text        NOT NULL DEFAULT 'tr',
    token_hash       bytea,
    code             text,
    status           text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'revoked', 'expired')),
    expires_at       timestamptz,
    max_uses         int,
    uses             int         NOT NULL DEFAULT 0,
    send_count       int         NOT NULL DEFAULT 0,
    last_sent_at     timestamptz,
    invited_by       uuid,
    accepted_by      uuid,
    accepted_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    version          int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id),
    CHECK ((kind = 'site_code') = (code IS NOT NULL)),
    CHECK (kind = 'site_code' OR token_hash IS NOT NULL),
    CHECK (kind <> 'household' OR (occupancy_kind = 'household' AND unit_id IS NOT NULL))
);
CREATE UNIQUE INDEX invitation_token_hash ON invitation (token_hash) WHERE token_hash IS NOT NULL;
CREATE UNIQUE INDEX invitation_code ON invitation (code) WHERE code IS NOT NULL;
CREATE INDEX invitation_org_site_status ON invitation (org_id, site_id, status);
SELECT openbekci_enable_rls('invitation');

-- A resident who signed up through the site code asks for a unit; the manager
-- approves before an occupancy exists.
CREATE TABLE unit_claim (
    id              uuid        NOT NULL,
    org_id          uuid        NOT NULL REFERENCES organization (id),
    site_id         uuid        NOT NULL,
    unit_id         uuid        NOT NULL,
    person_id       uuid        NOT NULL,
    user_id         uuid        NOT NULL REFERENCES "user" (id),
    invitation_id   uuid,
    kind            text        NOT NULL CHECK (kind IN ('owner', 'tenant', 'household')),
    status          text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    message         text,
    decided_by      uuid,
    decided_at      timestamptz,
    decision_note   text,
    occupancy_id    uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    version         int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id),
    FOREIGN KEY (org_id, site_id, occupancy_id) REFERENCES occupancy (org_id, site_id, id)
);
CREATE UNIQUE INDEX unit_claim_one_pending ON unit_claim (org_id, person_id, unit_id, kind) WHERE status = 'pending';
CREATE INDEX unit_claim_org_site_status ON unit_claim (org_id, site_id, status);
SELECT openbekci_enable_rls('unit_claim');

-- +goose Down
-- UNSUPPORTED after a site hand-over: ADD PRIMARY KEY (id) below fails once the same person or
-- membership id exists in two organizations (identifiers are preserved across hand-overs). Restore from backup instead.
DROP TABLE unit_claim;
DROP TABLE invitation;
DROP TABLE vendor;
DROP TABLE move_out;
DROP TABLE occupancy;
DROP TABLE directory_consent;
ALTER TABLE person DROP CONSTRAINT person_national_id_country_ck,
    DROP COLUMN site_id, DROP COLUMN notes, DROP COLUMN gate_contact_phone, DROP COLUMN national_id_country, DROP COLUMN national_id_enc;
ALTER TABLE membership DROP CONSTRAINT membership_pkey;
ALTER TABLE membership ADD PRIMARY KEY (id);
ALTER TABLE person DROP CONSTRAINT person_pkey;
ALTER TABLE person ADD PRIMARY KEY (id);
