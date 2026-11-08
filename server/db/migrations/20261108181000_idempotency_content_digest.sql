-- +goose Up

-- The SHA-256 of the content of a streamed request body (a file upload), set
-- when the request completes. A streamed body is not buffered, so it is not part
-- of request_hash; a retry with the same key is compared on this digest instead:
-- the same digest replays the stored response, another one is
-- idempotency_mismatch. NULL for requests whose body is part of request_hash.
ALTER TABLE idempotency_key ADD COLUMN content_digest text;

-- +goose Down
ALTER TABLE idempotency_key DROP COLUMN content_digest;
