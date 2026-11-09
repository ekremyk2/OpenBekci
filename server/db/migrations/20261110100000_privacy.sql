-- +goose Up

-- Privacy (backend plan 4.13 and 16): a person's decision on a consent text of
-- the organization (the privacy notice, a pack's per-purpose texts), and the
-- erase marker on the person. One row per (organization, user, purpose): the
-- current decision; every change is also an audit event, which is the history.
CREATE TABLE consent (
    org_id     uuid        NOT NULL REFERENCES organization (id),
    user_id    uuid        NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    -- the id of the consent text (core.privacy_notice, tr.consent_identity, ...)
    purpose    text        NOT NULL CHECK (char_length(purpose) BETWEEN 1 AND 120),
    version    text        NOT NULL CHECK (char_length(version) BETWEEN 1 AND 60),
    decision   text        NOT NULL CHECK (decision IN ('accepted', 'declined')),
    decided_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id, purpose)
);
SELECT openbekci_enable_rls('consent');

-- A data export request: who asked for the export of whose data. The export files
-- (scope privacy_export) are readable by that person and by the requester only.
CREATE TABLE data_export (
    org_id       uuid        NOT NULL REFERENCES organization (id),
    id           uuid        NOT NULL,
    person_id    uuid        NOT NULL,
    requested_by uuid        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id)
);
SELECT openbekci_enable_rls('data_export');

-- Set by the privacy erase; the person row keeps its id (ledger, ballots and
-- attendance point at it) and loses its personal data.
ALTER TABLE person ADD COLUMN erased_at timestamptz;

-- +goose Down
ALTER TABLE person DROP COLUMN erased_at;
DROP TABLE data_export;
DROP TABLE consent;
