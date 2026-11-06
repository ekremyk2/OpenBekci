-- +goose Up

-- Audit trail (backend plan 4.13 and 16). Append-only: the application roles
-- may INSERT and SELECT, never UPDATE, DELETE or TRUNCATE. The default
-- privileges give new tables all four, so they are revoked explicitly here.
-- (The one rewrite path, privacy_erase, is a SECURITY DEFINER function owned
-- by the migrator, added with the privacy work; it never touches the hashes.)
CREATE TABLE audit_event (
    id            uuid        PRIMARY KEY,
    org_id        uuid        NOT NULL REFERENCES organization (id),
    site_id       uuid,
    actor_user_id uuid,
    actor_kind    text        NOT NULL CHECK (actor_kind IN ('user', 'system', 'job')),
    action        text        NOT NULL,
    target_type   text        NOT NULL,
    target_id     uuid        NOT NULL,
    -- PII-safe: fields registered as PII appear as {hash_before, hash_after}
    diff          jsonb       NOT NULL DEFAULT '{}',
    ip            inet,
    request_id    text,
    at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_event_org_at ON audit_event (org_id, at DESC, id DESC);
CREATE INDEX audit_event_org_action ON audit_event (org_id, action, at DESC);
CREATE INDEX audit_event_org_target ON audit_event (org_id, target_type, target_id, at DESC);
SELECT openbekci_enable_rls('audit_event');
REVOKE UPDATE, DELETE, TRUNCATE ON audit_event FROM openbekci_app, openbekci_platform;

-- Events that belong to no organization: password changes, session
-- revocation, platform-administrator promotion. Not tenant data (no org_id, no
-- RLS). The app role may only append; the platform role may read.
CREATE TABLE platform_audit_event (
    id            uuid        PRIMARY KEY,
    actor_user_id uuid,
    actor_kind    text        NOT NULL CHECK (actor_kind IN ('user', 'system', 'job')),
    action        text        NOT NULL,
    target_type   text        NOT NULL,
    target_id     uuid        NOT NULL,
    diff          jsonb       NOT NULL DEFAULT '{}',
    ip            inet,
    request_id    text,
    at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX platform_audit_event_at ON platform_audit_event (at DESC, id DESC);
CREATE INDEX platform_audit_event_target ON platform_audit_event (target_type, target_id, at DESC);
REVOKE UPDATE, DELETE, TRUNCATE ON platform_audit_event FROM openbekci_app, openbekci_platform;
REVOKE SELECT ON platform_audit_event FROM openbekci_app;

-- +goose Down
DROP TABLE platform_audit_event;
DROP TABLE audit_event;
