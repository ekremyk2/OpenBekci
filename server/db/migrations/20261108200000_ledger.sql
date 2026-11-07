-- +goose Up

-- Ledger (backend plan 4.4): funds, chart of accounts, cash boxes, fiscal years
-- and the append-only double-entry journal. Keys follow phase B: PRIMARY KEY
-- (org_id, id), composite foreign keys carrying org_id and site_id.

CREATE TABLE fund (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    code       text        NOT NULL,
    name       text        NOT NULL,
    kind       text        NOT NULL DEFAULT 'custom' CHECK (kind IN ('operating', 'reserve', 'custom')),
    is_default boolean     NOT NULL DEFAULT false,
    archived   boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    UNIQUE (org_id, site_id, code),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('fund');

CREATE TABLE account (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    code       text        NOT NULL,
    name       text        NOT NULL,
    type       text        NOT NULL CHECK (type IN ('asset', 'liability', 'equity', 'income', 'expense')),
    -- bank, cash_box, receivable_owner, receivable_tenant, person_receivable, prepayment_liability, deposit_liability,
    -- dues_contribution, late_fee_income, other_income, expense_category, opening_equity, prior_year_surplus, ...
    subtype    text,
    fund_id    uuid,
    is_system  boolean     NOT NULL DEFAULT false,
    archived   boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    UNIQUE (org_id, site_id, code),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, fund_id) REFERENCES fund (org_id, site_id, id)
);
CREATE INDEX account_site_subtype ON account (org_id, site_id, subtype);
SELECT openbekci_enable_rls('account');

-- An asset account bound to a collector person (M5).
CREATE TABLE cash_box (
    id                  uuid        NOT NULL,
    org_id              uuid        NOT NULL REFERENCES organization (id),
    site_id             uuid        NOT NULL,
    name                text        NOT NULL,
    account_id          uuid        NOT NULL,
    collector_person_id uuid,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    version             int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    UNIQUE (org_id, account_id),
    FOREIGN KEY (org_id, site_id, account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, collector_person_id) REFERENCES person (org_id, id)
);
SELECT openbekci_enable_rls('cash_box');

CREATE TABLE fiscal_year (
    id                      uuid        NOT NULL,
    org_id                  uuid        NOT NULL REFERENCES organization (id),
    site_id                 uuid        NOT NULL,
    start_on                date        NOT NULL,
    end_on                  date        NOT NULL,
    status                  text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed', 'approved')),
    approved_by_decision_id uuid,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    version                 int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    UNIQUE (org_id, site_id, start_on),
    CHECK (end_on > start_on),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('fiscal_year');

-- Gap-free per-site per-year counters (receipts, decisions, ...), taken with a row lock.
CREATE TABLE sequence (
    org_id  uuid NOT NULL REFERENCES organization (id),
    site_id uuid NOT NULL,
    kind    text NOT NULL,
    year    int  NOT NULL,
    last    int  NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, site_id, kind, year),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('sequence');

CREATE TABLE journal_entry (
    id                uuid        NOT NULL,
    org_id            uuid        NOT NULL REFERENCES organization (id),
    site_id           uuid        NOT NULL,
    entry_date        date        NOT NULL,
    -- the date of the underlying document; differs from entry_date on reversals
    document_date     date        NOT NULL,
    fiscal_year_id    uuid,
    memo              text        NOT NULL DEFAULT '',
    source_type       text        NOT NULL,
    source_id         uuid,
    posted_by         uuid,
    posted_at         timestamptz NOT NULL DEFAULT clock_timestamp(),
    reverses_entry_id uuid,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, fiscal_year_id) REFERENCES fiscal_year (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, reverses_entry_id) REFERENCES journal_entry (org_id, site_id, id)
);
-- an entry is reversed at most once
CREATE UNIQUE INDEX journal_entry_reverses ON journal_entry (org_id, reverses_entry_id) WHERE reverses_entry_id IS NOT NULL;
CREATE INDEX journal_entry_site_date ON journal_entry (org_id, site_id, entry_date, id);
CREATE INDEX journal_entry_source ON journal_entry (org_id, source_type, source_id);
SELECT openbekci_enable_rls('journal_entry');

CREATE TABLE journal_line (
    id           uuid   NOT NULL,
    org_id       uuid   NOT NULL REFERENCES organization (id),
    site_id      uuid   NOT NULL,
    entry_id     uuid   NOT NULL,
    -- copy of the entry's date: the balance index is led by (account, date)
    entry_date   date   NOT NULL,
    account_id   uuid   NOT NULL,
    fund_id      uuid   NOT NULL,
    debit        bigint NOT NULL DEFAULT 0 CHECK (debit >= 0),
    credit       bigint NOT NULL DEFAULT 0 CHECK (credit >= 0),
    unit_id      uuid,
    debtor_kind  text   CHECK (debtor_kind IN ('owner', 'tenant')),
    person_id    uuid,
    block_id     uuid,
    vendor_id    uuid,
    PRIMARY KEY (org_id, id),
    -- exactly one of debit and credit is non-zero
    CHECK ((debit = 0) <> (credit = 0)),
    FOREIGN KEY (org_id, site_id, entry_id) REFERENCES journal_entry (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, account_id) REFERENCES account (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, fund_id) REFERENCES fund (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id)
);
CREATE INDEX journal_line_account_date ON journal_line (org_id, site_id, account_id, entry_date);
CREATE INDEX journal_line_unit ON journal_line (org_id, site_id, unit_id, debtor_kind) WHERE unit_id IS NOT NULL;
CREATE INDEX journal_line_person ON journal_line (org_id, person_id) WHERE person_id IS NOT NULL;
CREATE INDEX journal_line_entry ON journal_line (org_id, entry_id);
SELECT openbekci_enable_rls('journal_line');

REVOKE UPDATE, DELETE, TRUNCATE ON journal_entry, journal_line FROM openbekci_app, openbekci_platform;

-- The journal is append-only for every role (see audit_append_only).
CREATE TRIGGER journal_entry_append_only_row BEFORE UPDATE OR DELETE ON journal_entry
    FOR EACH ROW EXECUTE FUNCTION audit_append_only();
CREATE TRIGGER journal_entry_append_only_truncate BEFORE TRUNCATE ON journal_entry
    FOR EACH STATEMENT EXECUTE FUNCTION audit_append_only();
CREATE TRIGGER journal_line_append_only_row BEFORE UPDATE OR DELETE ON journal_line
    FOR EACH ROW EXECUTE FUNCTION audit_append_only();
CREATE TRIGGER journal_line_append_only_truncate BEFORE TRUNCATE ON journal_line
    FOR EACH STATEMENT EXECUTE FUNCTION audit_append_only();

-- Balanced entries, checked at commit: sum(debit) = sum(credit) per entry.
-- +goose StatementBegin
CREATE FUNCTION journal_entry_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    d bigint;
    c bigint;
BEGIN
    SELECT COALESCE(SUM(debit), 0), COALESCE(SUM(credit), 0) INTO d, c
      FROM journal_line WHERE org_id = NEW.org_id AND entry_id = NEW.entry_id;
    IF d <> c THEN
        RAISE EXCEPTION 'journal entry % is not balanced: debit % credit %', NEW.entry_id, d, c
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER journal_line_balanced AFTER INSERT ON journal_line
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION journal_entry_balanced();

-- Postings on or before the site's lock date are refused (rule 4).
-- +goose StatementBegin
CREATE FUNCTION journal_entry_unlocked() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    locked date;
BEGIN
    SELECT lock_date INTO locked FROM site WHERE org_id = NEW.org_id AND id = NEW.site_id;
    IF locked IS NOT NULL AND NEW.entry_date <= locked THEN
        RAISE EXCEPTION 'entry date % is on or before the lock date %', NEW.entry_date, locked
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER journal_entry_unlocked BEFORE INSERT ON journal_entry
    FOR EACH ROW EXECUTE FUNCTION journal_entry_unlocked();

-- +goose Down
DROP TRIGGER journal_entry_unlocked ON journal_entry;
DROP FUNCTION journal_entry_unlocked();
DROP TRIGGER journal_line_balanced ON journal_line;
DROP FUNCTION journal_entry_balanced();
DROP TABLE journal_line;
DROP TABLE journal_entry;
DROP TABLE sequence;
DROP TABLE fiscal_year;
DROP TABLE cash_box;
DROP TABLE account;
DROP TABLE fund;
