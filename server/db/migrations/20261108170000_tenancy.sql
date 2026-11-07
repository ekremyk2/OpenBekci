-- +goose Up

-- Tenancy (backend plan 4.1): sites, blocks, units, allocation keys, unit tags,
-- vehicles, facilities (reserved for 0.3), import batches and site hand-over
-- records.
--
-- Key convention of the tables below: PRIMARY KEY (org_id, id), not (id). A
-- site can be handed over to another organization of the same instance with
-- its identifiers preserved (decision D2, site export/import), and the source
-- rows may still exist while the copy is made, so ids are unique per
-- organization. Every tenant-to-tenant foreign key is composite and, for
-- site-scoped tables, also carries site_id, so the database refuses links
-- across organizations and across sites.

CREATE TABLE site (
    id                      uuid        NOT NULL,
    org_id                  uuid        NOT NULL REFERENCES organization (id),
    name                    text        NOT NULL,
    address                 jsonb       NOT NULL DEFAULT '{}',
    timezone                text        NOT NULL DEFAULT 'Europe/Istanbul',
    currency                text        NOT NULL DEFAULT 'TRY' CHECK (currency ~ '^[A-Z]{3}$'),
    fiscal_year_start_month int         NOT NULL DEFAULT 1 CHECK (fiscal_year_start_month BETWEEN 1 AND 12),
    -- postings dated on or before this day are refused (phase C)
    lock_date               date,
    -- typed accessors in the tenancy module: transparency_to_owners, tenants_are_portal_users, directory_enabled, appurtenance_default
    settings                jsonb       NOT NULL DEFAULT '{}',
    -- IBAN and holder, shown to payers
    bank_details            jsonb       NOT NULL DEFAULT '{}',
    status                  text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'offboarded')),
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    version                 int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id)
);
SELECT openbekci_enable_rls('site');

CREATE TABLE block (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    name       text        NOT NULL,
    -- the block of a single-building site is created automatically and hidden (S1)
    hidden     boolean     NOT NULL DEFAULT false,
    sort_order int         NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    UNIQUE (org_id, site_id, name),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('block');

CREATE TABLE unit (
    id                    uuid          NOT NULL,
    org_id                uuid          NOT NULL REFERENCES organization (id),
    site_id               uuid          NOT NULL,
    block_id              uuid          NOT NULL,
    -- what people say ("A-12"); the optional legal reference is the tapu bagimsiz bolum no
    door_number           text          NOT NULL,
    legal_ref             text,
    type                  text          NOT NULL DEFAULT 'flat' CHECK (type IN ('flat', 'shop', 'office', 'parking', 'storage', 'common', 'other')),
    area_m2               numeric(10,2) CHECK (area_m2 IS NULL OR area_m2 > 0),
    -- appurtenance (S2): a parking spot or storage room listed under a flat
    parent_unit_id        uuid,
    -- per-unit override of the site default (settings.appurtenance_default); null follows the site
    appurtenance_override text          CHECK (appurtenance_override IN ('appurtenance', 'independent')),
    -- per-unit payer override: charge type -> owner | tenant; the effective payer is computed by tenancy.EffectivePayer
    payer_overrides       jsonb         NOT NULL DEFAULT '{}',
    -- merged and split are reserved for the unit merge/split use cases
    status                text          NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'merged', 'split')),
    created_at            timestamptz   NOT NULL DEFAULT now(),
    updated_at            timestamptz   NOT NULL DEFAULT now(),
    version               int           NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    UNIQUE (org_id, site_id, door_number),
    FOREIGN KEY (org_id, site_id, block_id) REFERENCES block (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, parent_unit_id) REFERENCES unit (org_id, site_id, id),
    CHECK (parent_unit_id IS NULL OR parent_unit_id <> id)
);
CREATE INDEX unit_org_block ON unit (org_id, block_id);
SELECT openbekci_enable_rls('unit');

-- Per site: code (arsa_payi, equal, area, custom), kind share/equal/area/table (money M2).
CREATE TABLE allocation_key (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    code       text        NOT NULL CHECK (code ~ '^[a-z][a-z0-9_]{0,39}$'),
    name       text        NOT NULL,
    kind       text        NOT NULL CHECK (kind IN ('share', 'equal', 'area', 'table')),
    -- the Law 634 defaults created with the site
    is_default boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    UNIQUE (org_id, site_id, code),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('allocation_key');

-- Stored weights of share and table keys; equal and area keys are computed.
CREATE TABLE allocation_key_value (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    key_id     uuid        NOT NULL,
    unit_id    uuid        NOT NULL,
    value      numeric(24,12) NOT NULL CHECK (value >= 0),
    -- the value as entered ("12/1000", "0.012", "350"), shown back to the user
    raw        text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, key_id, unit_id),
    FOREIGN KEY (org_id, site_id, key_id) REFERENCES allocation_key (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id)
);
SELECT openbekci_enable_rls('allocation_key_value');

-- Cost-group membership (lift, pool) for scoped allocations.
CREATE TABLE unit_tag (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    unit_id    uuid        NOT NULL,
    tag        text        NOT NULL CHECK (tag ~ '^[a-z0-9][a-z0-9_-]{0,39}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, unit_id, tag),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id)
);
SELECT openbekci_enable_rls('unit_tag');

-- Vehicles of a unit; plates are stored normalized (34ABC123), unique per site,
-- and exported as CSV for the camera operator.
CREATE TABLE vehicle (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    unit_id    uuid        NOT NULL,
    plate      text        NOT NULL CHECK (plate ~ '^[0-9A-Z]{2,12}$'),
    label      text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, plate),
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id)
);
CREATE INDEX vehicle_org_unit ON vehicle (org_id, unit_id);
SELECT openbekci_enable_rls('vehicle');

-- Bookable things (0.3, community); reserved so the export manifest is stable.
CREATE TABLE facility (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    name       text        NOT NULL,
    rules      jsonb       NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('facility');

-- A spreadsheet import. dry_run batches only report; applied batches keep the
-- parsed rows (including the opening-balance columns phase C posts).
CREATE TABLE import_batch (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    kind       text        NOT NULL DEFAULT 'units_people',
    filename   text        NOT NULL,
    mapping    jsonb       NOT NULL DEFAULT '{}',
    dry_run    boolean     NOT NULL,
    status     text        NOT NULL CHECK (status IN ('dry_run', 'applied', 'failed')),
    summary    jsonb       NOT NULL DEFAULT '{}',
    report     jsonb       NOT NULL DEFAULT '[]',
    created_by uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('import_batch');

CREATE TABLE import_row (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    batch_id   uuid        NOT NULL,
    row_no     int         NOT NULL,
    status     text        NOT NULL CHECK (status IN ('ok', 'warning', 'error')),
    -- the mapped row as parsed
    data       jsonb       NOT NULL DEFAULT '{}',
    -- opening balance columns: {"amount_minor": int, "side": "owner"|"tenant", "as_of": "YYYY-MM-DD"}; only validated now, posted in phase C
    opening    jsonb,
    unit_id    uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, batch_id, row_no),
    FOREIGN KEY (org_id, site_id, batch_id) REFERENCES import_batch (org_id, site_id, id)
);
SELECT openbekci_enable_rls('import_row');

-- Record of a site export or import (D2). The archive of an export is kept in
-- the row until the file store arrives (phase D).
CREATE TABLE site_export (
    id            uuid        NOT NULL,
    org_id        uuid        NOT NULL REFERENCES organization (id),
    direction     text        NOT NULL CHECK (direction IN ('export', 'import')),
    -- no foreign key: the exported site may be offboarded and the import's site is in the archive
    site_id       uuid,
    site_name     text,
    source_org_id uuid,
    target_org_id uuid,
    status        text        NOT NULL DEFAULT 'done' CHECK (status IN ('running', 'done', 'failed')),
    summary       jsonb       NOT NULL DEFAULT '{}',
    archive       bytea,
    created_by    uuid,
    at            timestamptz NOT NULL DEFAULT now(),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    version       int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id)
);
SELECT openbekci_enable_rls('site_export');

-- +goose Down
DROP TABLE site_export;
DROP TABLE import_row;
DROP TABLE import_batch;
DROP TABLE facility;
DROP TABLE vehicle;
DROP TABLE unit_tag;
DROP TABLE allocation_key_value;
DROP TABLE allocation_key;
DROP TABLE unit;
DROP TABLE block;
DROP TABLE site;
