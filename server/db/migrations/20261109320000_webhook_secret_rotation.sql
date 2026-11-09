-- +goose Up

-- Webhook secret rotation with an overlap (ADR-0002): rotating seals a new
-- signing secret and keeps the replaced one, also sealed, until
-- secret_previous_expires_at; deliveries sign with both while it is in the
-- future, so a receiver can switch without a gap.
ALTER TABLE webhook_endpoint
    ADD COLUMN secret_previous_sealed     bytea,
    ADD COLUMN secret_previous_expires_at timestamptz,
    ADD COLUMN secret_rotated_at          timestamptz,
    ADD CONSTRAINT webhook_endpoint_previous_secret CHECK ((secret_previous_sealed IS NULL) = (secret_previous_expires_at IS NULL));

-- +goose Down
ALTER TABLE webhook_endpoint
    DROP CONSTRAINT webhook_endpoint_previous_secret,
    DROP COLUMN secret_rotated_at,
    DROP COLUMN secret_previous_expires_at,
    DROP COLUMN secret_previous_sealed;
