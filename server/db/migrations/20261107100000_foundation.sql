-- +goose Up

-- Enables and forces row-level security on a tenant table and installs the
-- tenant policy. Every tenant table must call this in the migration that
-- creates it so the policy text is identical everywhere (backend plan, section 5).
-- +goose StatementBegin
CREATE FUNCTION openbekci_enable_rls(tbl regclass) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    policy_name text := (SELECT relname FROM pg_class WHERE oid = tbl) || '_tenant';
BEGIN
    EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY', tbl);
    EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY', tbl);
    EXECUTE format(
        'CREATE POLICY %I ON %s '
        'USING      (org_id = (SELECT NULLIF(current_setting(''app.org_id'', true), '''')::uuid)) '
        'WITH CHECK (org_id = (SELECT NULLIF(current_setting(''app.org_id'', true), '''')::uuid))',
        policy_name, tbl);
END;
$$;
-- +goose StatementEnd

-- Instance-wide, single row. Not tenant data, so no RLS.
CREATE TABLE instance_setting (
    id          boolean     PRIMARY KEY DEFAULT true CHECK (id),
    instance_id uuid        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
INSERT INTO instance_setting (instance_id) VALUES (gen_random_uuid());

-- The organization is the RLS tenant; for this root table org_id = id.
CREATE TABLE organization (
    id         uuid        PRIMARY KEY,
    org_id     uuid        NOT NULL,
    slug       text        NOT NULL UNIQUE,
    name       text        NOT NULL,
    status     text        NOT NULL DEFAULT 'active',
    settings   jsonb       NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1,
    CONSTRAINT organization_org_id_is_id CHECK (org_id = id),
    CONSTRAINT organization_org_id_id_key UNIQUE (org_id, id)
);
SELECT openbekci_enable_rls('organization');

-- +goose Down
DROP TABLE organization;
DROP TABLE instance_setting;
DROP FUNCTION openbekci_enable_rls(regclass);
