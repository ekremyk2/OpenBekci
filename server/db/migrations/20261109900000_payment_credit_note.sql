-- +goose Up

-- The credit note (alacak makbuzu) issued when a payment is voided or refunded: its own gap-free number
-- (sequence kind credit_note) and the filed document. Both stay empty until the credit note is made.
ALTER TABLE payment
    ADD COLUMN credit_note_no          text,
    ADD COLUMN credit_note_document_id uuid;
CREATE UNIQUE INDEX payment_credit_note_no ON payment (org_id, site_id, credit_note_no) WHERE credit_note_no IS NOT NULL;

-- +goose Down
DROP INDEX payment_credit_note_no;
ALTER TABLE payment DROP COLUMN credit_note_document_id, DROP COLUMN credit_note_no;
