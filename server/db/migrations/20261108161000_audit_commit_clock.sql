-- +goose Up

-- Order events by the clock at INSERT instead of the transaction start. now()
-- is the same for every statement of a transaction, so a long transaction that
-- commits late was stamped with its start time and could land behind a cursor
-- a client had already passed. clock_timestamp() is the INSERT time, which is
-- much closer to commit order. It is still not commit order (two transactions
-- can commit in the opposite order of their inserts); an export that must not
-- miss events pages with from/to snapshots taken a little in the past. See
-- audit.Service.List.
ALTER TABLE audit_event ALTER COLUMN at SET DEFAULT clock_timestamp();
ALTER TABLE platform_audit_event ALTER COLUMN at SET DEFAULT clock_timestamp();

-- +goose Down
ALTER TABLE platform_audit_event ALTER COLUMN at SET DEFAULT now();
ALTER TABLE audit_event ALTER COLUMN at SET DEFAULT now();
