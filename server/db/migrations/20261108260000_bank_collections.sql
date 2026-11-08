-- +goose Up

-- Bank statement import with manual matching (backend plan 4.6, M5) and
-- collections: dunning stages, events, runs and cases (4.8).

-- The uploaded file is kept as bytes until the files module exists (TODO: move to files, keep file_id).
CREATE TABLE bank_statement_import (
    id               uuid        NOT NULL,
    org_id           uuid        NOT NULL REFERENCES organization (id),
    site_id          uuid        NOT NULL,
    importer         text        NOT NULL,
    asset_account_id uuid        NOT NULL,
    filename         text        NOT NULL DEFAULT '',
    content          bytea       NOT NULL,
    content_sha256   bytea       NOT NULL,
    mapping          jsonb       NOT NULL DEFAULT '{}',
    status           text        NOT NULL DEFAULT 'parsing' CHECK (status IN ('parsing', 'ready', 'partial', 'failed')),
    opening_balance  bigint,
    closing_balance  bigint,
    period_from      date,
    period_to        date,
    error            text,
    created_by       uuid,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    CHECK (octet_length(content) <= 10485760),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, asset_account_id) REFERENCES account (org_id, site_id, id)
);
-- the same file cannot be imported twice into one account
CREATE UNIQUE INDEX bank_statement_import_file ON bank_statement_import (org_id, site_id, asset_account_id, content_sha256);
SELECT openbekci_enable_rls('bank_statement_import');

CREATE TABLE bank_transaction (
    id            uuid        NOT NULL,
    org_id        uuid        NOT NULL REFERENCES organization (id),
    site_id       uuid        NOT NULL,
    import_id     uuid        NOT NULL,
    line_no       int         NOT NULL,
    booked_on     date        NOT NULL,
    amount        bigint      NOT NULL CHECK (amount <> 0),
    counterparty  text,
    description   text,
    reference     text,
    -- the parsed source row; stripped after 90 days by the prune job
    raw           jsonb,
    match_status  text        NOT NULL DEFAULT 'unmatched' CHECK (match_status IN ('unmatched', 'suggested', 'matched', 'ignored')),
    suggestions   jsonb       NOT NULL DEFAULT '[]',
    payment_id    uuid,
    expense_id    uuid,
    note          text,
    matched_by    uuid,
    matched_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, import_id, line_no),
    FOREIGN KEY (org_id, site_id, import_id) REFERENCES bank_statement_import (org_id, site_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, site_id, payment_id) REFERENCES payment (org_id, site_id, id)
);
CREATE INDEX bank_transaction_site_status ON bank_transaction (org_id, site_id, match_status, booked_on);
CREATE INDEX bank_transaction_raw ON bank_transaction (created_at) WHERE raw IS NOT NULL;
-- a payment settles at most one bank line
CREATE UNIQUE INDEX bank_transaction_payment ON bank_transaction (org_id, payment_id) WHERE payment_id IS NOT NULL;
SELECT openbekci_enable_rls('bank_transaction');

CREATE TABLE dunning_stage (
    id             uuid        NOT NULL,
    org_id         uuid        NOT NULL REFERENCES organization (id),
    site_id        uuid        NOT NULL,
    name           text        NOT NULL,
    kind           text        NOT NULL DEFAULT 'reminder' CHECK (kind IN ('reminder', 'ihtarname')),
    days_after_due int          NOT NULL CHECK (days_after_due >= 0),
    channels       text[]      NOT NULL DEFAULT '{in_app}',
    template_key   text,
    position       int         NOT NULL DEFAULT 0,
    enabled        boolean     NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    version        int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('dunning_stage');

CREATE TABLE dunning_run (
    id          uuid        NOT NULL,
    org_id      uuid        NOT NULL REFERENCES organization (id),
    site_id     uuid        NOT NULL,
    stage_id    uuid        NOT NULL,
    event_count int         NOT NULL DEFAULT 0,
    sms_count   int         NOT NULL DEFAULT 0,
    created_by  uuid,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, stage_id) REFERENCES dunning_stage (org_id, site_id, id)
);
SELECT openbekci_enable_rls('dunning_run');

-- One reminder or notice to a debtor. source job rows are unique per debtor, stage, period and channel.
CREATE TABLE dunning_event (
    id               uuid        NOT NULL,
    org_id           uuid        NOT NULL REFERENCES organization (id),
    site_id          uuid        NOT NULL,
    run_id           uuid,
    stage_id         uuid        NOT NULL,
    source           text        NOT NULL DEFAULT 'run' CHECK (source IN ('run', 'job')),
    unit_id          uuid,
    debtor_kind      text        CHECK (debtor_kind IN ('owner', 'tenant')),
    person_id        uuid,
    -- 'u:<unit>:<kind>' or 'p:<person>': the debtor key used for deduplication
    debtor_key       text        NOT NULL,
    period           text        NOT NULL DEFAULT '',
    channel          text        NOT NULL CHECK (channel IN ('in_app', 'email', 'sms', 'letter')),
    amount_due       bigint      NOT NULL DEFAULT 0,
    sent_at          timestamptz NOT NULL DEFAULT now(),
    -- the generated letter until the files module exists (TODO: file into documents, keep document_file_id)
    letter           bytea,
    document_file_id uuid,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    CHECK (unit_id IS NOT NULL OR person_id IS NOT NULL),
    FOREIGN KEY (org_id, site_id, stage_id) REFERENCES dunning_stage (org_id, site_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, site_id, run_id) REFERENCES dunning_run (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id)
);
CREATE UNIQUE INDEX dunning_event_job_dedupe ON dunning_event (org_id, stage_id, debtor_key, period, channel) WHERE source = 'job';
CREATE INDEX dunning_event_site ON dunning_event (org_id, site_id, sent_at DESC);
SELECT openbekci_enable_rls('dunning_event');

CREATE TABLE collection_case (
    id               uuid        NOT NULL,
    org_id           uuid        NOT NULL REFERENCES organization (id),
    site_id          uuid        NOT NULL,
    unit_id          uuid,
    debtor_kind      text        CHECK (debtor_kind IN ('owner', 'tenant')),
    debtor_person_id uuid,
    opened_on        date        NOT NULL,
    lawyer           text,
    file_no          text,
    status           text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    closed_on        date,
    costs_charged    bigint      NOT NULL DEFAULT 0 CHECK (costs_charged >= 0),
    cost_charge_id   uuid,
    freeze_id        uuid,
    notes            text,
    created_by       uuid,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    version          int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    CHECK ((unit_id IS NOT NULL AND debtor_kind IS NOT NULL) OR (unit_id IS NULL AND debtor_kind IS NULL AND debtor_person_id IS NOT NULL)),
    CHECK (status = 'open' OR closed_on IS NOT NULL),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, debtor_person_id) REFERENCES person (org_id, id)
);
-- one open case per debtor
CREATE UNIQUE INDEX collection_case_open_unit ON collection_case (org_id, unit_id, debtor_kind) WHERE status = 'open' AND unit_id IS NOT NULL;
CREATE UNIQUE INDEX collection_case_open_person ON collection_case (org_id, debtor_person_id) WHERE status = 'open' AND unit_id IS NULL;
SELECT openbekci_enable_rls('collection_case');

-- +goose Down
DROP TABLE collection_case;
DROP TABLE dunning_event;
DROP TABLE dunning_run;
DROP TABLE dunning_stage;
DROP TABLE bank_transaction;
DROP TABLE bank_statement_import;
