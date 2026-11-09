-- +goose Up

-- The documents only a pack provides (the call delivery checklist, the proxy form, the karar defteri draft) are
-- filed with the meeting's documents like the call letter, the attendance sheet and the minutes.
ALTER TABLE meeting_document DROP CONSTRAINT meeting_document_kind_check;
ALTER TABLE meeting_document ADD CONSTRAINT meeting_document_kind_check
    CHECK (kind IN ('call_letter', 'attendance_sheet', 'minutes', 'delivery_checklist', 'proxy_form', 'decision_book_draft'));

-- +goose Down
DELETE FROM meeting_document WHERE kind IN ('delivery_checklist', 'proxy_form', 'decision_book_draft');
ALTER TABLE meeting_document DROP CONSTRAINT meeting_document_kind_check;
ALTER TABLE meeting_document ADD CONSTRAINT meeting_document_kind_check CHECK (kind IN ('call_letter', 'attendance_sheet', 'minutes'));
