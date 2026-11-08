-- +goose Up

-- Transfers, refunds, expenses, bills and budgets (backend plan 4.7). Same conventions as the
-- ledger: PRIMARY KEY (org_id, id), RLS, composite foreign keys with site_id.

CREATE TABLE transfer (
    id              uuid        NOT NULL,
    org_id          uuid        NOT NULL REFERENCES organization (id),
    site_id         uuid        NOT NULL,
    from_account_id uuid        NOT NULL,
    to_account_id   uuid        NOT NULL,
    amount          bigint      NOT NULL CHECK (amount > 0),
    transferred_on  date        NOT NULL,
    memo            text,
    kind            text        NOT NULL CHECK (kind IN ('cash_handover', 'bank_deposit', 'other')),
    journal_entry_id uuid       NOT NULL,
    created_by      uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    CHECK (from_account_id <> to_account_id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, from_account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, to_account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, journal_entry_id) REFERENCES journal_entry (org_id, site_id, id)
);
CREATE INDEX transfer_site_day ON transfer (org_id, site_id, transferred_on DESC, id);
SELECT openbekci_enable_rls('transfer');

CREATE TABLE refund (
    id                   uuid        NOT NULL,
    org_id               uuid        NOT NULL REFERENCES organization (id),
    site_id              uuid        NOT NULL,
    person_id            uuid,
    unit_id              uuid,
    debtor_kind          text        CHECK (debtor_kind IN ('owner', 'tenant')),
    amount               bigint      NOT NULL CHECK (amount > 0),
    reason               text        NOT NULL,
    kind                 text        NOT NULL CHECK (kind IN ('overpayment', 'deposit_return', 'other')),
    paid_from_account_id uuid        NOT NULL,
    refunded_on          date        NOT NULL,
    credit_id            uuid,
    payment_id           uuid,
    journal_entry_id     uuid        NOT NULL,
    created_by           uuid,
    created_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    CHECK ((unit_id IS NULL) = (debtor_kind IS NULL)),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, paid_from_account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, journal_entry_id) REFERENCES journal_entry (org_id, site_id, id)
);
CREATE INDEX refund_site_day ON refund (org_id, site_id, refunded_on DESC, id);
SELECT openbekci_enable_rls('refund');

-- A recurring expense: the hourly expenses.recurring job posts an expense on its day every month.
CREATE TABLE recurring_expense (
    id                   uuid        NOT NULL,
    org_id               uuid        NOT NULL REFERENCES organization (id),
    site_id              uuid        NOT NULL,
    category_account_id  uuid        NOT NULL,
    fund_id              uuid        NOT NULL,
    amount               bigint      NOT NULL CHECK (amount > 0),
    paid_from_account_id uuid        NOT NULL,
    vendor_id            uuid,
    description          text        NOT NULL,
    document_no          text,
    frequency            text        NOT NULL DEFAULT 'monthly' CHECK (frequency IN ('monthly')),
    day_of_month         int         NOT NULL CHECK (day_of_month BETWEEN 1 AND 31),
    next_on              date        NOT NULL,
    active               boolean     NOT NULL DEFAULT true,
    created_by           uuid,
    created_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, category_account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, fund_id) REFERENCES fund (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, paid_from_account_id) REFERENCES account (org_id, site_id, id)
);
CREATE INDEX recurring_expense_due ON recurring_expense (next_on) WHERE active;
SELECT openbekci_enable_rls('recurring_expense');

CREATE TABLE expense (
    id                   uuid        NOT NULL,
    org_id               uuid        NOT NULL REFERENCES organization (id),
    site_id              uuid        NOT NULL,
    category_account_id  uuid        NOT NULL,
    fund_id              uuid        NOT NULL,
    amount               bigint      NOT NULL CHECK (amount > 0),
    paid_on              date        NOT NULL,
    paid_from_account_id uuid        NOT NULL,
    vendor_id            uuid,
    description          text        NOT NULL,
    document_no          text,
    file_id              uuid,
    recurring_expense_id uuid,
    status               text        NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'voided')),
    journal_entry_id     uuid        NOT NULL,
    void_entry_id        uuid,
    voided_reason        text,
    voided_at            timestamptz,
    created_by           uuid,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, category_account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, fund_id) REFERENCES fund (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, paid_from_account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, journal_entry_id) REFERENCES journal_entry (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, recurring_expense_id) REFERENCES recurring_expense (org_id, site_id, id),
    FOREIGN KEY (org_id, file_id) REFERENCES file (org_id, id)
);
CREATE INDEX expense_site_day ON expense (org_id, site_id, paid_on DESC, id);
-- one expense per recurrence and day: the catch-up job is idempotent
CREATE UNIQUE INDEX expense_recurrence_day ON expense (org_id, recurring_expense_id, paid_on) WHERE recurring_expense_id IS NOT NULL;
SELECT openbekci_enable_rls('expense');

-- Bills (site setting bills_enabled): a payable booked on the issue date, paid by bill payments.
CREATE TABLE bill (
    id                  uuid        NOT NULL,
    org_id              uuid        NOT NULL REFERENCES organization (id),
    site_id             uuid        NOT NULL,
    vendor_id           uuid        NOT NULL,
    document_no         text,
    issue_date          date        NOT NULL,
    due_date            date        NOT NULL,
    amount              bigint      NOT NULL CHECK (amount > 0),
    category_account_id uuid        NOT NULL,
    fund_id             uuid        NOT NULL,
    file_id             uuid,
    description         text,
    status              text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'partially_paid', 'paid', 'cancelled')),
    journal_entry_id    uuid        NOT NULL,
    created_by          uuid,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    version             int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    CHECK (due_date >= issue_date),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, category_account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, fund_id) REFERENCES fund (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, journal_entry_id) REFERENCES journal_entry (org_id, site_id, id),
    FOREIGN KEY (org_id, file_id) REFERENCES file (org_id, id)
);
CREATE INDEX bill_site_due ON bill (org_id, site_id, due_date, id);
SELECT openbekci_enable_rls('bill');

CREATE TABLE bill_payment (
    id                   uuid        NOT NULL,
    org_id               uuid        NOT NULL REFERENCES organization (id),
    site_id              uuid        NOT NULL,
    bill_id              uuid        NOT NULL,
    amount               bigint      NOT NULL CHECK (amount > 0),
    paid_on              date        NOT NULL,
    paid_from_account_id uuid        NOT NULL,
    reference            text,
    journal_entry_id     uuid        NOT NULL,
    created_by           uuid,
    created_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id, bill_id) REFERENCES bill (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, paid_from_account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, journal_entry_id) REFERENCES journal_entry (org_id, site_id, id)
);
CREATE INDEX bill_payment_bill ON bill_payment (org_id, bill_id, paid_on);
SELECT openbekci_enable_rls('bill_payment');

-- Budgets: one per fiscal year, a line per expense category and fund (M3).
CREATE TABLE budget (
    id             uuid        NOT NULL,
    org_id         uuid        NOT NULL REFERENCES organization (id),
    site_id        uuid        NOT NULL,
    fiscal_year_id uuid        NOT NULL,
    status         text        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'final')),
    decision_id    uuid,
    created_by     uuid,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    version        int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    UNIQUE (org_id, site_id, fiscal_year_id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, fiscal_year_id) REFERENCES fiscal_year (org_id, site_id, id)
);
SELECT openbekci_enable_rls('budget');

CREATE TABLE budget_line (
    org_id                      uuid   NOT NULL REFERENCES organization (id),
    site_id                     uuid   NOT NULL,
    budget_id                   uuid   NOT NULL,
    expense_category_account_id uuid   NOT NULL,
    fund_id                     uuid   NOT NULL,
    amount                      bigint NOT NULL CHECK (amount >= 0),
    PRIMARY KEY (org_id, budget_id, expense_category_account_id, fund_id),
    FOREIGN KEY (org_id, site_id, budget_id) REFERENCES budget (org_id, site_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, site_id, expense_category_account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, fund_id) REFERENCES fund (org_id, site_id, id)
);
SELECT openbekci_enable_rls('budget_line');

-- +goose Down
DROP TABLE budget_line;
DROP TABLE budget;
DROP TABLE bill_payment;
DROP TABLE bill;
DROP TABLE expense;
DROP TABLE recurring_expense;
DROP TABLE refund;
DROP TABLE transfer;
