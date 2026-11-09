-- +goose Up

-- The statutory default late fee (5 % a month, no grace) applies to every due date, not only to dues
-- that fall after the site was created: a site set up late, with dues already overdue (imported
-- balances, a back-dated plan), would otherwise accrue nothing on them (M4: the fee accrued so far
-- is always shown). Only the automatic default rows move; a site's own entries keep their dates.
ALTER TABLE late_fee_policy DISABLE TRIGGER late_fee_policy_no_update;
UPDATE late_fee_policy SET from_on = DATE '1970-01-01' WHERE is_default AND from_on > DATE '1970-01-01';
ALTER TABLE late_fee_policy ENABLE TRIGGER late_fee_policy_no_update;

-- +goose Down
-- The original from_on (the site's creation date) is derivable, but moving it back would discard the
-- fees accrued meanwhile on earlier dues; the down step is a no-op by design.
SELECT 1;
