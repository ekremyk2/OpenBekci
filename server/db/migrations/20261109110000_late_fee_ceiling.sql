-- +goose Up

-- The 5 % a month ceiling is a legal preset, not a rule of the schema: a country pack fills the site setting
-- late_fee_policy.monthly_rate_ceiling_bps and the billing service enforces it. A site without a pack has no
-- ceiling; the schema keeps only a sanity bound (100 % a month).
ALTER TABLE late_fee_policy DROP CONSTRAINT late_fee_policy_rate_bps_per_month_check;
ALTER TABLE late_fee_policy ADD CONSTRAINT late_fee_policy_rate_bps_per_month_check CHECK (rate_bps_per_month BETWEEN 0 AND 10000);

-- +goose Down
ALTER TABLE late_fee_policy DROP CONSTRAINT late_fee_policy_rate_bps_per_month_check;
ALTER TABLE late_fee_policy ADD CONSTRAINT late_fee_policy_rate_bps_per_month_check CHECK (rate_bps_per_month BETWEEN 0 AND 500);
