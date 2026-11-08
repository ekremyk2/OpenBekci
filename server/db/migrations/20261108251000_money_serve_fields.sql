-- +goose Up

-- Fields the served contract shows that the first money migration did not keep.
ALTER TABLE late_fee_freeze ADD COLUMN version int NOT NULL DEFAULT 1;
ALTER TABLE late_fee_freeze ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
-- the collection case that asked for the freeze (the case table belongs to collections)
ALTER TABLE late_fee_freeze ADD COLUMN case_id uuid;
ALTER TABLE opening_balance ADD COLUMN void_reason text;

-- +goose Down
ALTER TABLE opening_balance DROP COLUMN void_reason;
ALTER TABLE late_fee_freeze DROP COLUMN case_id;
ALTER TABLE late_fee_freeze DROP COLUMN updated_at;
ALTER TABLE late_fee_freeze DROP COLUMN version;
