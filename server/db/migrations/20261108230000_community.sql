-- +goose Up

-- Community (backend plan 4.15, decisions C1 to C4): facilities and bookings,
-- visitors with QR admission and resident approval, recurring visitors, the
-- guard console (PIN switch) and parcels. Same key convention as the other
-- phase B tables: PRIMARY KEY (org_id, id), composite foreign keys that also
-- carry site_id.

CREATE EXTENSION IF NOT EXISTS btree_gist;

-- The facility table was reserved by the tenancy migration; rules is the
-- FacilityRules document (hours, durations, lead time, quota, fee, who may book).
ALTER TABLE facility
    ADD COLUMN description text,
    ADD COLUMN enabled     boolean NOT NULL DEFAULT true,
    ADD CONSTRAINT facility_org_site_id_key UNIQUE (org_id, site_id, id);

-- Dates a manager blocks (maintenance, a private event).
CREATE TABLE facility_block (
    id          uuid        NOT NULL,
    org_id      uuid        NOT NULL REFERENCES organization (id),
    site_id     uuid        NOT NULL,
    facility_id uuid        NOT NULL,
    start_at    timestamptz NOT NULL,
    end_at      timestamptz NOT NULL,
    reason      text,
    created_by  uuid,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id, facility_id) REFERENCES facility (org_id, site_id, id),
    CHECK (end_at > start_at)
);
CREATE INDEX facility_block_facility ON facility_block (org_id, facility_id, start_at);
SELECT openbekci_enable_rls('facility_block');

-- A booking is confirmed instantly when it satisfies the rules (C1); the
-- exclusion constraint is what makes two confirmed bookings of one facility
-- unable to overlap, however the requests race.
CREATE TABLE booking (
    id               uuid        NOT NULL,
    org_id           uuid        NOT NULL REFERENCES organization (id),
    site_id          uuid        NOT NULL,
    facility_id      uuid        NOT NULL,
    unit_id          uuid        NOT NULL,
    person_id        uuid,
    start_at         timestamptz NOT NULL,
    end_at           timestamptz NOT NULL,
    status           text        NOT NULL DEFAULT 'confirmed' CHECK (status IN ('confirmed', 'cancelled')),
    -- the unit side the fee is charged to
    debtor_kind      text        CHECK (debtor_kind IN ('owner', 'tenant')),
    charge_id        uuid,
    fee_amount       bigint      CHECK (fee_amount IS NULL OR fee_amount >= 0),
    fee_currency     text        CHECK (fee_currency IS NULL OR fee_currency ~ '^[A-Z]{3}$'),
    refund_amount    bigint      CHECK (refund_amount IS NULL OR refund_amount >= 0),
    cancelled_reason text,
    cancelled_by     uuid,
    created_by       uuid,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    version          int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id, facility_id) REFERENCES facility (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id),
    CHECK (end_at > start_at),
    CONSTRAINT booking_no_overlap EXCLUDE USING gist (
        org_id WITH =, facility_id WITH =, tstzrange(start_at, end_at, '[)') WITH &&
    ) WHERE (status = 'confirmed')
);
CREATE INDEX booking_unit ON booking (org_id, unit_id, start_at);
CREATE INDEX booking_site_time ON booking (org_id, site_id, start_at);
SELECT openbekci_enable_rls('booking');

-- A standing permission for a named person (cleaner, nanny, carer) with its
-- own long-lived QR. Times are wall-clock in the site's time zone; start > end
-- is a window that crosses midnight and belongs to its start day.
CREATE TABLE recurring_visitor (
    id              uuid        NOT NULL,
    org_id          uuid        NOT NULL REFERENCES organization (id),
    site_id         uuid        NOT NULL,
    unit_id         uuid        NOT NULL,
    name            text        NOT NULL,
    plate           text        CHECK (plate IS NULL OR plate ~ '^[0-9A-Z]{2,12}$'),
    days            smallint[]  NOT NULL CHECK (cardinality(days) BETWEEN 1 AND 7),
    start_time      text        NOT NULL CHECK (start_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
    end_time        text        NOT NULL CHECK (end_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
    valid_from      date        NOT NULL,
    valid_to        date,
    status          text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    -- SHA-256 of the token; the token itself is sealed so the unit's people can show the QR again
    qr_token_hash   bytea       NOT NULL,
    qr_token_sealed bytea,
    registered_by   uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    version         int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, registered_by) REFERENCES person (org_id, id),
    CHECK (valid_to IS NULL OR valid_to >= valid_from)
);
CREATE UNIQUE INDEX recurring_visitor_token ON recurring_visitor (org_id, qr_token_hash);
CREATE INDEX recurring_visitor_unit ON recurring_visitor (org_id, unit_id);
SELECT openbekci_enable_rls('recurring_visitor');

-- A visitor: pre-registered with a QR, a walk-in the guard registered, or one
-- visit of a recurring visitor. Valid for the window plus 30 minutes either
-- side; single entry (C2).
CREATE TABLE visitor_entry (
    id                   uuid        NOT NULL,
    org_id               uuid        NOT NULL REFERENCES organization (id),
    site_id              uuid        NOT NULL,
    unit_id              uuid        NOT NULL,
    name                 text        NOT NULL,
    plate                text        CHECK (plate IS NULL OR plate ~ '^[0-9A-Z]{2,12}$'),
    window_from          timestamptz NOT NULL,
    window_to            timestamptz NOT NULL,
    approval_status      text        NOT NULL CHECK (approval_status IN ('pre_approved', 'requested', 'approved', 'denied', 'expired')),
    source               text        NOT NULL CHECK (source IN ('pre_registered', 'walk_in', 'recurring')),
    qr_token_hash        bytea,
    qr_token_sealed      bytea,
    recurring_visitor_id uuid,
    decided_by           uuid,
    decided_at           timestamptz,
    arrived_at           timestamptz,
    arrived_by           uuid,
    left_at              timestamptz,
    cancelled            boolean     NOT NULL DEFAULT false,
    registered_by        uuid,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    version              int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, recurring_visitor_id) REFERENCES recurring_visitor (org_id, site_id, id),
    FOREIGN KEY (org_id, decided_by) REFERENCES person (org_id, id),
    FOREIGN KEY (org_id, arrived_by) REFERENCES person (org_id, id),
    FOREIGN KEY (org_id, registered_by) REFERENCES person (org_id, id),
    CHECK (window_to >= window_from),
    CHECK (source <> 'pre_registered' OR (qr_token_hash IS NOT NULL AND recurring_visitor_id IS NULL)),
    CHECK (source <> 'recurring' OR recurring_visitor_id IS NOT NULL)
);
CREATE UNIQUE INDEX visitor_entry_token ON visitor_entry (org_id, qr_token_hash) WHERE qr_token_hash IS NOT NULL;
CREATE INDEX visitor_entry_site_window ON visitor_entry (org_id, site_id, window_from);
CREATE INDEX visitor_entry_unit ON visitor_entry (org_id, unit_id, window_from);
CREATE INDEX visitor_entry_recurring ON visitor_entry (org_id, recurring_visitor_id) WHERE recurring_visitor_id IS NOT NULL;
SELECT openbekci_enable_rls('visitor_entry');

-- The approve / deny request sent to the unit's occupants for an unexpected
-- visitor. The first answer of any occupant decides.
CREATE TABLE visitor_approval_request (
    id           uuid        NOT NULL,
    org_id       uuid        NOT NULL REFERENCES organization (id),
    site_id      uuid        NOT NULL,
    entry_id     uuid        NOT NULL,
    unit_id      uuid        NOT NULL,
    requested_by uuid,
    requested_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    status       text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'expired')),
    decided_by   uuid,
    decided_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, entry_id),
    FOREIGN KEY (org_id, site_id, entry_id) REFERENCES visitor_entry (org_id, site_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, requested_by) REFERENCES person (org_id, id),
    FOREIGN KEY (org_id, decided_by) REFERENCES person (org_id, id)
);
CREATE INDEX visitor_approval_unit ON visitor_approval_request (org_id, unit_id, status);
CREATE INDEX visitor_approval_pending ON visitor_approval_request (org_id, expires_at) WHERE status = 'pending';
SELECT openbekci_enable_rls('visitor_approval_request');

-- A parcel logged by the guard against a unit (C3).
CREATE TABLE delivery (
    id                uuid        NOT NULL,
    org_id            uuid        NOT NULL REFERENCES organization (id),
    site_id           uuid        NOT NULL,
    unit_id           uuid        NOT NULL,
    received_at       timestamptz NOT NULL DEFAULT now(),
    received_by       uuid,
    description       text,
    collected_at      timestamptz,
    collected_by_name text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, received_by) REFERENCES person (org_id, id),
    CHECK ((collected_at IS NULL) = (collected_by_name IS NULL))
);
CREATE INDEX delivery_unit ON delivery (org_id, unit_id, received_at);
CREATE INDEX delivery_waiting ON delivery (org_id, site_id, received_at) WHERE collected_at IS NULL;
SELECT openbekci_enable_rls('delivery');

-- The quick PIN of a guard (per person), stored hashed. attempts_from is moved
-- forward when a manager sets the PIN: failures before it do not count.
CREATE TABLE gate_pin (
    org_id        uuid        NOT NULL REFERENCES organization (id),
    person_id     uuid        NOT NULL,
    pin_hash      text        NOT NULL,
    attempts_from timestamptz NOT NULL DEFAULT now(),
    set_by        uuid,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, person_id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id)
);
SELECT openbekci_enable_rls('gate_pin');

-- Every PIN attempt (the lock is derived from the recent failures, so it holds
-- across replicas); pruned with the visitor log.
CREATE TABLE gate_pin_attempt (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    person_id  uuid,
    -- the console login session (family) the attempt came from
    console_id uuid        NOT NULL,
    ok         boolean     NOT NULL,
    at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
CREATE INDEX gate_pin_attempt_person ON gate_pin_attempt (org_id, site_id, person_id, at);
CREATE INDEX gate_pin_attempt_console ON gate_pin_attempt (org_id, console_id, at);
SELECT openbekci_enable_rls('gate_pin_attempt');

-- A guard at the booth: bound to the console login session and the site.
CREATE TABLE guard_session (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    person_id  uuid        NOT NULL,
    console_id uuid        NOT NULL,
    token_hash bytea       NOT NULL,
    expires_at timestamptz NOT NULL,
    ended_at   timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id)
);
CREATE UNIQUE INDEX guard_session_token ON guard_session (token_hash);
CREATE INDEX guard_session_console ON guard_session (org_id, console_id) WHERE ended_at IS NULL;
SELECT openbekci_enable_rls('guard_session');

-- +goose Down
DROP TABLE guard_session;
DROP TABLE gate_pin_attempt;
DROP TABLE gate_pin;
DROP TABLE delivery;
DROP TABLE visitor_approval_request;
DROP TABLE visitor_entry;
DROP TABLE recurring_visitor;
DROP TABLE booking;
DROP TABLE facility_block;
ALTER TABLE facility DROP CONSTRAINT facility_org_site_id_key, DROP COLUMN enabled, DROP COLUMN description;
