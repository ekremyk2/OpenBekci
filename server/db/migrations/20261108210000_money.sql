-- +goose Up

-- Billing, late fees, opening balances and payments (backend plan 4.5, 4.6).
-- Every table: PRIMARY KEY (org_id, id), RLS, composite foreign keys with site_id.

CREATE TABLE charge_plan (
    id               uuid        NOT NULL,
    org_id           uuid        NOT NULL REFERENCES organization (id),
    site_id          uuid        NOT NULL,
    name             text        NOT NULL,
    kind             text        NOT NULL DEFAULT 'recurring' CHECK (kind IN ('recurring', 'one_off')),
    charge_type      text        NOT NULL DEFAULT 'dues' CHECK (charge_type IN ('dues', 'special_assessment', 'reserve_contribution', 'utility', 'booking_fee', 'legal_cost', 'deposit', 'adjustment', 'settlement')),
    default_debtor   text        NOT NULL DEFAULT 'owner' CHECK (default_debtor IN ('owner', 'tenant')),
    frequency        text        NOT NULL DEFAULT 'monthly' CHECK (frequency IN ('monthly', 'quarterly', 'yearly')),
    due_day          int         NOT NULL DEFAULT 1 CHECK (due_day BETWEEN 1 AND 31),
    start_on         date        NOT NULL,
    end_on           date,
    amount_mode      text        NOT NULL DEFAULT 'per_unit' CHECK (amount_mode IN ('per_unit', 'total_allocated')),
    allocation_key   text,
    -- site, block:<uuid>, tag:<tag>
    scope            text        NOT NULL DEFAULT 'site',
    fund_id          uuid        NOT NULL,
    income_account_id uuid,
    status           text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'archived')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    version          int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    CHECK (amount_mode = 'per_unit' OR allocation_key IS NOT NULL),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, fund_id) REFERENCES fund (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, income_account_id) REFERENCES account (org_id, site_id, id)
);
SELECT openbekci_enable_rls('charge_plan');

CREATE TABLE charge_plan_rate (
    id          uuid        NOT NULL,
    org_id      uuid        NOT NULL REFERENCES organization (id),
    site_id     uuid        NOT NULL,
    plan_id     uuid        NOT NULL,
    from_on     date        NOT NULL,
    to_on       date,
    amount      bigint      NOT NULL CHECK (amount >= 0),
    decision_id uuid,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    CHECK (to_on IS NULL OR to_on >= from_on),
    FOREIGN KEY (org_id, site_id, plan_id) REFERENCES charge_plan (org_id, site_id, id)
);
CREATE INDEX charge_plan_rate_plan ON charge_plan_rate (org_id, plan_id, from_on);
SELECT openbekci_enable_rls('charge_plan_rate');

-- Money owed. kind principal or fee (late fee). debtor_kind person: debt carried to a person after move-out (S7).
CREATE TABLE charge (
    id                uuid        NOT NULL,
    org_id            uuid        NOT NULL REFERENCES organization (id),
    site_id           uuid        NOT NULL,
    plan_id           uuid,
    unit_id           uuid        NOT NULL,
    period            text        NOT NULL DEFAULT '',
    kind              text        NOT NULL DEFAULT 'principal' CHECK (kind IN ('principal', 'fee')),
    charge_type       text        NOT NULL,
    debtor_kind       text        NOT NULL CHECK (debtor_kind IN ('owner', 'tenant', 'person')),
    debtor_person_id  uuid,
    -- negative only for the reversal of a late fee
    amount            bigint      NOT NULL,
    due_date          date        NOT NULL,
    description       text        NOT NULL DEFAULT '',
    allocation_basis  jsonb,
    fund_id           uuid        NOT NULL,
    journal_entry_id  uuid        NOT NULL,
    cancel_entry_id   uuid,
    -- derived from payment_allocation (rebuildable): open, partially_paid, paid, cancelled
    status            text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'partially_paid', 'paid', 'cancelled')),
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    CHECK (debtor_kind <> 'person' OR debtor_person_id IS NOT NULL),
    FOREIGN KEY (org_id, site_id, plan_id) REFERENCES charge_plan (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, debtor_person_id) REFERENCES person (org_id, id),
    FOREIGN KEY (org_id, site_id, fund_id) REFERENCES fund (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, journal_entry_id) REFERENCES journal_entry (org_id, site_id, id)
);
-- one principal charge per (plan, unit, period): generation is idempotent
CREATE UNIQUE INDEX charge_plan_unit_period ON charge (org_id, plan_id, unit_id, period) WHERE plan_id IS NOT NULL AND kind = 'principal';
CREATE INDEX charge_unit_side ON charge (org_id, site_id, unit_id, debtor_kind);
CREATE INDEX charge_person ON charge (org_id, debtor_person_id) WHERE debtor_person_id IS NOT NULL;
SELECT openbekci_enable_rls('charge');

CREATE TABLE opening_balance (
    id               uuid        NOT NULL,
    org_id           uuid        NOT NULL REFERENCES organization (id),
    site_id          uuid        NOT NULL,
    as_of            date        NOT NULL,
    kind             text        NOT NULL CHECK (kind IN ('unit_receivable', 'bank', 'cash', 'vendor')),
    unit_id          uuid,
    debtor_kind      text        CHECK (debtor_kind IN ('owner', 'tenant')),
    account_id       uuid,
    principal        bigint      NOT NULL DEFAULT 0,
    late_fee         bigint      NOT NULL DEFAULT 0 CHECK (late_fee >= 0),
    journal_entry_id uuid        NOT NULL,
    entry_ids        uuid[]      NOT NULL DEFAULT '{}',
    import_row_id    uuid,
    voided_at        timestamptz,
    void_entry_id    uuid,
    created_by       uuid,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, journal_entry_id) REFERENCES journal_entry (org_id, site_id, id)
);
CREATE UNIQUE INDEX opening_balance_import_row ON opening_balance (org_id, import_row_id) WHERE import_row_id IS NOT NULL AND voided_at IS NULL;
SELECT openbekci_enable_rls('opening_balance');

-- The site's late fee policy schedule (a new row takes effect from from_on; cap 500 bps per month).
CREATE TABLE late_fee_policy (
    id                 uuid        NOT NULL,
    org_id             uuid        NOT NULL REFERENCES organization (id),
    site_id            uuid        NOT NULL,
    from_on            date        NOT NULL,
    rate_bps_per_month int         NOT NULL CHECK (rate_bps_per_month BETWEEN 0 AND 500),
    grace_days         int         NOT NULL DEFAULT 0 CHECK (grace_days >= 0),
    created_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, from_on),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('late_fee_policy');

CREATE TABLE late_fee_booking (
    id           uuid        NOT NULL,
    org_id       uuid        NOT NULL REFERENCES organization (id),
    site_id      uuid        NOT NULL,
    unit_id      uuid        NOT NULL,
    debtor_kind  text        NOT NULL CHECK (debtor_kind IN ('owner', 'tenant')),
    -- the crystallization month
    period       text        NOT NULL,
    kind         text        NOT NULL CHECK (kind IN ('fee', 'reversal', 'waiver')),
    amount       bigint      NOT NULL,
    charge_id    uuid,
    entry_id     uuid        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, charge_id) REFERENCES charge (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, entry_id) REFERENCES journal_entry (org_id, site_id, id)
);
CREATE INDEX late_fee_booking_side ON late_fee_booking (org_id, site_id, unit_id, debtor_kind);
SELECT openbekci_enable_rls('late_fee_booking');

CREATE TABLE late_fee_waiver (
    id          uuid        NOT NULL,
    org_id      uuid        NOT NULL REFERENCES organization (id),
    site_id     uuid        NOT NULL,
    unit_id     uuid        NOT NULL,
    debtor_kind text        NOT NULL CHECK (debtor_kind IN ('owner', 'tenant')),
    amount      bigint      NOT NULL CHECK (amount > 0),
    reason      text        NOT NULL,
    by_user_id  uuid,
    entry_id    uuid        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, entry_id) REFERENCES journal_entry (org_id, site_id, id)
);
SELECT openbekci_enable_rls('late_fee_waiver');

CREATE TABLE late_fee_freeze (
    id          uuid        NOT NULL,
    org_id      uuid        NOT NULL REFERENCES organization (id),
    site_id     uuid        NOT NULL,
    unit_id     uuid        NOT NULL,
    debtor_kind text        NOT NULL CHECK (debtor_kind IN ('owner', 'tenant')),
    from_on     date        NOT NULL,
    to_on       date,
    reason      text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    CHECK (to_on IS NULL OR to_on >= from_on),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id)
);
SELECT openbekci_enable_rls('late_fee_freeze');

CREATE TABLE payment (
    id                 uuid        NOT NULL,
    org_id             uuid        NOT NULL REFERENCES organization (id),
    site_id            uuid        NOT NULL,
    -- the debtor: a unit side or a person
    unit_id            uuid,
    debtor_kind        text        CHECK (debtor_kind IN ('owner', 'tenant', 'person')),
    debtor_person_id   uuid,
    payer_person_id    uuid,
    method             text        NOT NULL CHECK (method IN ('cash', 'bank_transfer')),
    gross              bigint      NOT NULL CHECK (gross > 0),
    fee                bigint      NOT NULL DEFAULT 0 CHECK (fee >= 0),
    net                bigint      NOT NULL CHECK (net > 0),
    received_on        date        NOT NULL,
    recorded_by        uuid,
    asset_account_id   uuid        NOT NULL,
    reference          text,
    status             text        NOT NULL DEFAULT 'confirmed' CHECK (status IN ('pending_declared', 'confirmed', 'refunded', 'voided')),
    receipt_no         text,
    journal_entry_id   uuid,
    void_entry_id      uuid,
    created_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    CHECK (net = gross - fee),
    CHECK (unit_id IS NOT NULL OR debtor_person_id IS NOT NULL),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, debtor_person_id) REFERENCES person (org_id, id),
    FOREIGN KEY (org_id, site_id, asset_account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, journal_entry_id) REFERENCES journal_entry (org_id, site_id, id)
);
CREATE UNIQUE INDEX payment_receipt_no ON payment (org_id, site_id, receipt_no) WHERE receipt_no IS NOT NULL;
CREATE INDEX payment_unit_side ON payment (org_id, site_id, unit_id, debtor_kind);
SELECT openbekci_enable_rls('payment');

-- payment_id is null for an allocation of credit (prepayment) to a charge.
CREATE TABLE payment_allocation (
    id          uuid        NOT NULL,
    org_id      uuid        NOT NULL REFERENCES organization (id),
    site_id     uuid        NOT NULL,
    payment_id  uuid,
    charge_id   uuid        NOT NULL,
    amount      bigint      NOT NULL CHECK (amount > 0),
    applied_on  date        NOT NULL,
    voided      boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id, payment_id) REFERENCES payment (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, charge_id) REFERENCES charge (org_id, site_id, id)
);
CREATE INDEX payment_allocation_charge ON payment_allocation (org_id, charge_id) WHERE NOT voided;
CREATE INDEX payment_allocation_payment ON payment_allocation (org_id, payment_id);
SELECT openbekci_enable_rls('payment_allocation');

-- +goose Down
DROP TABLE payment_allocation;
DROP TABLE payment;
DROP TABLE late_fee_freeze;
DROP TABLE late_fee_waiver;
DROP TABLE late_fee_booking;
DROP TABLE late_fee_policy;
DROP TABLE opening_balance;
DROP TABLE charge;
DROP TABLE charge_plan_rate;
DROP TABLE charge_plan;
