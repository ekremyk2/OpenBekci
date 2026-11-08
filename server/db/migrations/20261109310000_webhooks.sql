-- +goose Up

-- Outbound signed webhooks (backend plan sections 11 and 16, release 0.4).
-- An endpoint subscribes to event types; the River outbox fans each core event
-- out into one delivery job per (event, endpoint). The delivery table is the
-- log the API lists; the job queue stays the source of truth for retries.
CREATE TABLE webhook_endpoint (
    id                   uuid        NOT NULL,
    org_id               uuid        NOT NULL REFERENCES organization (id),
    url                  text        NOT NULL CHECK (url ~ '^https://' AND length(url) <= 2000),
    -- AES-256-GCM (ENCRYPTION_KEY, purpose integrations.webhook_secret), the
    -- signing key of OpenBekci-Signature; shown once at creation
    secret_sealed        bytea       NOT NULL,
    events               text[]      NOT NULL CHECK (cardinality(events) BETWEEN 1 AND 50),
    active               boolean     NOT NULL DEFAULT true,
    description          text        CHECK (length(description) <= 500),
    -- set when a delivery used up its attempts; cleared by the next success
    failing_since        timestamptz,
    consecutive_failures int         NOT NULL DEFAULT 0,
    created_by           uuid,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    version              int         NOT NULL DEFAULT 1,
    PRIMARY KEY (org_id, id)
);
SELECT openbekci_enable_rls('webhook_endpoint');

CREATE TABLE webhook_delivery (
    id            uuid        NOT NULL,
    org_id        uuid        NOT NULL REFERENCES organization (id),
    endpoint_id   uuid        NOT NULL,
    -- the core event (id, type) and its JSON body, ids only
    event_id      uuid        NOT NULL,
    event_type    text        NOT NULL,
    body          jsonb       NOT NULL,
    -- pending: queued or waiting for a retry; succeeded; failed: attempts used up
    status        text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed')),
    attempts      int         NOT NULL DEFAULT 0,
    response_code int,
    last_error    text,
    next_retry_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    delivered_at  timestamptz,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, endpoint_id, event_id),
    FOREIGN KEY (org_id, endpoint_id) REFERENCES webhook_endpoint (org_id, id) ON DELETE CASCADE
);
CREATE INDEX webhook_delivery_endpoint ON webhook_delivery (org_id, endpoint_id, id DESC);
SELECT openbekci_enable_rls('webhook_delivery');

-- +goose Down
DROP TABLE webhook_delivery;
DROP TABLE webhook_endpoint;
