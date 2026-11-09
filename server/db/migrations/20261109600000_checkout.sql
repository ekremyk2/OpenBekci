-- +goose Up

-- Provider checkout (release 0.4): a resident or a manager starts a hosted card
-- checkout for a debtor side; the provider returns checkout_url, the webhook (or
-- the status poll) completes it. Statuses: pending -> paid | failed | expired,
-- paid -> refunded. A paid event for a checkout already expired or failed still
-- records the payment (the money moved), so paid wins over expired and failed.

CREATE TABLE checkout (
    id                 uuid        NOT NULL,
    org_id             uuid        NOT NULL REFERENCES organization (id),
    site_id            uuid        NOT NULL,
    -- the debtor: one side of a unit
    unit_id            uuid        NOT NULL,
    debtor_kind        text        NOT NULL CHECK (debtor_kind IN ('owner', 'tenant')),
    -- charges the checkout pays; empty means oldest first
    charge_ids         uuid[]      NOT NULL DEFAULT '{}',
    provider           text        NOT NULL,
    -- whose provider account the checkout was created on (decision M5)
    merchant_of_record text        NOT NULL DEFAULT 'site' CHECK (merchant_of_record IN ('site', 'organization')),
    amount             bigint      NOT NULL CHECK (amount > 0),
    currency           text        NOT NULL,
    status             text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'failed', 'expired', 'refunded')),
    provider_txn_id    text,
    checkout_url       text,
    return_url         text        NOT NULL,
    failure_code       text,
    payment_id         uuid,
    created_by         uuid,
    created_at         timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL,
    completed_at       timestamptz,
    last_polled_at     timestamptz,
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, payment_id) REFERENCES payment (org_id, id)
);
-- the provider's transaction id resolves the checkout of a webhook
CREATE UNIQUE INDEX checkout_provider_txn ON checkout (org_id, provider, provider_txn_id) WHERE provider_txn_id IS NOT NULL;
CREATE INDEX checkout_site ON checkout (org_id, site_id, id DESC);
CREATE INDEX checkout_creator ON checkout (org_id, created_by, id DESC);
CREATE INDEX checkout_pending_expiry ON checkout (expires_at) WHERE status = 'pending';
SELECT openbekci_enable_rls('checkout');

-- A payment has a version so that a refund can be made with If-Match: the
-- version moves whenever the payment changes status.
ALTER TABLE payment ADD COLUMN version int NOT NULL DEFAULT 1;

-- A provider payout can now be posted to several sites: when the organization
-- is the merchant of record one payout covers the payments of many sites, and
-- each site's share is its own row (and its own journal entry).
ALTER TABLE provider_settlement
    ADD COLUMN merchant_of_record text   NOT NULL DEFAULT 'site' CHECK (merchant_of_record IN ('site', 'organization')),
    ADD COLUMN gross              bigint,
    ADD COLUMN fee                bigint;
ALTER TABLE provider_settlement DROP CONSTRAINT provider_settlement_pkey;
ALTER TABLE provider_settlement ADD PRIMARY KEY (org_id, provider, ref, site_id);

-- +goose Down
ALTER TABLE provider_settlement DROP CONSTRAINT provider_settlement_pkey;
ALTER TABLE provider_settlement ADD PRIMARY KEY (org_id, provider, ref);
ALTER TABLE provider_settlement DROP COLUMN fee, DROP COLUMN gross, DROP COLUMN merchant_of_record;
ALTER TABLE payment DROP COLUMN version;
DROP TABLE checkout;
