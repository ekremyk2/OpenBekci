-- +goose Up

-- The fake provider pays out in batches: a paid transaction joins the next
-- payout of its account and keeps that payout's ref and date, so asking for the
-- settlements of overlapping periods (the core's sync job does, every few
-- hours) yields the same payouts with the same refs and never one twice.
-- site_id is the site whose provider account the transaction was made on.
ALTER TABLE pack_example_txn
    ADD COLUMN site_id         uuid,
    ADD COLUMN settlement_ref  text,
    ADD COLUMN settled_on      date;

-- +goose Down
ALTER TABLE pack_example_txn DROP COLUMN settled_on, DROP COLUMN settlement_ref, DROP COLUMN site_id;
