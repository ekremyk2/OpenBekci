-- +goose Up

-- Payment providers (packs plan 3.8): a payment made through a provider is a
-- payment row with method card:<provider>, the provider's transaction id (the
-- idempotency key of its webhooks) and gross, fee and net. The net sits in a
-- per-provider clearing account until the settlement arrives (provider_settlement).

ALTER TABLE payment DROP CONSTRAINT payment_method_check;
ALTER TABLE payment ADD CONSTRAINT payment_method_check
    CHECK (method IN ('cash', 'bank_transfer') OR method ~ '^card:[a-z][a-z0-9]*(\.[a-z0-9_]+)?$');
ALTER TABLE payment
    ADD COLUMN provider        text,
    ADD COLUMN provider_txn_id text,
    ADD COLUMN refund_ref      text,
    ADD CONSTRAINT payment_provider_pair CHECK ((provider IS NULL) = (provider_txn_id IS NULL));
-- a provider transaction is recorded at most once per organization
CREATE UNIQUE INDEX payment_provider_txn ON payment (org_id, provider, provider_txn_id) WHERE provider IS NOT NULL;

-- A provider payout: bank against clearing. ref is the provider's settlement id.
CREATE TABLE provider_settlement (
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    provider   text        NOT NULL,
    ref        text        NOT NULL,
    net        bigint      NOT NULL CHECK (net > 0),
    settled_on date        NOT NULL,
    entry_id   uuid        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, provider, ref),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, entry_id) REFERENCES journal_entry (org_id, site_id, id)
);
SELECT openbekci_enable_rls('provider_settlement');

-- +goose Down
DROP TABLE provider_settlement;
DROP INDEX payment_provider_txn;
ALTER TABLE payment DROP CONSTRAINT payment_provider_pair, DROP COLUMN refund_ref, DROP COLUMN provider_txn_id, DROP COLUMN provider;
ALTER TABLE payment DROP CONSTRAINT payment_method_check;
ALTER TABLE payment ADD CONSTRAINT payment_method_check CHECK (method IN ('cash', 'bank_transfer'));
