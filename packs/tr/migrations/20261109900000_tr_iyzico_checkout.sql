-- +goose Up

-- Whose iyzico account a checkout was created on. The core asks Status and
-- Refund about a transaction without saying which site or merchant of record it
-- belongs to, so the pack keeps the account of each checkout token.
CREATE TABLE pack_tr_iyzico_checkout (
    org_id     uuid        NOT NULL,
    token      text        NOT NULL,
    site_id    uuid        NOT NULL,
    -- 'site' or 'organization' (the merchant of record when the checkout was created)
    merchant   text        NOT NULL CHECK (merchant IN ('site', 'organization')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, token)
);
SELECT openbekci_pack_table('pack_tr_iyzico_checkout');

-- +goose Down
DROP TABLE pack_tr_iyzico_checkout;
