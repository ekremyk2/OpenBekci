-- +goose Up

-- A bank line can settle an expense (an outgoing line linked to an expense that was posted without the
-- bank). The link is a real foreign key now that expenses exist, and an expense matches at most one line.
ALTER TABLE bank_transaction
    ADD CONSTRAINT bank_transaction_expense_fk FOREIGN KEY (org_id, site_id, expense_id) REFERENCES expense (org_id, site_id, id);
CREATE UNIQUE INDEX bank_transaction_expense ON bank_transaction (org_id, expense_id) WHERE expense_id IS NOT NULL;

-- +goose Down
DROP INDEX bank_transaction_expense;
ALTER TABLE bank_transaction DROP CONSTRAINT bank_transaction_expense_fk;
