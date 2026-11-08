-- +goose Up

-- Access control (design 07-access-control, release 0.3): agents, access
-- points, credentials projected from vehicles and visitor entries, the
-- allow-list version counter and change log, the opening log and the commands
-- sent to agents.
--
-- Conventions as in the tenancy migration: PRIMARY KEY (org_id, id), RLS from
-- openbekci_enable_rls, composite foreign keys that carry org_id and, for
-- site-scoped parents, site_id.

-- An on-site agent. Enrolment and token hashes live here, under RLS: the
-- agent token carries its agent id, and access_agent_org (below) maps that id
-- to the organization so the request can open a tenant transaction.
CREATE TABLE agent (
    id                  uuid        NOT NULL,
    org_id              uuid        NOT NULL REFERENCES organization (id),
    site_id             uuid        NOT NULL,
    name                text        NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    status              text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'revoked')),
    -- one-time enrolment token (SHA-256), 15 minutes, single use
    enrol_token_hash    bytea,
    enrol_expires_at    timestamptz,
    enrol_used_at       timestamptz,
    -- long-lived agent token: SHA-256 of a 256-bit secret; the retired token
    -- stays valid for 10 minutes after a rotation, and the new token is kept
    -- sealed for that long so repeating the rotation returns the same answer
    token_hash          bytea,
    previous_token_hash bytea,
    previous_valid_until timestamptz,
    rotation_sealed     bytea,
    rotate_by           timestamptz,
    rotate_now          boolean     NOT NULL DEFAULT false,
    -- what the agent reported
    hostname            text,
    platform            text,
    agent_version       text,
    protocol_version    int,
    last_seen           timestamptz,
    snapshot_version    bigint,
    config_version      bigint,
    queue_depth         bigint,
    drivers_status      jsonb       NOT NULL DEFAULT '[]',
    clock_offset_ms     bigint,
    clock_suspect       boolean     NOT NULL DEFAULT false,
    lost_events         bigint,
    store_error         text,
    dead_letters        bigint,
    enrolled_at         timestamptz,
    revoked_at          timestamptz,
    revoked_by          uuid,
    created_by          uuid,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    version             int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    UNIQUE (org_id, site_id, name),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
CREATE UNIQUE INDEX agent_enrol_token ON agent (enrol_token_hash) WHERE enrol_token_hash IS NOT NULL;
SELECT openbekci_enable_rls('agent');

-- Maps an agent id to its organization, and an enrolment token hash to its
-- agent. SECURITY DEFINER because the caller does not know the organization
-- yet (the migrator role bypasses RLS). They return nothing else.
-- +goose StatementBegin
CREATE FUNCTION access_agent_org(p_agent uuid) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
    SELECT org_id FROM agent WHERE id = p_agent ORDER BY created_at LIMIT 1
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION access_enrol_agent(p_hash bytea) RETURNS TABLE (org_id uuid, agent_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
    SELECT a.org_id, a.id FROM agent a WHERE a.enrol_token_hash = p_hash LIMIT 1
$$;
-- +goose StatementEnd

CREATE TABLE access_point (
    id         uuid        NOT NULL,
    org_id     uuid        NOT NULL REFERENCES organization (id),
    site_id    uuid        NOT NULL,
    name       text        NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    kind       text        NOT NULL,
    direction  text        NOT NULL,
    -- the agent that drives it; an agent may serve many points
    agent_id   uuid,
    -- {instance, channel}: names a driver instance in the agent's local file
    driver_ref jsonb,
    enabled    boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version    int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    UNIQUE (org_id, site_id, name),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    FOREIGN KEY (org_id, site_id, agent_id) REFERENCES agent (org_id, site_id, id)
);
CREATE INDEX access_point_agent ON access_point (org_id, agent_id);
SELECT openbekci_enable_rls('access_point');

-- Per site: the allow-list version (gap-free counter), the config version and
-- the bookkeeping of the projector. Writers lock the row (SELECT ... FOR
-- UPDATE), so versions are ordered and gap-free.
CREATE TABLE access_site_state (
    org_id          uuid        NOT NULL REFERENCES organization (id),
    site_id         uuid        NOT NULL,
    version         bigint      NOT NULL DEFAULT 0,
    config_version  bigint      NOT NULL DEFAULT 0,
    -- digest of the configuration (timezone, points, settings) behind config_version
    config_hash     text,
    -- visitor credentials changed after this instant are fetched from the CredentialSource
    visitor_since   timestamptz,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, site_id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('access_site_state');

-- Derived credentials (plates of vehicles, visitor plates and QR hashes) and
-- reserved manual kinds. Nobody edits a derived credential by hand.
CREATE TABLE credential (
    id          uuid        NOT NULL,
    org_id      uuid        NOT NULL REFERENCES organization (id),
    site_id     uuid        NOT NULL,
    kind        text        NOT NULL,
    -- plate: normalized plate; visitor_qr: hex(sha256(token)); tag and pin: the value the agent compares
    value       text        NOT NULL CHECK (length(value) BETWEEN 1 AND 128),
    source_type text        NOT NULL,
    source_id   uuid,
    unit_id     uuid,
    person_id   uuid,
    label       text,
    point_scope text        NOT NULL DEFAULT 'all_vehicle',
    point_ids   uuid[]      NOT NULL DEFAULT '{}',
    valid_from  timestamptz NOT NULL,
    valid_to    timestamptz,
    -- {days:[1..7], start:"HH:MM", end:"HH:MM"} in the site timezone
    recurring   jsonb,
    max_uses    int         CHECK (max_uses IS NULL OR max_uses >= 1),
    uses        int         NOT NULL DEFAULT 0,
    status      text        NOT NULL DEFAULT 'active',
    status_reason text,
    -- the allow-list entry last published for this credential (null: not in the list)
    published   jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, site_id, id),
    UNIQUE (org_id, site_id, kind, source_type, source_id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    -- display only: a credential outlives the unit or person it names (the link is cleared, not blocked)
    FOREIGN KEY (org_id, site_id, unit_id) REFERENCES unit (org_id, site_id, id) ON DELETE SET NULL (unit_id),
    FOREIGN KEY (org_id, person_id) REFERENCES person (org_id, id) ON DELETE SET NULL (person_id)
);
CREATE INDEX credential_site_value ON credential (org_id, site_id, kind, value);
CREATE INDEX credential_source ON credential (org_id, source_type, source_id);
SELECT openbekci_enable_rls('credential');

-- The change log behind deltas: one row per version.
CREATE TABLE allowlist_change (
    org_id        uuid        NOT NULL REFERENCES organization (id),
    site_id       uuid        NOT NULL,
    version       bigint      NOT NULL,
    op            text        NOT NULL CHECK (op IN ('upsert', 'remove')),
    credential_id uuid        NOT NULL,
    entry         jsonb,
    at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, site_id, version),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id),
    CHECK ((op = 'upsert') = (entry IS NOT NULL))
);
CREATE INDEX allowlist_change_at ON allowlist_change (org_id, at);
SELECT openbekci_enable_rls('allowlist_change');

-- Commands for agents: the only server to agent state that is persisted. The
-- id is the envelope id of the command message.
CREATE TABLE agent_command (
    id               uuid        NOT NULL,
    org_id           uuid        NOT NULL REFERENCES organization (id),
    site_id          uuid        NOT NULL,
    agent_id         uuid        NOT NULL,
    point_id         uuid,
    command          text        NOT NULL,
    payload          jsonb       NOT NULL DEFAULT '{}',
    actor_user_id    uuid,
    actor_display    text,
    status           text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'done', 'failed', 'expired')),
    -- {status, reason, detail} as acknowledged by the agent
    result           jsonb,
    issued_at        timestamptz NOT NULL,
    expires_at       timestamptz NOT NULL,
    delivered_at     timestamptz,
    completed_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, id),
    FOREIGN KEY (org_id, site_id, agent_id) REFERENCES agent (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, point_id) REFERENCES access_point (org_id, site_id, id),
    -- an open_point lives at most 60 seconds; the agent refuses a longer one
    CHECK (expires_at > issued_at AND (command <> 'open_point' OR expires_at <= issued_at + interval '60 seconds'))
);
CREATE INDEX agent_command_pending ON agent_command (org_id, agent_id, status, issued_at);
SELECT openbekci_enable_rls('agent_command');

-- The opening log. Append-only: the application roles cannot UPDATE, DELETE or
-- TRUNCATE; retention pruning and erasure of observed plates go through the
-- SECURITY DEFINER functions below (owned by the migrator).
CREATE TABLE access_event (
    id               uuid        NOT NULL,
    org_id           uuid        NOT NULL REFERENCES organization (id),
    site_id          uuid        NOT NULL,
    point_id         uuid        NOT NULL,
    -- null for events the server wrote itself
    agent_id         uuid,
    credential_id    uuid,
    decision         text        NOT NULL,
    reason           text        NOT NULL,
    presented_kind   text,
    observed_plate   text,
    actor_user_id    uuid,
    actor_display    text,
    command_id       uuid,
    visitor_entry_id uuid,
    occurred_at      timestamptz NOT NULL,
    synced_at        timestamptz NOT NULL DEFAULT now(),
    sync_seq         bigint      GENERATED ALWAYS AS IDENTITY,
    raw_ref          text,
    raw_payload      jsonb,
    PRIMARY KEY (org_id, id),
    UNIQUE (sync_seq),
    FOREIGN KEY (org_id, site_id, point_id) REFERENCES access_point (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, agent_id) REFERENCES agent (org_id, site_id, id),
    FOREIGN KEY (org_id, site_id, credential_id) REFERENCES credential (org_id, site_id, id)
);
CREATE INDEX access_event_feed ON access_event (org_id, site_id, sync_seq);
CREATE INDEX access_event_time ON access_event (org_id, site_id, occurred_at DESC);
SELECT openbekci_enable_rls('access_event');
REVOKE UPDATE, DELETE, TRUNCATE ON access_event FROM openbekci_app, openbekci_platform;

-- +goose StatementBegin
CREATE FUNCTION access_prune_events(p_org uuid, p_before timestamptz) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
DECLARE n bigint;
BEGIN
    DELETE FROM access_event WHERE org_id = p_org AND occurred_at < p_before;
    GET DIAGNOSTICS n = ROW_COUNT;
    RETURN n;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION access_scrub_plate(p_org uuid, p_credential uuid) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
DECLARE n bigint;
BEGIN
    UPDATE access_event SET observed_plate = NULL, raw_payload = NULL
     WHERE org_id = p_org AND credential_id = p_credential AND observed_plate IS NOT NULL;
    GET DIAGNOSTICS n = ROW_COUNT;
    RETURN n;
END;
$$;
-- +goose StatementEnd

-- Outbox of the projector: a change to a vehicle (or an occupancy) marks the
-- source dirty in the same transaction; the access.project job drains it.
-- The community module marks visitor entries through access.MarkDirty.
CREATE TABLE access_dirty (
    org_id      uuid        NOT NULL REFERENCES organization (id),
    site_id     uuid        NOT NULL,
    source_type text        NOT NULL,
    source_id   uuid        NOT NULL,
    queued_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, source_type, source_id)
);
CREATE INDEX access_dirty_queue ON access_dirty (queued_at);
SELECT openbekci_enable_rls('access_dirty');

-- The background jobs run without an organization: these two functions tell them
-- which sites have work (sites with queued sources, sites with access points).
-- SECURITY DEFINER for the same reason as the lookups above; they return ids only.
-- +goose StatementBegin
CREATE FUNCTION access_dirty_sites() RETURNS TABLE (org_id uuid, site_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
    SELECT DISTINCT d.org_id, d.site_id FROM access_dirty d ORDER BY 1, 2 LIMIT 200
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION access_active_sites() RETURNS TABLE (org_id uuid, site_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
    SELECT DISTINCT p.org_id, p.site_id FROM access_point p ORDER BY 1, 2
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION access_mark_vehicle_dirty() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE r vehicle;
BEGIN
    IF TG_OP = 'DELETE' THEN r := OLD; ELSE r := NEW; END IF;
    INSERT INTO access_dirty (org_id, site_id, source_type, source_id)
    VALUES (r.org_id, r.site_id, 'vehicle', r.id)
    ON CONFLICT (org_id, source_type, source_id) DO NOTHING;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER access_vehicle_dirty AFTER INSERT OR UPDATE OR DELETE ON vehicle
    FOR EACH ROW EXECUTE FUNCTION access_mark_vehicle_dirty();

-- An occupancy that starts or ends changes who may drive in: every vehicle of the unit is dirty.
-- +goose StatementBegin
CREATE FUNCTION access_mark_occupancy_dirty() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE r occupancy;
BEGIN
    IF TG_OP = 'DELETE' THEN r := OLD; ELSE r := NEW; END IF;
    INSERT INTO access_dirty (org_id, site_id, source_type, source_id)
    SELECT v.org_id, v.site_id, 'vehicle', v.id FROM vehicle v
     WHERE v.org_id = r.org_id AND v.unit_id = r.unit_id
    ON CONFLICT (org_id, source_type, source_id) DO NOTHING;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER access_occupancy_dirty AFTER INSERT OR UPDATE OR DELETE ON occupancy
    FOR EACH ROW EXECUTE FUNCTION access_mark_occupancy_dirty();

-- +goose Down
DROP TRIGGER access_occupancy_dirty ON occupancy;
DROP FUNCTION IF EXISTS access_mark_occupancy_dirty();
DROP TRIGGER access_vehicle_dirty ON vehicle;
DROP FUNCTION IF EXISTS access_mark_vehicle_dirty();
DROP FUNCTION IF EXISTS access_active_sites();
DROP FUNCTION IF EXISTS access_dirty_sites();
DROP TABLE access_dirty;
DROP FUNCTION IF EXISTS access_scrub_plate(uuid, uuid);
DROP FUNCTION IF EXISTS access_prune_events(uuid, timestamptz);
DROP TABLE access_event;
DROP TABLE agent_command;
DROP TABLE allowlist_change;
DROP TABLE credential;
DROP TABLE access_site_state;
DROP TABLE access_point;
DROP FUNCTION IF EXISTS access_enrol_agent(bytea);
DROP FUNCTION IF EXISTS access_agent_org(uuid);
DROP TABLE agent;
