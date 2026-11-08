-- +goose Up

-- Late fees: person debtors (carried debt) accrue too; one fee booking per debtor and closed month;
-- the policy schedule is append-only; every site has the statutory default from its creation date.

ALTER TABLE late_fee_booking DROP CONSTRAINT late_fee_booking_debtor_kind_check;
ALTER TABLE late_fee_booking ADD CONSTRAINT late_fee_booking_debtor_kind_check CHECK (debtor_kind IN ('owner', 'tenant', 'person'));
ALTER TABLE late_fee_booking ADD COLUMN person_id uuid;
ALTER TABLE late_fee_booking ADD CONSTRAINT late_fee_booking_person_fk FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id);
ALTER TABLE late_fee_booking ADD CONSTRAINT late_fee_booking_person_ck CHECK ((debtor_kind = 'person') = (person_id IS NOT NULL));

CREATE UNIQUE INDEX late_fee_booking_side_period ON late_fee_booking (org_id, site_id, unit_id, debtor_kind, period) WHERE kind = 'fee' AND debtor_kind <> 'person';
CREATE UNIQUE INDEX late_fee_booking_person_period ON late_fee_booking (org_id, person_id, period) WHERE kind = 'fee' AND debtor_kind = 'person';
CREATE INDEX late_fee_booking_person ON late_fee_booking (org_id, person_id) WHERE person_id IS NOT NULL;

ALTER TABLE late_fee_policy ADD COLUMN is_default boolean NOT NULL DEFAULT false;

-- +goose StatementBegin
CREATE FUNCTION late_fee_policy_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND OLD.is_default THEN
        RETURN OLD; -- the automatic default is replaced by the site's first own policy
    END IF;
    RAISE EXCEPTION 'late_fee_policy is append-only: add a row with a later from_on' USING ERRCODE = 'integrity_constraint_violation';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER late_fee_policy_no_update BEFORE UPDATE OR DELETE ON late_fee_policy FOR EACH ROW EXECUTE FUNCTION late_fee_policy_append_only();

-- the statutory default for every existing site without a policy
INSERT INTO late_fee_policy (id, org_id, site_id, from_on, rate_bps_per_month, grace_days, is_default)
SELECT gen_random_uuid(), s.org_id, s.id, (s.created_at AT TIME ZONE s.timezone)::date, 500, 0, true
FROM site s WHERE NOT EXISTS (SELECT 1 FROM late_fee_policy p WHERE p.org_id = s.org_id AND p.site_id = s.id);

-- +goose Down
DROP TRIGGER late_fee_policy_no_update ON late_fee_policy;
DROP FUNCTION late_fee_policy_append_only();
ALTER TABLE late_fee_policy DROP COLUMN is_default;
DROP INDEX late_fee_booking_person;
DROP INDEX late_fee_booking_person_period;
DROP INDEX late_fee_booking_side_period;
ALTER TABLE late_fee_booking DROP CONSTRAINT late_fee_booking_person_ck;
ALTER TABLE late_fee_booking DROP CONSTRAINT late_fee_booking_person_fk;
ALTER TABLE late_fee_booking DROP COLUMN person_id;
ALTER TABLE late_fee_booking DROP CONSTRAINT late_fee_booking_debtor_kind_check;
ALTER TABLE late_fee_booking ADD CONSTRAINT late_fee_booking_debtor_kind_check CHECK (debtor_kind IN ('owner', 'tenant'));
