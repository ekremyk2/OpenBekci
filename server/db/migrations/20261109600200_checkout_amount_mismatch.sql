-- +goose Up

-- What the provider confirmed can differ from what the checkout asked for (a
-- partial capture, a currency conversion at the provider, a provider-side
-- adjustment). The payment records the provider's confirmed gross, never the
-- checkout's amount; the checkout keeps the confirmed amount and a flag so that
-- a manager can see the difference.
ALTER TABLE checkout
    ADD COLUMN amount_mismatch  boolean NOT NULL DEFAULT false,
    ADD COLUMN confirmed_amount bigint;

-- +goose Down
ALTER TABLE checkout DROP COLUMN confirmed_amount, DROP COLUMN amount_mismatch;
