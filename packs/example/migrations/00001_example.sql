-- +goose Up

-- The tables of a pack are named pack_<id>_*. openbekci_pack_table (a core
-- function) adds the foreign key to organization, the composite foreign key
-- to site when there is a site_id column, and forced row-level security.

CREATE TABLE pack_example_txn (
    org_id     uuid        NOT NULL,
    id         uuid        NOT NULL DEFAULT gen_random_uuid(),
    txn        text        NOT NULL,
    state      text        NOT NULL CHECK (state IN ('pending', 'paid', 'failed', 'refunded')),
    gross      bigint      NOT NULL DEFAULT 0,
    fee        bigint      NOT NULL DEFAULT 0,
    currency   text        NOT NULL DEFAULT 'TRY',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, txn)
);
SELECT openbekci_pack_table('pack_example_txn');

-- Events and lifecycle marks the pack saw; ref makes handlers idempotent.
CREATE TABLE pack_example_event (
    org_id     uuid        NOT NULL,
    id         uuid        NOT NULL DEFAULT gen_random_uuid(),
    site_id    uuid,
    kind       text        NOT NULL,
    ref        text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, kind, ref)
);
SELECT openbekci_pack_table('pack_example_event');

-- +goose Down
DROP TABLE pack_example_event;
DROP TABLE pack_example_txn;
