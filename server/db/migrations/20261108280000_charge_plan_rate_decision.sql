-- +goose Up

-- A decision creates at most one rate (a dues-rate change, or the single rate of a
-- special assessment plan): applying it twice concurrently must not create a second.
CREATE UNIQUE INDEX charge_plan_rate_decision ON charge_plan_rate (org_id, decision_id) WHERE decision_id IS NOT NULL;

-- +goose Down
DROP INDEX charge_plan_rate_decision;
