-- +goose Up

-- Push devices and the web-login link (backend plan 4.12, sections 8 and 12;
-- mobile plan 7.2 and 9). Both tables belong to the sign-in account, not to an
-- organization: no org_id, no RLS; they are on the allowlist of the
-- every-table-has-a-policy test like the other identity tables.

-- One row per (user, token). The token is sealed with the core field cipher
-- (purpose "device.push_token", AAD = the row id) and never returned by the
-- API; token_hash (SHA-256) only serves the uniqueness of (user, token).
-- session_family_id is the session that registered the device: signing that
-- session out deletes the row. A device the relay reported as invalid is kept
-- but disabled (disabled_at), so a re-registration of the same token enables
-- it again.
CREATE TABLE device (
    id                uuid        PRIMARY KEY,
    user_id           uuid        NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    session_family_id uuid,
    push_kind         text        NOT NULL CHECK (push_kind IN ('fcm', 'apns', 'unifiedpush')),
    token_enc         bytea       NOT NULL,
    token_hash        bytea       NOT NULL,
    relay_id          text        CHECK (relay_id IS NULL OR relay_id ~ '^[A-Za-z0-9_.-]{1,64}$'),
    app_version       text        CHECK (app_version IS NULL OR char_length(app_version) <= 64),
    locale            text        CHECK (locale IS NULL OR char_length(locale) <= 16),
    disabled_at       timestamptz,
    disabled_reason   text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, token_hash)
);
CREATE INDEX device_session_family ON device (session_family_id) WHERE session_family_id IS NOT NULL;

-- One-time tokens of the web-login link: a signed-in app asks for one and
-- opens {BASE_URL}/login/link/{token} in the browser, which exchanges it for a
-- cookie session. Only the SHA-256 of the token is stored. The link is bound
-- to the user and to the session that issued it (session_family_id): it stops
-- working when that session is signed out.
CREATE TABLE web_login_link (
    id                uuid        PRIMARY KEY,
    user_id           uuid        NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    session_family_id uuid        NOT NULL,
    token_hash        bytea       NOT NULL UNIQUE,
    created_at        timestamptz NOT NULL DEFAULT now(),
    expires_at        timestamptz NOT NULL,
    consumed_at       timestamptz
);
CREATE INDEX web_login_link_expires_at ON web_login_link (expires_at);

-- The ephemeral push relay enrollment challenge (infra plan 4.2): `admin push
-- enrol` stores it for ten minutes, the discovery document serves it as
-- relay_enrollment.challenge while it lives, and the command deletes it when
-- the relay answers. At most one row.
CREATE TABLE relay_enrollment (
    id         boolean     PRIMARY KEY DEFAULT true CHECK (id),
    challenge  text        NOT NULL,
    expires_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE relay_enrollment;
DROP TABLE web_login_link;
DROP TABLE device;
