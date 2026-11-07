-- +goose Up

-- Idempotency keys belong to an authenticated principal, and a row belongs to
-- the request that created it.
--
-- Isolation decision. idempotency_key stays outside row-level security
-- (openbekci_enable_rls): its policy is org_id = app.org_id, which hides and
-- rejects the rows whose org_id is NULL (instance-wide operations), and the
-- store runs in the HTTP layer on a non-tenant connection (InAppTx) before any
-- tenant context exists. Isolation is therefore strict by principal instead:
-- principal_id is NOT NULL, part of the unique key and of every statement, and
-- anonymous callers get no stored keys at all (the HTTP layer ignores the
-- header, or answers 401 when the operation requires a key). Rows of
-- organization-scoped operations also carry the org from the request path in
-- the key. Revisit with the first org-scoped mutations: move those rows to a
-- table that calls openbekci_enable_rls and is written through InTenantTx.
--
-- Pre-release table: rows written before this migration are dropped.
DELETE FROM idempotency_key;
ALTER TABLE idempotency_key ALTER COLUMN principal_id SET NOT NULL;
-- Token of the request that owns the row; Complete and Abort match on it, so a
-- request that outlives a stale takeover cannot overwrite the new owner.
ALTER TABLE idempotency_key ADD COLUMN owner uuid NOT NULL;
-- Allow-listed response headers other than Content-Type (Location, ...).
ALTER TABLE idempotency_key ADD COLUMN response_headers jsonb NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE idempotency_key DROP COLUMN response_headers;
ALTER TABLE idempotency_key DROP COLUMN owner;
ALTER TABLE idempotency_key ALTER COLUMN principal_id DROP NOT NULL;
