-- +goose Up

-- Organization API keys (backend plan sections 6, 7 and 16, release 0.4).
-- A key authenticates as a principal whose rights are the intersection of its
-- scopes (permission names) and the grants its creator holds at call time. The
-- secret is 256 random bits, shown once; only its SHA-256 is stored (the same
-- approach as session tokens), looked up by the public prefix.
CREATE TABLE api_key (
    id           uuid        NOT NULL,
    org_id       uuid        NOT NULL REFERENCES organization (id),
    name         text        NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    -- the public part of "obk_<prefix>_<secret>"; unique across the instance
    prefix       text        NOT NULL CHECK (prefix ~ '^[a-z0-9]{8}$'),
    secret_hash  bytea       NOT NULL,
    -- permission names from the authz registry
    scopes       text[]      NOT NULL CHECK (cardinality(scopes) BETWEEN 1 AND 200),
    -- optional restriction to one site
    site_id      uuid,
    -- the administrator whose grants bound the key
    created_by   uuid        NOT NULL REFERENCES "user" (id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    rotated_at   timestamptz,
    last_used_at timestamptz,
    expires_at   timestamptz,
    revoked_at   timestamptz,
    revoked_by   uuid,
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
CREATE UNIQUE INDEX api_key_prefix ON api_key (prefix);
SELECT openbekci_enable_rls('api_key');

-- Audit events caused through a key name the key as the actor (actor_kind
-- 'api_key', actor_key_id); actor_user_id stays empty so the trail never
-- attributes a key's action to the administrator who created it.
ALTER TABLE audit_event DROP CONSTRAINT audit_event_actor_kind_check;
ALTER TABLE audit_event ADD CONSTRAINT audit_event_actor_kind_check CHECK (actor_kind IN ('user', 'system', 'job', 'api_key'));
ALTER TABLE audit_event ADD COLUMN actor_key_id uuid;
ALTER TABLE platform_audit_event DROP CONSTRAINT platform_audit_event_actor_kind_check;
ALTER TABLE platform_audit_event ADD CONSTRAINT platform_audit_event_actor_kind_check CHECK (actor_kind IN ('user', 'system', 'job', 'api_key'));
ALTER TABLE platform_audit_event ADD COLUMN actor_key_id uuid;

-- +goose Down
ALTER TABLE platform_audit_event DROP COLUMN actor_key_id;
ALTER TABLE audit_event DROP COLUMN actor_key_id;
DROP TABLE api_key;
