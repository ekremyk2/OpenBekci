-- +goose Up

-- Identity (backend plan, sections 4.2 and 6). Everything in this file is
-- instance-wide, not tenant data: no org_id, no RLS; the tables are on the
-- allowlist of the every-table-has-a-policy test.

-- citext: e-mail addresses are unique case-insensitively.
CREATE EXTENSION IF NOT EXISTS citext;

-- The sign-in account. One per human across the whole instance; organizations
-- link to it through person.user_id.
CREATE TABLE "user" (
    id                uuid        PRIMARY KEY,
    email             citext      UNIQUE,
    phone             text        UNIQUE,
    password_hash     text,
    locale            text        NOT NULL DEFAULT 'tr',
    status            text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    is_platform_admin boolean     NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    version           int         NOT NULL DEFAULT 1,
    CONSTRAINT user_has_contact CHECK (email IS NOT NULL OR phone IS NOT NULL),
    CONSTRAINT user_phone_e164 CHECK (phone IS NULL OR phone ~ '^\+[1-9][0-9]{6,14}$')
);

-- Opaque tokens of every kind (web cookie, access token, refresh token); only
-- the SHA-256 of the token is stored. family_id ties a refresh token and the
-- access tokens issued from it together (a web cookie is its own family), so
-- reuse of a rotated refresh token can revoke the whole family. The family id
-- is also the session id shown to the user: it survives rotation.
CREATE TABLE session (
    id           uuid        PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    kind         text        NOT NULL CHECK (kind IN ('web_cookie', 'access_token', 'refresh_token')),
    token_hash   bytea       NOT NULL UNIQUE,
    family_id    uuid        NOT NULL,
    rotated_from uuid        REFERENCES session (id) ON DELETE SET NULL,
    device_name  text,
    ip           text,
    user_agent   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz,
    last_used_at timestamptz,
    reauth_until timestamptz
);
CREATE INDEX session_user_family ON session (user_id, family_id);
CREATE INDEX session_family ON session (family_id);
CREATE INDEX session_expires_at ON session (expires_at);

-- One-time codes and links. SMS sign-in codes (6 digits, 5 minutes, 5 attempts)
-- and magic links (a long random token, 15 minutes, single use) share the
-- table (purpose 'reauth' is the email code of re-authentication): code_hash is HMAC(SECRET_KEY, purpose, target, code) for a short code
-- and the SHA-256 of the token for a link (purpose 'magic_link').
CREATE TABLE otp_code (
    id          uuid        PRIMARY KEY,
    user_id     uuid        REFERENCES "user" (id) ON DELETE CASCADE,
    target_kind text        NOT NULL CHECK (target_kind IN ('phone', 'email')),
    target      text        NOT NULL,
    purpose     text        NOT NULL CHECK (purpose IN ('sign_in', 'verify', 'magic_link', 'reauth')),
    code_hash   bytea       NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    attempts    int         NOT NULL DEFAULT 0,
    consumed_at timestamptz
);
CREATE INDEX otp_code_target ON otp_code (purpose, target, created_at DESC);
CREATE UNIQUE INDEX otp_code_link_hash ON otp_code (code_hash) WHERE purpose = 'magic_link';
CREATE INDEX otp_code_expires_at ON otp_code (expires_at);

-- Append-only, pruned (retention 90 days). Drives lockout: failures per
-- identifier since the last success.
CREATE TABLE login_attempt (
    id         uuid        PRIMARY KEY,
    identifier text        NOT NULL,
    method     text        NOT NULL,
    ip         text,
    success    boolean     NOT NULL,
    user_id    uuid,
    at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_attempt_identifier_at ON login_attempt (identifier, at DESC);
CREATE INDEX login_attempt_at ON login_attempt (at);

-- What was sent (or failed to send) through the SMS and mail gateways. The
-- code and link requests answer the same for known and unknown accounts, so a
-- gateway failure cannot be reported to the requester; it lands here, and in
-- the log, for the operator. target is masked. Pruned with the other logs.
CREATE TABLE delivery_log (
    id      uuid        PRIMARY KEY,
    at      timestamptz NOT NULL DEFAULT now(),
    channel text        NOT NULL CHECK (channel IN ('sms', 'email')),
    purpose text        NOT NULL,
    target  text        NOT NULL,
    user_id uuid,
    status  text        NOT NULL CHECK (status IN ('sent', 'failed', 'dropped')),
    detail  text
);
CREATE INDEX delivery_log_at ON delivery_log (at DESC);

-- +goose Down
DROP TABLE delivery_log;
DROP TABLE login_attempt;
DROP TABLE otp_code;
DROP TABLE session;
DROP TABLE "user";
