-- +goose Up

-- A checkout create is idempotent in the module itself, not only in the HTTP
-- layer: the HTTP layer releases an Idempotency-Key after a server error (a
-- provider that timed out), and a retry with a new checkout id would ask the
-- provider for a second hosted checkout for the same charges. The key and a
-- hash of the request are kept on the checkout; a retry of the same request
-- reuses the checkout id (which is the provider reference) and its provider
-- checkout, or asks the provider again with the same reference.
ALTER TABLE checkout
    ADD COLUMN idempotency_key text,
    ADD COLUMN request_hash    text;
CREATE UNIQUE INDEX checkout_idempotency ON checkout (org_id, created_by, idempotency_key) WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX checkout_idempotency;
ALTER TABLE checkout DROP COLUMN request_hash, DROP COLUMN idempotency_key;
