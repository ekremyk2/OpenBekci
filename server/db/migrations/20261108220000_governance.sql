-- +goose Up

-- Governance (backend plan 4.14, decisions G1 to G8): the owners' assembly end
-- to end. Every table carries org_id first (RLS) and composite foreign keys
-- (org_id, ...), and the site-scoped ones also site_id.

-- Per site: the rules (quorum, thresholds, vote weight, proxy caps, notice
-- timing, term reminders) as editable settings with Law 634 defaults. No row
-- means "the Turkish preset"; the first PUT creates it. version is what
-- If-Match compares; an absent row reads as version 1.
CREATE TABLE governance_settings (
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    preset     text,
    settings   jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 2,
    PRIMARY KEY (org_id, site_id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('governance_settings');

CREATE TABLE meeting (
    id                      uuid        NOT NULL,
    org_id                  uuid        NOT NULL REFERENCES organization (id),
    site_id                 uuid        NOT NULL,
    kind                    text        NOT NULL CHECK (kind IN ('general_assembly', 'extraordinary')),
    -- site now; block and council are reserved (G7)
    scope                   text        NOT NULL DEFAULT 'site' CHECK (scope IN ('site', 'block', 'council')),
    title                   text,
    scheduled_at            timestamptz NOT NULL,
    second_call_at          timestamptz NOT NULL,
    location                text        NOT NULL,
    status                  text        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'called', 'in_session', 'closed', 'cancelled')),
    -- who could vote and with what weight, computed when the meeting is called (G1)
    eligibility_snapshot    jsonb,
    -- the rules in force when it was called: a later settings change does not rewrite a past meeting
    settings_snapshot       jsonb,
    call_letter_document_id uuid,
    minutes_document_id     uuid,
    minutes_file_id         uuid,
    called_at               timestamptz,
    started_at              timestamptz,
    closed_at               timestamptz,
    cancel_reason           text,
    created_by              uuid,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    version                 int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    CHECK (second_call_at > scheduled_at)
);
CREATE INDEX meeting_org_site_time ON meeting (org_id, site_id, scheduled_at);
SELECT openbekci_enable_rls('meeting');

-- Generated documents (call letter, hazirun cetveli, minutes) as the module
-- stored them. The documents module (operations) files them through
-- governance.DocumentFiler; until then the ids point at these rows.
CREATE TABLE meeting_document (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    meeting_id uuid        NOT NULL,
    kind       text        NOT NULL CHECK (kind IN ('call_letter', 'attendance_sheet', 'minutes')),
    file_name  text        NOT NULL,
    mime       text        NOT NULL,
    content    bytea       NOT NULL,
    created_by uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id, meeting_id) REFERENCES meeting (org_id, site_id, id)
);
CREATE INDEX meeting_document_meeting ON meeting_document (org_id, meeting_id, kind);
SELECT openbekci_enable_rls('meeting_document');

CREATE TABLE agenda_item (
    id          uuid        NOT NULL,
    org_id      uuid        NOT NULL REFERENCES organization (id),
    site_id     uuid        NOT NULL,
    meeting_id  uuid        NOT NULL,
    position    int         NOT NULL CHECK (position > 0),
    title       text        NOT NULL,
    description text,
    will_be_voted boolean   NOT NULL DEFAULT false,
    -- key into the site's threshold table; null means the default topic
    topic       text,
    decision_id uuid,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    version     int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, meeting_id) REFERENCES meeting (org_id, site_id, id) ON DELETE CASCADE,
    UNIQUE (org_id, meeting_id, position) DEFERRABLE INITIALLY DEFERRED
);
SELECT openbekci_enable_rls('agenda_item');

-- The legal record of delivering the call (phase call) or the decisions
-- (phase decisions) to one owner (G3): what the court asks for.
CREATE TABLE meeting_notice (
    id            uuid        NOT NULL,
    org_id        uuid        NOT NULL REFERENCES organization (id),
    site_id       uuid        NOT NULL,
    meeting_id    uuid        NOT NULL,
    person_id     uuid        NOT NULL,
    phase         text        NOT NULL CHECK (phase IN ('call', 'decisions')),
    method        text        CHECK (method IN ('registered_mail', 'signed', 'email', 'sms', 'other')),
    status        text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'recorded')),
    sent_at       timestamptz,
    tracking_no   text,
    -- the scanned receipt or signature page; a plain id until the file store is in
    proof_file_id uuid,
    note          text,
    recorded_by   uuid,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, meeting_id, person_id, phase),
    FOREIGN KEY (org_id, site_id, meeting_id) REFERENCES meeting (org_id, site_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id),
    CHECK ((status = 'recorded') = (method IS NOT NULL AND sent_at IS NOT NULL))
);
SELECT openbekci_enable_rls('meeting_notice');

-- Non-binding pre-meeting poll (G2): one per agenda item, one answer per unit.
CREATE TABLE poll (
    org_id         uuid        NOT NULL REFERENCES organization (id),
    site_id        uuid        NOT NULL,
    agenda_item_id uuid        NOT NULL,
    question       text,
    opens_at       timestamptz NOT NULL,
    closes_at      timestamptz NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    version        int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, agenda_item_id),
    FOREIGN KEY (org_id, site_id, agenda_item_id) REFERENCES agenda_item (org_id, site_id, id) ON DELETE CASCADE,
    CHECK (closes_at > opens_at)
);
SELECT openbekci_enable_rls('poll');

CREATE TABLE poll_answer (
    org_id         uuid        NOT NULL REFERENCES organization (id),
    site_id        uuid        NOT NULL,
    agenda_item_id uuid        NOT NULL,
    unit_id        uuid        NOT NULL,
    answer         text        NOT NULL CHECK (answer IN ('yes', 'no', 'abstain')),
    answered_by    uuid        NOT NULL,
    answered_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agenda_item_id, unit_id),
    FOREIGN KEY (org_id, agenda_item_id) REFERENCES poll (org_id, agenda_item_id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id),
    FOREIGN KEY (org_id, answered_by) REFERENCES person (org_id, id)
);
SELECT openbekci_enable_rls('poll_answer');

-- Who attended (G4). One row per person and represented owner: an owner who
-- also holds proxies has several rows. votes and share are fixed at check-in
-- from the eligibility snapshot, so the quorum stays reproducible.
CREATE TABLE attendance (
    id                  uuid          NOT NULL,
    org_id              uuid          NOT NULL REFERENCES organization (id),
    site_id             uuid          NOT NULL,
    meeting_id          uuid          NOT NULL,
    person_id           uuid          NOT NULL,
    unit_ids            uuid[]        NOT NULL,
    mode                text          NOT NULL CHECK (mode IN ('in_person', 'proxy')),
    proxy_for_person_id uuid,
    source              text          NOT NULL DEFAULT 'desk' CHECK (source IN ('desk', 'qr')),
    votes               int           NOT NULL CHECK (votes >= 0),
    share               numeric(24,12) NOT NULL CHECK (share >= 0),
    checked_in_at       timestamptz   NOT NULL DEFAULT now(),
    checked_in_by       uuid,
    created_at          timestamptz   NOT NULL DEFAULT now(),
    updated_at          timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id, meeting_id) REFERENCES meeting (org_id, site_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id),
    FOREIGN KEY (org_id, proxy_for_person_id) REFERENCES person (org_id, id),
    CHECK ((mode = 'proxy') = (proxy_for_person_id IS NOT NULL))
);
CREATE UNIQUE INDEX attendance_one_per_represented ON attendance
    (org_id, meeting_id, person_id, COALESCE(proxy_for_person_id, '00000000-0000-0000-0000-000000000000'::uuid));
SELECT openbekci_enable_rls('attendance');

-- The QR check-in token of an owner for one meeting (in the call letter and
-- the app). Meeting-scoped and useless after the meeting closes.
CREATE TABLE attendance_token (
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    meeting_id uuid        NOT NULL,
    person_id  uuid        NOT NULL,
    token      text        NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, meeting_id, person_id),
    FOREIGN KEY (org_id, site_id, meeting_id) REFERENCES meeting (org_id, site_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id)
);
SELECT openbekci_enable_rls('attendance_token');

-- In-room counts of one agenda item (G2: the binding vote happens in the room).
CREATE TABLE vote_count (
    org_id         uuid           NOT NULL REFERENCES organization (id),
    site_id        uuid           NOT NULL,
    agenda_item_id uuid           NOT NULL,
    yes_count      int            NOT NULL CHECK (yes_count >= 0),
    no_count       int            NOT NULL CHECK (no_count >= 0),
    abstain_count  int            NOT NULL CHECK (abstain_count >= 0),
    yes_share      numeric(24,12) NOT NULL CHECK (yes_share >= 0),
    no_share       numeric(24,12) NOT NULL CHECK (no_share >= 0),
    abstain_share  numeric(24,12) NOT NULL CHECK (abstain_share >= 0),
    recorded_by    uuid,
    recorded_at    timestamptz    NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, agenda_item_id),
    FOREIGN KEY (org_id, site_id, agenda_item_id) REFERENCES agenda_item (org_id, site_id, id) ON DELETE CASCADE
);
SELECT openbekci_enable_rls('vote_count');

-- Gap-free decision numbers per site and year (for the karar defteri): the
-- row is locked by the INSERT ... ON CONFLICT DO UPDATE that takes the next
-- number, in the same transaction as the decision.
CREATE TABLE decision_counter (
    org_id  uuid NOT NULL REFERENCES organization (id),
    site_id uuid NOT NULL,
    year    int  NOT NULL,
    last_no int  NOT NULL,
    PRIMARY KEY (org_id, site_id, year),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('decision_counter');

CREATE TABLE decision (
    id                  uuid        NOT NULL,
    org_id              uuid        NOT NULL REFERENCES organization (id),
    site_id             uuid        NOT NULL,
    meeting_id          uuid        NOT NULL,
    agenda_item_id      uuid,
    sequence_no         int         NOT NULL CHECK (sequence_no > 0),
    year                int         NOT NULL,
    text                text        NOT NULL,
    outcome             text        NOT NULL CHECK (outcome IN ('accepted', 'rejected', 'no_vote', 'noted')),
    -- the counts and the threshold evaluation as of recording
    vote_count          jsonb,
    dissents            jsonb       NOT NULL DEFAULT '[]',
    concerns_residents  boolean     NOT NULL DEFAULT false,
    notified_at         timestamptz,
    -- {kind, record_type, record_id, applied_at, applied_by}: the change this decision created (G5)
    effect              jsonb,
    created_by          uuid,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    version             int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    UNIQUE (org_id, site_id, year, sequence_no),
    FOREIGN KEY (org_id, site_id, meeting_id) REFERENCES meeting (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, agenda_item_id) REFERENCES agenda_item (org_id, site_id, id)
);
CREATE UNIQUE INDEX decision_one_per_item ON decision (org_id, agenda_item_id) WHERE agenda_item_id IS NOT NULL;
CREATE INDEX decision_org_site_year ON decision (org_id, site_id, year, sequence_no);
SELECT openbekci_enable_rls('decision');

ALTER TABLE agenda_item
    ADD CONSTRAINT agenda_item_decision_fk FOREIGN KEY (org_id, site_id, decision_id) REFERENCES decision (org_id, site_id, id);

-- Reminders before an elected term ends (G6), one per membership and lead time.
CREATE TABLE term_reminder (
    id            uuid        NOT NULL,
    org_id        uuid        NOT NULL REFERENCES organization (id),
    site_id       uuid        NOT NULL,
    membership_id uuid        NOT NULL,
    person_id     uuid        NOT NULL,
    role          text        NOT NULL,
    valid_to      date        NOT NULL,
    lead_days     int         NOT NULL,
    notified_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, membership_id, lead_days),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, membership_id) REFERENCES membership (org_id, id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id)
);
CREATE INDEX term_reminder_org_site ON term_reminder (org_id, site_id, valid_to);
SELECT openbekci_enable_rls('term_reminder');

-- +goose Down
DROP TABLE term_reminder;
ALTER TABLE agenda_item DROP CONSTRAINT agenda_item_decision_fk;
DROP TABLE decision;
DROP TABLE decision_counter;
DROP TABLE vote_count;
DROP TABLE attendance_token;
DROP TABLE attendance;
DROP TABLE poll_answer;
DROP TABLE poll;
DROP TABLE meeting_notice;
DROP TABLE agenda_item;
DROP TABLE meeting_document;
DROP TABLE meeting;
DROP TABLE governance_settings;
