-- +goose Up

-- Packs (packs plan sections 2 and 3, backend plan 10): per-organization
-- enablement, per-site pack settings, the "pack default" markers of site
-- settings, and the helper that gives pack tables the tenant conventions.
--
-- Pack ids are lower-case letters and digits ("tr", "example"); a pack's tables
-- are named pack_<id>_*. The ids "enablement", "site", "goose", "core" and
-- "default" are reserved because tables of this migration start with pack_enablement.

CREATE TABLE pack_enablement (
    org_id      uuid        NOT NULL REFERENCES organization (id),
    pack_id     text        NOT NULL CHECK (pack_id ~ '^[a-z][a-z0-9]{0,23}$'),
    enabled     boolean     NOT NULL DEFAULT true,
    -- organization-scope settings without the secret fields
    settings    jsonb       NOT NULL DEFAULT '{}',
    -- secret fields of the organization-scope settings: key -> sealed value (field encryption, never returned)
    secrets     jsonb       NOT NULL DEFAULT '{}',
    version     int         NOT NULL DEFAULT 1,
    enabled_at  timestamptz,
    disabled_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, pack_id)
);
SELECT openbekci_enable_rls('pack_enablement');

-- Site-scope pack settings (a provider account per site).
CREATE TABLE pack_enablement_site (
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    pack_id    text        NOT NULL,
    settings   jsonb       NOT NULL DEFAULT '{}',
    secrets    jsonb       NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, site_id, pack_id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, pack_id) REFERENCES pack_enablement (org_id, pack_id)
);
SELECT openbekci_enable_rls('pack_enablement_site');

-- Which site setting keys a pack filled and with what value. A key is a "pack
-- default" while the site's setting still equals the recorded value; once the
-- site edits it the key is the site's own and later enablements leave it alone.
CREATE TABLE site_pack_default (
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    key        text        NOT NULL,
    pack_id    text        NOT NULL,
    value      jsonb       NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, site_id, key),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('site_pack_default');

-- openbekci_pack_table gives a pack table the tenant conventions: the table
-- name must be pack_<id>_<name>, it must have an org_id column and a primary
-- key that includes it; the foreign key to organization is added, a site_id
-- column gets the composite (org_id, site_id) foreign key to site, and the
-- row-level security policy of openbekci_enable_rls is enabled and forced.
-- Pack migrations call it right after CREATE TABLE:
--   SELECT openbekci_pack_table('pack_acme_thing');
-- +goose StatementBegin
CREATE FUNCTION openbekci_pack_table(tbl regclass) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    rel text := (SELECT relname FROM pg_class WHERE oid = tbl);
    org_attnum smallint;
    has_site boolean;
BEGIN
    IF rel !~ '^pack_[a-z][a-z0-9]*_[a-z0-9_]+$' THEN
        RAISE EXCEPTION 'pack table % must be named pack_<id>_<name>', rel;
    END IF;
    SELECT attnum INTO org_attnum FROM pg_attribute WHERE attrelid = tbl AND attname = 'org_id' AND NOT attisdropped;
    IF org_attnum IS NULL THEN
        RAISE EXCEPTION 'pack table % needs an org_id column', rel;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = tbl AND contype = 'p' AND org_attnum = ANY (conkey)) THEN
        RAISE EXCEPTION 'pack table % needs a primary key that includes org_id', rel;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = tbl AND conname = rel || '_org_fk') THEN
        EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I FOREIGN KEY (org_id) REFERENCES organization (id)', tbl, rel || '_org_fk');
    END IF;
    has_site := EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = tbl AND attname = 'site_id' AND NOT attisdropped);
    IF has_site AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = tbl AND conname = rel || '_site_fk') THEN
        EXECUTE format('ALTER TABLE %s ADD CONSTRAINT %I FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)', tbl, rel || '_site_fk');
    END IF;
    IF NOT (SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid = tbl) THEN
        IF EXISTS (SELECT 1 FROM pg_policy WHERE polrelid = tbl) THEN
            RAISE EXCEPTION 'pack table % has policies but row-level security is not enforced', rel;
        END IF;
        PERFORM openbekci_enable_rls(tbl);
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION openbekci_pack_table(regclass);
DROP TABLE site_pack_default;
DROP TABLE pack_enablement_site;
DROP TABLE pack_enablement;
