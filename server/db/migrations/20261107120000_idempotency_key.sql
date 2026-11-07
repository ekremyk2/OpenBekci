-- +goose Up

-- Idempotency-Key store (backend plan, section 8). Keyed by organization and
-- principal explicitly rather than by row-level security: it is read and
-- written before authorization by the HTTP layer, and org_id / principal_id
-- are NULL for anonymous or platform-level requests. Not tenant data, so no
-- RLS policy; it is allow-listed in the every-table-has-a-policy test.
CREATE TABLE idempotency_key (
    org_id                uuid,
    principal_id          uuid,
    key                   text        NOT NULL,
    request_hash          text        NOT NULL,
    status                text        NOT NULL CHECK (status IN ('in_progress', 'done')),
    response_status       int,
    response_content_type text,
    response_body         bytea,
    created_at            timestamptz NOT NULL DEFAULT now(),
    expires_at            timestamptz NOT NULL DEFAULT now() + interval '7 days',
    CONSTRAINT idempotency_key_done_has_response
        CHECK (status = 'in_progress' OR response_status IS NOT NULL)
);
-- One row per (org, principal, key); NULLs compare equal so anonymous keys are unique too.
CREATE UNIQUE INDEX idempotency_key_scope_key ON idempotency_key (org_id, principal_id, key) NULLS NOT DISTINCT;
CREATE INDEX idempotency_key_expires_at ON idempotency_key (expires_at);

-- +goose Down
DROP TABLE idempotency_key;
