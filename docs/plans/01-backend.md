# OpenBekci — Backend Plan

| | |
|---|---|
| Status | Draft v0.1 |
| Date | 2026-11-06 |
| Parent | [00-overview.md](00-overview.md) — decisions and shared contracts there bind this document |

---

## 1. Goals and non-goals

**Goals**

- One Go binary that serves the API, the embedded web app and (by default) the job worker, with PostgreSQL as the only required dependency.
- A modular monolith whose module boundaries are strong enough that a module could be extracted later, but which never pays the operational cost of microservices.
- Correct money: double-entry ledger, integer minor units, immutable postings, deterministic rounding, closed periods.
- Tenant isolation enforced by the database (RLS), not only by application code.
- Every extension a country pack needs is reachable through `pkg/sdk`; the core contains no country logic.
- Contract-first API: handlers are generated from the OpenAPI spec and the spec is validated in tests.

**Non-goals (v1)**

- Multi-currency within a site. Multi-region deployment. Sharding.
- Untrusted/third-party plugins loaded at runtime (WebAssembly sandbox deferred).
- GraphQL, gRPC, or a public event stream beyond signed webhooks.
- Full accounting for the management company (only the site's books).

---

## 2. Technology stack

| Concern | Choice | Why |
|---|---|---|
| Language | Go, current stable; we support the two latest releases per Go's policy | See overview §2 |
| HTTP | `net/http` + `chi` v5 for routing/middleware | Standard handlers, mature middleware (request ID, real IP, recoverer, timeout), supported by the codegen |
| OpenAPI codegen | `oapi-codegen` strict-server mode (spike `ogen` at B0; switch only if validation/perf wins clearly) | Generates typed handlers, request/response structs, and embeds the spec for runtime validation |
| Request validation | `kin-openapi` validation middleware (dev/test always, prod configurable) + domain validation | Catches contract drift early |
| Database driver | `pgx` v5 (pool) | Native Postgres types, performance, `LISTEN/NOTIFY` for River |
| Query layer | `sqlc` | Type-safe SQL, no ORM magic, easy to review |
| Migrations | `goose` with embedded SQL files; packs ship their own goose filesystems | Simple, widely known, transactional |
| Jobs | `riverqueue/river` | Transactional enqueue in the same tx as business data; periodic jobs; no Redis |
| Auth crypto | `golang.org/x/crypto/argon2`, `go-webauthn/webauthn`, `coreos/go-oidc` | Standard choices |
| Email | `wneessen/go-mail` + `html/template` with MJML-free responsive base template | Pure Go SMTP |
| Storage | own `storage.Blob` interface; local FS driver, S3 driver via `aws-sdk-go-v2` (works with MinIO, R2, …) | Self-host without S3; scale with it |
| Logging | `log/slog` JSON | Standard library |
| Tracing/metrics | OpenTelemetry SDK; OTLP exporter optional; Prometheus `/metrics` | Vendor-neutral |
| Config | env vars `OPENBEKCI_*`, optional YAML file via `--config`; parsed with `caarlos0/env` or `koanf` | 12-factor, easy in compose/Helm |
| CLI | `cobra` (or stdlib `flag` subcommands if we stay small) | `serve`, `worker`, `migrate`, `admin` |
| i18n (server-side) | `nicksnyder/go-i18n` v2 with JSON catalogs | Only for emails/PDFs/notification texts |
| PDF | Pure Go first (`maroto` v2 / `go-pdf/fpdf`); optional Gotenberg sidecar for rich HTML→PDF | Decide at B2 after a spike |
| Testing | stdlib `testing`, `go-cmp`, `testcontainers-go` (Postgres), `rapid` for property tests | Real Postgres in integration tests, no mocks of the DB |
| Lint/security | `golangci-lint`, `govulncheck`, `gosec` | CI gates |
| Release | GoReleaser; distroless images; cosign signatures; SBOM (syft) | Reproducible, verifiable |

**Deliberately not used:** ORMs (GORM, ent), decimal libraries for money (int64 minor units suffice), Redis, message brokers, dependency-injection frameworks (wiring is explicit Go code in `internal/app`).

---

## 3. Repository layout (`server/`)

```
server/
├─ cmd/openbekci/
│  ├─ main.go                 subcommand dispatch
│  ├─ serve.go                HTTP + (default) embedded worker
│  ├─ worker.go               worker-only process
│  ├─ migrate.go              up | down | status | create
│  ├─ admin.go                create-platform-admin, create-org, packs list, …
│  └─ packs_all.go            imports every official pack for side-effect registration
├─ internal/
│  ├─ app/                    config loading, wiring (hand-written DI), lifecycle, graceful shutdown
│  ├─ httpapi/
│  │  ├─ gen/                 oapi-codegen output — never edited by hand
│  │  ├─ server.go            StrictServerInterface implementation composed from module handlers
│  │  ├─ middleware/          request id, logging, auth, tenant, rate limit, idempotency, problem+json
│  │  └─ problem/             error → RFC 9457 mapping
│  ├─ platform/               infrastructure adapters, no business rules
│  │  ├─ db/                  pgx pool, tx helpers, tenant-scoped tx (RLS), sqlc generated package
│  │  ├─ storage/             Blob interface + local + s3 drivers
│  │  ├─ mail/                SMTP sender, template rendering
│  │  ├─ jobs/                River client, job registration helpers
│  │  ├─ events/              outbox writer + dispatcher
│  │  ├─ otel/                tracing/metrics setup
│  │  ├─ clock/               Clock interface (real + fake)
│  │  ├─ id/                  UUIDv7
│  │  ├─ i18n/                catalogs + localizer
│  │  └─ crypto/              field encryption (AES-GCM), token hashing, key rotation
│  ├─ core/                   one package per domain module (see §4)
│  │  ├─ tenancy/  identity/  people/  property/  ledger/  billing/  payments/
│  │  ├─ tickets/  announcements/  documents/  meetings/  bookings/  visitors/
│  │  ├─ notifications/  audit/  settings/  privacy/
│  │  └─ authz/               permissions, roles, Check()
│  ├─ packs/                  pack runtime: registry, per-org enablement, route mounting, migration runner
│  └─ webassets/              go:embed of ../../web/dist (built by web CI)
├─ pkg/sdk/                   PUBLIC pack SDK (Apache-2.0) — interfaces, event types, helpers, testkit
├─ db/
│  ├─ migrations/             goose: 20261106000001_init.sql …
│  ├─ queries/<module>/*.sql  sqlc inputs
│  └─ sqlc.yaml
├─ locales/                   server-side catalogs (en.json, tr.json, …)
└─ testdata/                  fixtures, golden files
```

### 3.1 Module anatomy

Every `internal/core/<module>` follows the same shape so contributors can find their way around any module:

```
core/ledger/
├─ ledger.go          domain types + invariants (pure Go; no DB, no HTTP)
├─ service.go         use cases; depends on Repo, Clock, Events, authz
├─ repo.go            Repo interface the service needs
├─ repo_pg.go         sqlc-backed implementation
├─ http.go            handlers implementing this module's slice of the generated server interface
├─ events.go          event type definitions this module emits
├─ jobs.go            River job definitions (if any)
├─ permissions.go     permission constants this module declares
└─ *_test.go
```

Rules:

- A module's `service.go` is the only entry point for other modules and for HTTP. Other modules never call a module's repo.
- Cross-module dependencies go through narrow interfaces **declared by the consumer** (e.g. `billing` declares `type UnitReader interface { ListUnits(ctx, siteID) }` and `property` satisfies it in wiring). This keeps the import graph acyclic and visible in `internal/app`.
- Side effects between modules (e.g. "payment received → notify resident") go through **domain events**, not direct calls, unless the operation must be atomic with the caller (then it's a direct service call inside the same transaction).
- `internal/core/*` never imports `internal/httpapi` or `internal/packs`.

---

## 4. Domain model

Entity lists are indicative; exact columns live in migrations. All tenant tables carry `id uuid (v7)`, `org_id`, `created_at`, `updated_at`, `version int` (optimistic locking) unless noted as append-only.

### 4.1 tenancy

| Entity | Notes |
|---|---|
| `organization` | Tenant. `slug`, `name`, `default_locale`, `timezone`, `country_code`, `status`, `settings jsonb`. Created by platform admin (self-host) or signup (SaaS) |
| `site` | Belongs to org. `name`, `address` (structured: country, admin areas, postal code, lines — pack may refine format), `timezone`, `currency`, `fiscal_year_start_month`, `settings jsonb` |
| `building` | Block within a site. Optional — a single-building site has one auto-created building |
| `unit` | `building_id`, `number`, `floor`, `type` (flat / shop / office / parking / storage / other), `area_m2 numeric`, `share_numerator`, `share_denominator` (arsa payı or equivalent), `status` (active / vacant / merged) |
| `facility` | Common area for bookings: `name`, `capacity`, `rules jsonb` (hours, max duration, advance days), `fee` Money nullable |

### 4.2 identity

| Entity | Notes |
|---|---|
| `user` | Instance-wide (not org-scoped). `email` (nullable, unique), `phone` (nullable, unique, E.164), `password_hash` (nullable), `locale`, `status`, `mfa_totp_secret` (encrypted), `is_platform_admin` |
| `external_identity` | `user_id`, `provider` (`google`, `tr.edevlet`, …), `subject`, `claims jsonb`. Providers are registered by core or packs |
| `passkey` | WebAuthn credentials |
| `session` | `user_id`, `kind` (web_cookie / refresh_token / api_key), `token_hash`, `expires_at`, `revoked_at`, `last_used_at`, `ip`, `user_agent`, `rotated_from` |
| `invitation` | `org_id`, `person_id`, `email/phone`, `token_hash`, `expires_at`, `accepted_at` |
| `login_attempt` | For rate limiting / lockout (append-only, pruned) |

Email and phone are both optional but at least one is required; this is how phone-first markets work. Phone OTP delivery needs an SMS channel, which core doesn't ship; until a pack provides one, phone-only users are created by invitation link and authenticate with password/passkey.

### 4.3 people

| Entity | Notes |
|---|---|
| `person` | Org-scoped. `user_id` nullable (a person exists before they sign up). `display_name`, `contact jsonb`, `notes`. National ID numbers are **not** in core; a pack stores them encrypted in its own table |
| `occupancy` | `person_id`, `unit_id`, `kind` (owner / tenant / resident / representative), `share_pct` (for co-owners), `valid_from`, `valid_to`. Statement visibility and voting eligibility derive from this |
| `membership` | `person_id`, `role`, `scope_type` (org / site / building / unit), `scope_id`, `valid_from`, `valid_to`. A person can hold several |
| `vendor` | Suppliers: `name`, `tax_id` (generic string), `contact jsonb`, `default_expense_account_id` |

Built-in roles: `org_admin`, `site_manager`, `board_member`, `auditor` (read-only over money and records), `accountant`, `staff`, `guard`, `owner`, `resident`. Custom roles are post-1.0.

### 4.4 ledger

| Entity | Notes |
|---|---|
| `account` | Per site. `code`, `name`, `type` (asset / liability / equity / income / expense), `subtype` (bank, cash, unit_receivable, vendor_payable, dues_income, late_fee_income, …), `is_system`. A default chart is created per site; packs may supply a localized chart |
| `journal_entry` | **Append-only.** `site_id`, `entry_date`, `period_id`, `memo`, `source_type`, `source_id` (charge, payment, bill, manual, reversal), `posted_by`, `posted_at`, `reverses_entry_id` |
| `journal_line` | **Append-only.** `entry_id`, `account_id`, `debit` bigint, `credit` bigint (exactly one non-zero), `unit_id` nullable (unit sub-ledger), `vendor_id` nullable, `person_id` nullable |
| `period` | `site_id`, `year`, `month`, `status` (open / closed), `closed_by`, `closed_at` |
| `account_balance` | Cache: `account_id`, `unit_id?`, `period_id`, `debit_total`, `credit_total`; updated in the same transaction as the posting. Source of truth remains the lines; a `rebuild-balances` admin command exists |

Invariants, enforced in Go **and** by a deferred constraint trigger: Σ debit = Σ credit per entry; no lines on closed periods; no `UPDATE`/`DELETE` on `journal_*` (the app role simply has no grant).

### 4.5 billing

| Entity | Notes |
|---|---|
| `charge_plan` | Dues definition. `site_id`, `name`, `kind` (recurring / one_off), `frequency` (monthly / quarterly / yearly), `start`, `end`, `due_day`, `amount_mode` (`total` to allocate, or `per_unit`), `amount` Money, `allocation_policy` (`equal` / `by_share` / `by_area` / `by_occupants` / `fixed_table` / pack-provided id), `unit_filter jsonb`, `income_account_id`, `status` |
| `charge` | One per unit per period: `plan_id?`, `unit_id`, `period_id`, `kind` (dues / expense_share / late_fee / booking_fee / adjustment), `amount`, `due_date`, `description`, `journal_entry_id`, `status` (open / partially_paid / paid / cancelled) |
| `expense_allocation` | A vendor bill or ad-hoc cost shared to units → produces charges |
| `late_fee_run` | Idempotency record: `charge_id`, `as_of_date`, `fee_charge_id` |
| `charge_payment` | Allocation of a payment to a charge: `payment_id`, `charge_id`, `amount` |

### 4.6 payments

| Entity | Notes |
|---|---|
| `payment` | `site_id`, `payer_person_id?`, `unit_id?`, `method` (cash / bank_transfer / card / provider:<id>), `amount`, `received_at`, `reference`, `status` (pending / confirmed / failed / refunded), `asset_account_id`, `journal_entry_id`, `provider_payload jsonb` |
| `bank_statement_import` | `importer_id`, `file_id`, `status`, `summary` |
| `bank_transaction` | Parsed line: `import_id`, `booked_at`, `amount`, `counterparty`, `description`, `raw jsonb`, `match_status`, `payment_id?` |
| `matching_rule` | Site-defined rules (regex on description → unit) feeding auto-matching |
| `bill` | Vendor invoice: `vendor_id`, `expense_account_id`, `amount`, `issued_at`, `due_at`, `file_id?`, `status`, `journal_entry_id` |
| `bill_payment` | Pays a bill from an asset account |

### 4.7 tickets

`ticket` (`site_id`, `unit_id?`, `reporter_person_id`, `category_id`, `priority`, `status`, `assignee_person_id?`, `title`, `body`, `location`), `ticket_comment` (`visibility`: public / internal), `ticket_attachment` → `file`, `ticket_category` (per site, with default assignee role), `ticket_status_history` (append-only). Workflow: `open → triaged → in_progress → resolved → closed`, with `reopen` from resolved/closed within a window.

### 4.8 announcements

`announcement` (`scope_type` site / building, `scope_id`, `title`, `body_md`, `publish_at`, `expires_at`, `pinned`, `audience` all / owners / residents / board / staff, `notify_channels`), `announcement_read` (person, read_at).

### 4.9 documents

`folder` (tree per site, `visibility` rules inherited), `document` (`folder_id`, `title`, `current_version_id`, `visibility`: board / all / units[] / roles[]), `document_version` (`file_id`, `created_by`, `note`). `file` is the generic blob record (§13).

### 4.10 meetings and voting

| Entity | Notes |
|---|---|
| `meeting` | `site_id`, `kind` (general_assembly / extraordinary / board), `scheduled_at`, `location`, `status`, `quorum_rule_id` (core default or pack), `second_call_at` (some laws have a second meeting without quorum), `minutes_document_id?` |
| `agenda_item` | ordered, `title`, `body_md`, `requires_vote` |
| `attendance` | `person_id`, `unit_ids[]` represented, `mode` (in_person / proxy / online), `proxy_for_person_id?`, `checked_in_at` |
| `vote` | `agenda_item_id`, `method` (per_unit / per_share / per_person / pack rule id), `options[]`, `threshold_rule_id`, `opens_at`, `closes_at`, `status`, `result jsonb` |
| `ballot` | `vote_id`, `voter_person_id`, `unit_id`, `weight numeric`, `option`, `cast_at` — append-only |
| `decision` | `meeting_id`, `agenda_item_id`, `text`, `outcome`, `sequence_no` (for a decision book) |

Quorum, weights and thresholds are computed through the `LegalRules` extension point; the core ships a generic implementation (simple majority of represented shares, quorum = 50%+1 of units) that every country can override.

### 4.11 bookings

`booking` (`facility_id`, `unit_id`, `person_id`, `starts_at`, `ends_at`, `status` requested / confirmed / cancelled / rejected, `charge_id?`), exclusion constraint on `(facility_id, tstzrange)` for confirmed bookings.

### 4.12 visitors

`visitor_entry` (`site_id`, `unit_id`, `visitor_name`, `vehicle_plate?`, `expected_from`, `expected_to`, `status` expected / arrived / left / denied, `registered_by_person_id`, `arrived_at`, `left_at`, `gate_note`), `vehicle` (unit-registered plates), `delivery` (parcel log — optional in v1).

### 4.13 notifications

| Entity | Notes |
|---|---|
| `notification` | `user_id`, `org_id`, `type`, `payload jsonb`, `created_at`, `read_at`, `link` |
| `notification_delivery` | `notification_id`, `channel` (in_app / email / push / `pack:<id>`), `status`, `attempts`, `last_error`, `sent_at` |
| `notification_preference` | per user per org per type → channels + digest mode |
| `device` | push registrations: `user_id`, `platform`, `token` (encrypted), `relay_id`, `last_seen_at` |
| `template_override` | org-level overrides of notification templates per locale |

### 4.14 audit

`audit_event` (**append-only**): `org_id`, `actor_user_id?`, `actor_kind` (user / system / pack / job), `action` (`ticket.update`), `target_type`, `target_id`, `before jsonb`, `after jsonb` (diff only), `ip`, `request_id`, `at`. Written inside the business transaction by the service layer via `audit.Record(ctx, …)`.

### 4.15 settings, privacy

`setting` (`org_id`, `site_id?`, `key`, `value jsonb`, validated against a JSON Schema registered by core or pack), `org_pack` (`org_id`, `pack_id`, `enabled`, `settings jsonb`), `feature_flag`.

`data_export_request` and `erasure_request` (`person_id`, `status`, `requested_by`, `completed_at`, `artifact_file_id?`) back the privacy workflows (§16.5).

---

## 5. Multi-tenancy

- Single database, shared schema. Every tenant table has `org_id uuid not null` as the **leading column of every index** that the app queries through.
- **Row-level security on every tenant table:**
  ```sql
  ALTER TABLE unit ENABLE ROW LEVEL SECURITY;
  ALTER TABLE unit FORCE ROW LEVEL SECURITY;
  CREATE POLICY unit_tenant ON unit
    USING (org_id = current_setting('app.org_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.org_id', true)::uuid);
  ```
- The application connects as `openbekci_app`, which does **not** own the tables (migrations run as `openbekci_migrator`). Non-owners can't bypass RLS, so a missing `WHERE org_id = …` in application code returns nothing rather than leaking.
- Per request: `db.InTenantTx(ctx, orgID, func(tx) error)` does `BEGIN; SET LOCAL app.org_id = $1; … COMMIT`. There is no way to get a tenant connection without going through this helper; `platform/db` doesn't export the raw pool to `core`.
- Jobs iterate organizations and run each unit of work inside its own tenant transaction. Cross-tenant reads (platform admin screens, metrics) use `db.InPlatformTx`, which sets `app.org_id` to a sentinel and is only callable from `internal/core/platformadmin` (enforced by a lint rule / `depguard`).
- `user` and `session` are instance-wide (no `org_id`); membership rows link users into orgs.
- Self-hosting changes nothing: it's an instance with one (or a few) organizations. SaaS-specific org lifecycle (signup, plan limits, billing the org) lives outside this repo and attaches via the SDK's platform hooks.

Migration test: a CI test creates two orgs, inserts rows in each, and asserts every table in `information_schema` either has an RLS policy or is on the explicit instance-wide allowlist.

---

## 6. Authentication

| Method | Core/Pack | Notes |
|---|---|---|
| Email + password | core | argon2id (`t=3, m=64MiB, p=4`, tune at B0), minimum length 10, optional HIBP k-anonymity check |
| Magic link (email) | core | 15-minute single-use token |
| Passkeys (WebAuthn) | core | Primary passwordless path for web; mobile via platform authenticators |
| TOTP second factor | core | Optional per user; org can require it for admin roles |
| Phone OTP | needs an SMS channel | Core wires the flow; a pack provides the channel |
| External OIDC (Google, Microsoft, generic) | core | For orgs that want SSO; maps to `external_identity` |
| e-Devlet, other government IdPs | pack | Pack implements `sdk.IdentityProvider`; core handles session issuance and linking |

Sessions:

- **Web:** `POST /v1/auth/session` sets an httpOnly, Secure, `SameSite=Lax` cookie containing an opaque token (hash stored in `session`). Mutating requests must carry `Origin`/`Sec-Fetch-Site` consistent with the configured base URL, plus the SPA sends `X-Requested-With`; this is sufficient CSRF defense for a JSON-only API without a double-submit token.
- **Mobile / integrations:** `POST /v1/auth/token` returns an access JWT (15 min, HS256 with instance secret — no external verifiers needed, so no key distribution) and a rotating refresh token (default 60 days, hash stored, old token invalidated on rotation, reuse detection revokes the family).
- **API keys:** long-lived, scoped to an org and a permission set, for integrations. Prefix `obk_`.
- One `session` table backs all three; `GET /v1/me/sessions` and revocation work uniformly.
- Invitation flow: manager creates a `person`, optionally sends an invitation; accepting creates/links a `user` and a `membership`.
- A user with memberships in several orgs chooses with the `{org_id}` path segment; `GET /v1/me` lists their orgs and roles.

---

## 7. Authorization

- **Permissions** are string constants declared per module (`tickets.read`, `tickets.write`, `tickets.assign`, `ledger.read`, `ledger.post`, `ledger.close_period`, `billing.manage_plans`, `payments.record`, `meetings.manage`, `votes.cast`, …). The full list, with the role → permission matrix, is generated into the OpenAPI spec (`components.schemas.Permission` enum and `x-openbekci-roles`) so clients can consume it.
- **Roles** are built-in sets of permissions, assigned through `membership` with a **scope** (org / site / building / unit). A `site_manager` of site A has no rights in site B.
- **Resource rules** on top of RBAC: a `resident`/`owner` can read their own units' statements, tickets and bookings only; an `owner` can vote only for units they own at the vote's eligibility date; `auditor` is read-only everywhere.
- Implementation: middleware resolves the caller's memberships for the org (cached per request), puts a `authz.Principal` in the context; services call `authz.Require(ctx, perm, authz.Scope{Site: id})` or `authz.CanAccessUnit(ctx, unitID)`. **Checks live in the service layer**, so jobs, packs and future transports are covered, not only HTTP.
- `platform_admin` is a flag on `user`, not a membership; it unlocks instance endpoints under `/v1/platform/…` only.
- Tests: an authz matrix test enumerates every operation in the OpenAPI spec × every role × "own resource / other resource" and asserts the expected 200/403. Adding an endpoint without a row fails the test.

---

## 8. API design

The spec lives in `api/` (split per module, bundled with `redocly bundle` into `api/openapi.yaml`). Conventions from overview §6.2 apply. Additional backend specifics:

- **Path shape:** `/v1/orgs/{org_id}/<resource>` and `/v1/orgs/{org_id}/<resource>/{id}`; sub-resources only where the child cannot exist alone (`/tickets/{id}/comments`). Site scoping via `?site_id=`. Instance endpoints under `/v1/platform/…`; identity/self under `/v1/auth/…` and `/v1/me/…`; packs under `/v1/orgs/{org_id}/packs/{pack_id}/…`.
- **Operations** are named `<module>.<verb><Noun>` (`tickets.listTickets`) — this becomes the generated method names in all three clients.
- **Problem details:** every error is `{ type, title, status, code, detail?, instance, errors?: [{ field, code, params? }] }`. `code` values are an enum in the spec (`validation_failed`, `not_found`, `conflict`, `period_closed`, `insufficient_permission`, …). Domain errors map to codes in `httpapi/problem`.
- **Idempotency:** `Idempotency-Key` required on `POST` for payments, charges, journal entries, bills, bookings; stored in `idempotency_key` (`org_id`, `key`, `request_hash`, `response`, `expires_at` 24h). Same key + different body → `422 idempotency_mismatch`.
- **Concurrency:** resources with `version` accept `If-Match: "<version>"` on `PATCH`/`PUT`; mismatch → `412 version_conflict`.
- **Pagination:** opaque base64 cursor encoding `(sort_key, id)`; `limit` max 200, default 50.
- **Filtering:** explicit query params per list endpoint (no generic query language). Full-text search on tickets/announcements/documents via Postgres `tsvector` with a per-site language config.
- **Bulk:** unit and person import via `POST …/units:import` accepting CSV/XLSX → async job → result report. Exports similarly.
- **Rate limiting:** per IP for auth endpoints (strict), per user elsewhere; token bucket in memory per instance, with a Postgres-backed fallback table for multi-replica deployments. `429` with `Retry-After`.
- **Webhooks (B6):** org-configured endpoints subscribed to event types; HMAC-SHA256 signed; retries with backoff via River.
- **Realtime:** `GET /v1/me/events` SSE stream for in-app notifications (post-1.0 unless the web plan needs it earlier). Polling is acceptable at 1.0.
- **Response validation** runs in tests and dev (`OPENBEKCI_API_VALIDATE_RESPONSES=true`) so handlers can't drift from the spec.
- **Web-driven requirements** (from the [web plan](02-web.md) §18, shipped with the matching backend milestone): `index.html` served as a template with a per-response CSP nonce and instance branding, immutable hashed assets (B0); operations tagged `x-idempotent` in the spec (B0); `403 reauth_required` + `POST /v1/auth/reauth` (B1); import endpoints accept `dry_run=true` and return a row-level validation report (B1); `POST …/charge-plans:preview` returning per-unit allocations without persisting (B2); pack settings schema endpoint with `x-ui` uiSchema conventions (B5); cheap unread-count endpoint (B3); `Cache-Control: no-store` on statements and personal data (B2).
- **Mobile-driven endpoints** (from the [mobile plan](03-mobile.md) §16, shipped in B5): unauthenticated `GET /.well-known/openbekci.json` discovery document (instance name, API base URL and versions, `min_app_version`, auth methods, push relay id, enabled pack ids, branding) and its authenticated twin `GET /v1/instance`; `GET /v1/me` aggregate (orgs, roles, effective permissions, enabled packs, policies, units); `GET /v1/orgs/{org}/me/dashboard`; `updated_since` + `ETag`/`If-None-Match` on cacheable lists; `POST/DELETE /v1/me/devices` (`push_kind`, `relay_id`); a fast `GET /v1/me/notifications/{id}`; image `?variant=thumb|medium`; a one-time web-login link; URL-based payment checkout (`checkout_url`, `return_url`) with a status poll.

---

## 9. Money and ledger rules

1. **Representation:** `Money{Amount int64, Currency string}`; the minor-unit exponent comes from a currency table (`TRY`=2, `JPY`=0, …). Arithmetic helpers refuse to mix currencies (panic in tests, error in prod).
2. **Double entry always.** Charges, payments, bills, fees, adjustments, booking fees — each posts a balanced `journal_entry`. There is no "balance" column that is not derived from lines.
3. **Immutability.** Entries are never updated or deleted. A mistake is corrected by posting a **reversal** (`reverses_entry_id`) and a new entry. The app role has no `UPDATE/DELETE` grant on `journal_entry`/`journal_line`/`ballot`/`audit_event`.
4. **Periods.** Monthly. Closing a period locks postings dated within it; postings for closed periods are rejected with `period_closed`. Reopening requires `ledger.close_period` and writes an audit event.
5. **Allocation rounding.** When a total is split across units, remainders are distributed by the largest-remainder method in a deterministic order (unit number, then id) so Σ allocations == total exactly. This is tested with property tests.
6. **Allocation policies** implement `sdk.AllocationPolicy{ Allocate(total Money, units []sdk.UnitWeight) ([]sdk.Allocation, error) }`. Core: `equal`, `by_share`, `by_area`, `by_occupants`, `fixed_table`. Packs may register more (e.g. a legally mandated formula).
7. **Late fees** implement `sdk.LateFeePolicy{ Fee(charge, asOf, paidSoFar) (Money, error) }`. Core default: configurable % per month after a grace period, simple (not compound). The Turkish pack supplies the Law 634 rate. A daily job computes fees idempotently via `late_fee_run`.
8. **Payment allocation:** default oldest-due-first across a unit's open charges; manual re-allocation allowed while the period is open; over-payments sit as a credit on the unit sub-ledger.
9. **Statements:** per unit, per date range: opening balance, charges, payments, fees, closing balance; computed from lines, exported to PDF/CSV; cached per (unit, period) once the period is closed.
10. **Reports (core):** trial balance, income/expense by account and period, receivables aging, cash/bank book, budget vs actual (budget entity post-1.0).
11. **Tests:** golden fixtures (a whole year of a 24-unit site), invariant property tests, and a "replay" test that rebuilds `account_balance` from lines and compares.

---

## 10. Pack / extension system

### 10.1 SDK surface (`pkg/sdk`, Apache-2.0)

```go
package sdk

type Pack interface {
    Manifest() Manifest                  // ID ("tr"), Version, MinSDK, DisplayName, Countries []string
    Register(ctx context.Context, r *Registry) error
}

// Registry — what a pack may contribute. Each Register* validates and namespaces.
func (r *Registry) IdentityProvider(p IdentityProvider)
func (r *Registry) PaymentProvider(p PaymentProvider)
func (r *Registry) NotificationChannel(c NotificationChannel)
func (r *Registry) StatementImporter(i StatementImporter)
func (r *Registry) LegalRules(l LegalRules)                 // quorum, vote weights, thresholds, late fee defaults
func (r *Registry) AllocationPolicy(id string, p AllocationPolicy)
func (r *Registry) LateFeePolicy(id string, p LateFeePolicy)
func (r *Registry) DocumentTemplate(t DocumentTemplate)     // decision book, minutes, statements, receipts
func (r *Registry) ChartOfAccounts(c ChartOfAccounts)
func (r *Registry) AddressFormat(f AddressFormat)
func (r *Registry) Settings(schema SettingsSchema)          // JSON Schema + UI hints → generic forms in clients
func (r *Registry) Migrations(fsys fs.FS)                   // goose files; tables must be prefixed pack_<id>_
func (r *Registry) Routes(fn func(RouteGroup))              // mounted at /v1/orgs/{org}/packs/<id>/
func (r *Registry) Job(def JobDefinition)                   // River jobs, optionally periodic
func (r *Registry) Subscribe(eventType string, h EventHandler)
func (r *Registry) ConsentTexts(c ConsentTexts)             // privacy/legal texts per locale
func (r *Registry) Validator(id string, v Validator)        // national id, tax id, plate formats … (people, vendors, forms)
func (r *Registry) ReferenceData(id string, d ReferenceData)// admin areas, banks, holidays (address forms, scheduling)
// InvoiceIssuer is reserved for post-1.0 (e-invoicing); see 04-packs.md §2.

// Services a pack may call (handed in via Registry.Services()):
type Services interface {
    Units() UnitReader; People() PeopleReader; Ledger() LedgerPoster; Payments() PaymentRecorder
    Notifications() Notifier; Files() FileStore; Settings() SettingsReader
    DB() TenantDB       // pack-prefixed tables only; same RLS tx
    Logger() *slog.Logger; Clock() Clock
}
```

Design rules:

- Packs import **only** `pkg/sdk`. Go's `internal/` rule enforces this.
- Everything a pack registers is namespaced by its ID (routes, settings keys, tables, job kinds, event handler names). The registry rejects collisions.
- A pack is **enabled per organization** (`org_pack`). Disabled packs' routes return 404, their jobs skip the org, their event handlers are not invoked. Settings schemas are still visible so an admin can enable and configure.
- Packs never modify core tables directly; they post through `LedgerPoster`, which runs the same invariants as the core.
- **Compile-time linking:** `cmd/openbekci/packs_all.go` imports official packs (`_ "github.com/openbekci/openbekci/packs/tr"`). A `-tags nopacks` build excludes them. Private packs are added by forking that one file or by a tiny custom `main` — documented in the pack authoring guide.
- **SDK versioning:** semver; `Manifest.MinSDK` is checked at startup. Breaking SDK changes are batched into major versions with migration notes.
- **Testkit:** `pkg/sdk/testkit` spins up a Postgres (testcontainers), an in-memory core, and lets a pack test its own routes/jobs/policies in isolation.

### 10.2 Extension point ↔ Turkish pack mapping (drives validation of the SDK)

| Extension point | `packs/tr` |
|---|---|
| `IdentityProvider` | e-Devlet (OAuth-style flow) → verified TCKN stored encrypted in `pack_tr_person_identity` |
| `PaymentProvider` | iyzico (first), PayTR |
| `NotificationChannel` | SMS via Netgsm / İleti Merkezi |
| `StatementImporter` | CSV/XLSX formats of major banks |
| `LegalRules` | Law 634: quorum (majority of shares *and* owners), second-meeting rule, decision thresholds by topic, 5%/month late fee |
| `DocumentTemplate` | Karar defteri, toplantı tutanağı, aidat makbuzu, hesap ekstresi |
| `ChartOfAccounts` | Localized default chart |
| `ConsentTexts` | KVKK aydınlatma ve açık rıza metinleri |
| `Settings` | Tax office, e-Fatura credentials, provider keys |
| `Validator`, `ReferenceData` | TCKN/vergi no/plate validators; il–ilçe–mahalle lists, public holidays |
| e-Fatura / e-Arşiv | `InvoiceIssuer` — **not in the first SDK cut**; interface reserved, implemented post-1.0 (packs plan TR-5) |

The full extension-point catalogue, pack lifecycle, governance and the Turkish pack's scope and sequencing are in [04-packs.md](04-packs.md).

---

## 11. Events, outbox, jobs

- **Domain events** are Go structs with a stable string type (`ledger.entry_posted`, `billing.charge_created`, `payments.payment_confirmed`, `tickets.status_changed`, `meetings.vote_closed`, …) and a JSON payload defined in `pkg/sdk/events` (so packs can subscribe with types).
- **Transactional outbox:** `events.Emit(ctx, ev)` inserts into `outbox` in the caller's tenant transaction. Commit → River job `outbox.dispatch` (enqueued in the same tx) → in-process dispatcher fans out to subscribers (notifications, audit projections, pack handlers, webhooks). At-least-once; every handler is idempotent (dedupe on `event_id` in `event_delivery`).
- **River jobs** (all tenant-scoped and idempotent):

| Job | Schedule | Module |
|---|---|---|
| `billing.generate_charges` | daily 02:00 site-local; generates for plans whose period starts | billing |
| `billing.apply_late_fees` | daily | billing |
| `billing.send_reminders` | daily, per site policy (before due, after due) | billing/notifications |
| `payments.poll_provider` | provider-defined | packs |
| `payments.import_statement` | on upload | payments |
| `notifications.deliver` | on demand, retries with backoff | notifications |
| `notifications.digest` | daily/weekly per user preference | notifications |
| `documents.render` | on demand (PDF generation) | documents |
| `privacy.export`, `privacy.erase` | on request | privacy |
| `maintenance.prune` | daily (sessions, login attempts, idempotency keys, old outbox rows) | platform |
| `webhooks.deliver` | on demand | integrations |

- Worker placement: `openbekci serve` runs the worker in-process by default (`--no-worker` to disable); `openbekci worker` runs it alone for larger deployments. River's leader election handles periodic jobs across replicas.

---

## 12. Notifications

- Channels: `in_app` (always), `email` (SMTP configured), `push` (relay configured), pack channels (`sms:netgsm`, …). Each implements `sdk.NotificationChannel{ ID(); Send(ctx, Delivery) error; Capabilities() }`.
- Routing: notification `type` has a default channel set; user preference overrides per type; org can force channels for legally relevant notices (assembly invitations).
- Templates: per type, per locale, Go templates for subject/body (text + HTML); org overrides via `template_override`. Rendering uses the recipient's locale and the site's timezone.
- Push payload (via relay): content-free `data` limited to `notification_id`, `org_id`, `type`, `collapse_key`, `badge`; the app fetches the notification. The relay protocol (enrollment with a domain challenge via the discovery document's `relay_enrollment_challenge`, HMAC-signed `POST /v1/send` batches, `invalid_token` feedback that deletes the `device` row) is specified in [05-infra.md §4](05-infra.md); the backend ships the client and `admin push-relay enroll` in B5. UnifiedPush endpoints are posted to directly, without a relay.
- Digests: users may choose "immediate" or "daily digest" per type.
- Delivery tracking in `notification_delivery` with errors visible to org admins.

---

## 13. Files and storage

- `storage.Blob` interface: `Put`, `Get`, `Delete`, `PresignUpload`, `PresignDownload`. Drivers: `local` (default; files under `OPENBEKCI_DATA_DIR/files`, served through the API with signed short-lived URLs) and `s3`.
- `file` table: `org_id`, `key`, `size`, `mime`, `sha256`, `uploaded_by`, `scope_type/scope_id` (what it's attached to), `status` (pending / ready / quarantined), `retention_until?`.
- Upload flow: `POST /files` → server returns either a presigned S3 URL or a direct upload URL on the server (local) → client uploads → `POST /files/{id}:finalize` verifies size/hash/mime (sniffed, not trusted from client). Limits: configurable max size (default 25 MB), mime allowlist.
- Optional ClamAV scan hook (`OPENBEKCI_CLAMAV_ADDR`) sets `quarantined`.
- Image handling: server generates thumbnails for ticket/announcement images (pure Go `imaging`), strips EXIF location.
- Downloads: never public; `GET /files/{id}` checks the attached resource's visibility and redirects to a presigned URL (S3) or streams (local).

---

## 14. Internationalization and localization

- API errors and enums are codes; clients own the strings.
- Server-rendered content (emails, SMS texts, PDFs, notification bodies) uses `locales/<lang>.json` catalogs with ICU-style plurals, fallback chain per overview §6.6.
- Every site has a timezone; dues due-dates, "daily" jobs and statements are computed in site time; storage is always `timestamptz`.
- Number/currency formatting on the server (PDFs, emails) uses `golang.org/x/text` with the recipient's locale.
- Address is a structured JSON object with a generic schema; `AddressFormat` from a pack can supply field order, labels and validation.
- Country and currency reference data ship in core (ISO lists).

---

## 15. Observability

- `slog` JSON to stdout; fields: `request_id`, `org_id`, `user_id` (never emails), `route`, `status`, `duration_ms`. Debug level never logs bodies.
- OpenTelemetry: HTTP server, pgx, River and outgoing HTTP instrumented; OTLP exporter on when `OTEL_EXPORTER_OTLP_ENDPOINT` is set.
- Prometheus `/metrics` (protected by a bearer token or network policy): request latencies, job queue depth/latency, outbox lag, DB pool stats, notification delivery outcomes.
- `GET /healthz` (process up), `GET /readyz` (DB reachable, migrations current, storage reachable), `/debug/pprof` behind `OPENBEKCI_DEBUG=true`.
- Error reporting: optional Sentry-compatible DSN.

---

## 16. Security and privacy

### 16.1 Application
- Password hashing argon2id; constant-time comparisons; login rate limits + progressive delay; lockout notifications.
- Sessions revocable; refresh-token reuse detection; `Secure`/`HttpOnly`/`SameSite` cookies; CSRF defense per §6.
- Security headers: strict CSP for the embedded SPA (nonce-based), HSTS, `X-Content-Type-Options`, `Referrer-Policy`. CORS only for explicitly configured extra origins (the embedded SPA needs none; mobile needs none).
- All SQL via sqlc; no string-built queries. All input validated at the boundary by the generated layer and again by domain rules.
- Sensitive fields (TOTP secrets, device tokens, pack-stored national IDs, provider credentials) encrypted with AES-256-GCM using a key from `OPENBEKCI_ENCRYPTION_KEY`; key IDs stored with ciphertext to allow rotation (`admin rotate-keys`).
- Re-authentication required for: changing email/phone/password, disabling MFA, closing periods, erasing a person, managing API keys.
- Uploaded files: mime sniffing, size limits, no execution, served with `Content-Disposition: attachment` for non-image types.

### 16.2 Tenant isolation
- RLS everywhere (§5) + the "every table has a policy" CI test + cross-tenant negative tests per module.

### 16.3 Supply chain
- `govulncheck` and Dependabot in CI; pinned module versions; GoReleaser with SBOM and cosign; distroless image running as non-root.

### 16.4 Audit
- Every mutation writes `audit_event` in-transaction; admins can browse/export; append-only at the DB grant level.

### 16.5 Privacy (GDPR / KVKK-ready in core)
- **Export:** a person's data (profile, occupancies, charges, payments, tickets, ballots they cast, files they uploaded) as a JSON + files archive, produced by a job.
- **Erasure/anonymization:** the ledger cannot lose lines, so erasure anonymizes the `person` (name → "Former resident #…", contacts removed, user unlinked, external identities deleted, pack-stored identifiers deleted via a pack hook) while journal lines keep their `unit_id`. Erasure is blocked while the person has open balances unless an admin overrides (audited).
- **Consent records:** `consent` (`person_id`, `text_id`, `version`, `accepted_at`, `ip`); texts come from core (generic) or packs.
- **Retention:** per-org settings for log and visitor-entry retention; `maintenance.prune` honours them.
- A threat model document (`docs/security/threat-model.md`) is written at B1 and updated per milestone.

---

## 17. Configuration and operations

Environment variables (`OPENBEKCI_` prefix; YAML file optional):

| Variable | Required | Notes |
|---|---|---|
| `DATABASE_URL` | yes | pgx URL; app role |
| `MIGRATOR_DATABASE_URL` | no | defaults to `DATABASE_URL`; used by `migrate` |
| `BASE_URL` | yes | public URL, used for links, cookies, CORS, OIDC callbacks |
| `SECRET_KEY` | yes | ≥32 bytes; signs JWTs and cookies |
| `ENCRYPTION_KEY` | yes | ≥32 bytes; field encryption |
| `DATA_DIR` | no | local storage root (default `/var/lib/openbekci`) |
| `STORAGE_*` | no | `s3` driver settings |
| `SMTP_*` | no | without it, email channel is disabled and the admin UI says so |
| `PUSH_RELAY_URL`, `PUSH_RELAY_TOKEN` | no | enables push |
| `OTEL_*`, `METRICS_TOKEN`, `SENTRY_DSN` | no | observability |
| `WORKER` | no | `embedded` (default) / `off` |
| `API_VALIDATE_RESPONSES`, `DEBUG` | no | dev aids |

Commands:

```
openbekci serve [--no-worker]        # HTTP + worker
openbekci worker                     # worker only
openbekci migrate up|down|status     # also runs pack migrations
openbekci admin create-platform-admin --email …
openbekci admin create-org --slug … --name … --country TR
openbekci admin packs list|enable|disable --org …
openbekci admin rotate-keys
openbekci admin rebuild-balances --site …
openbekci version
```

Operations:

- Secret-bearing variables accept a `_FILE` variant (`OPENBEKCI_SECRET_KEY_FILE`) for Docker/Kubernetes secret mounts; `OPENBEKCI_TRUSTED_PROXIES` lists CIDRs whose `X-Forwarded-*` headers are honoured. A startup check verifies that `SET LOCAL` survives on the pool's connections (transaction-mode poolers break tenant context) and refuses to run otherwise.
- `migrate` is run explicitly by default; `OPENBEKCI_AUTO_MIGRATE=true` for simple compose setups. Migrations follow expand/contract so version N+1 can run against N's schema briefly (zero-downtime for SaaS).
- Backups are plain `pg_dump` + copying `DATA_DIR` (or the S3 bucket); the infra plan documents and tests restore.
- Upgrades: pull new image, run `migrate up`, start. Downgrades one version are supported by `migrate down` only for migrations marked reversible.
- Image: distroless, non-root, multi-arch (amd64/arm64); the web `dist` is baked in at build time.

---

## 18. Testing strategy

| Layer | What | Tooling |
|---|---|---|
| Domain unit | Ledger invariants, allocation rounding, late fee math, quorum/vote math, workflow transitions, permission matrix | `testing`, `rapid` property tests, table tests |
| Repository/integration | sqlc queries against real Postgres; RLS isolation; triggers; migrations up→down→up | `testcontainers-go`, one DB per package, tx-rollback per test |
| API | In-process server + generated Go client; request/response validated against the spec; authz matrix; idempotency; pagination | `kin-openapi` validator in test mode |
| Pack SDK | `testkit` exercising registry validation, namespacing, enable/disable semantics; the TR pack's own tests | `pkg/sdk/testkit` |
| Jobs | River in test mode (synchronous), fake clock for schedules | `river/rivertest`, `clock.Fake` |
| Golden | PDFs (text layer compared), emails, statement CSVs, a full-year 24-unit ledger fixture | `testdata/` with an `-update` flag |
| Performance smoke | k6 scripts for list endpoints and statement generation on a 500-unit site | B6 onwards, nightly |

Targets: domain packages ≥ 90 % line coverage, overall ≥ 70 %; integration tests run on every PR (Postgres service container), total CI under 10 minutes.

---

## 19. Development workflow

- `Taskfile.yml`: `task gen` (bundle spec → oapi-codegen → sqlc → permission matrix → TS/Dart clients), `task lint`, `task test`, `task test:integration`, `task run` (with hot reload via `air`), `task migrate:new name=…`, `task db:reset`.
- `docker compose -f deploy/compose.dev.yml up` provides Postgres (+ MinIO, Mailpit for emails) for local development.
- CI (GitHub Actions): `generate-check` (regenerate, `git diff --exit-code`), lint, `govulncheck`, unit, integration, build matrix (linux/amd64, linux/arm64, darwin, windows), image build on `main`, GoReleaser on tags.
- Branching: trunk-based; short-lived PRs; Conventional Commits; squash merge; `CHANGELOG.md` generated by release tooling.
- Code conventions: errors wrapped with `%w` and context; domain sentinel errors (`ledger.ErrPeriodClosed`) mapped to problem codes in one place; no `panic` on request paths; contexts everywhere; no global state except the pack registry (populated at init, frozen after startup).
- ADRs for: oapi-codegen vs ogen (B0), PDF approach (B2), SSE vs polling (B3), webhook signing (B6).

---

## 20. Backend milestones

Each milestone ends with its exit criteria met in CI and a tagged pre-release.

| # | Name | Deliverables | Exit criteria |
|---|---|---|---|
| **B0** | Skeleton (≈2 wks) | Repo layout, config, `serve`/`migrate`, pgx+sqlc+goose, River wired, OpenAPI pipeline (auth + health endpoints), problem+json, slog/otel, RLS helper + "every table has a policy" test, Dockerfile + compose, CI | `docker compose up` → `/readyz` green; generated TS/Dart clients build; ADR-0001 (codegen choice) merged |
| **B1** | Tenancy, identity, people (≈4 wks) | Orgs/sites/buildings/units/facilities CRUD + CSV import; users, sessions (cookie + token), password/magic link/passkeys/TOTP, invitations; persons, occupancies, memberships; roles & permissions + matrix export; audit log; settings; announcements (simplest end-to-end feature) | Authz matrix test for all endpoints; cross-tenant tests; threat model v1 |
| **B2** | Ledger, billing, payments (≈6 wks) | Chart of accounts, journal, periods, balances; charge plans, allocation policies, charges, late-fee job; payments, allocation, vendors/bills; statements (CSV + PDF); reports; `Idempotency-Key` | Full-year golden fixture passes; replay test; property tests; PDF ADR |
| **B3** | Operations (≈5 wks) | Tickets (workflow, comments, attachments), documents/folders/versions, files/storage drivers, bookings (+ fee → charge), visitors/vehicles, notifications (in-app, email, preferences, digests, templates, overrides), outbox + dispatcher, statement import framework + matching rules | Email/notification golden tests; exclusion constraint tests; outbox at-least-once test |
| **B4** | Governance (≈4 wks) | Meetings, agenda, attendance/proxies, quorum via `LegalRules` (core default), votes/ballots (per unit/share/person), decisions, minutes & decision-book rendering via `DocumentTemplate` (core generic templates) | Vote math property tests; append-only ballot grant test |
| **B5** | Pack SDK v0 + mobile readiness (≈4 wks) | `pkg/sdk` public surface, registry, per-org enablement, pack migrations/routes/jobs/subscriptions, `testkit`; push relay client + `device` registration; the client-driven endpoints listed in §8 (discovery, `/v1/me`, dashboard, `updated_since`/ETags, image variants, web-login link, checkout shape) | A sample pack in `packs/example` passes `testkit`; every item in mobile plan §16 is in the spec |
| **B6** | TR pack + integrations (≈6 wks) | `packs/tr`: legal rules, late fee, templates, chart, consent texts, address format; iyzico `PaymentProvider`; Netgsm `NotificationChannel`; one bank `StatementImporter`; e-Devlet `IdentityProvider`; webhooks; API keys | TR pack tests green; SDK declared v1.0 (frozen) |
| **B7** | 1.0 hardening (≈4 wks) | Privacy export/erasure, retention pruning, rate limits & abuse protections, key rotation, perf pass (500-unit site), docs for admins/operators, `/v1` freeze, security review | k6 budget met; external security review findings closed |

Total ≈ 35 weeks of backend work for one full-time engineer; parallelizable across modules after B1 since module boundaries are explicit.

---

## 21. Risks and open questions

| Risk / question | Mitigation / decision needed |
|---|---|
| A real ledger feels heavy to managers who want "a dues list" | The ledger is invisible by default: the UI talks in charges/payments/statements; accountants and auditors get the journal views. Don't compromise the ledger to simplify the UI |
| RLS overhead and index misuse | `org_id` leads every index; `EXPLAIN` checks in integration tests for the hot queries; `account_balance` cache for reports |
| `oapi-codegen` vs `ogen` | Two-day spike at B0 on: strict typing, validation, union/oneOf handling, generated code size. ADR-0001 |
| Phone-only identity in core | Core supports phone as identifier and invitation-based onboarding; OTP arrives with the first SMS pack. Confirm this is acceptable for the first markets |
| PDF generation | Pure Go first; Gotenberg sidecar if templates demand HTML/CSS fidelity. ADR at B2 |
| Co-ownership and share math edge cases (fractional shares, merged units) | Model shares as integer numerator/denominator; property tests; "merged" unit status with successor links |
| Legal text accuracy in packs | Packs carry a `LEGAL.md` with sources and a disclaimer; rules are data+code reviewed by someone with domain knowledge before release |
| Scope creep from "ERP" expectations (payroll, corporate accounting) | Non-goals list in overview §1; propose as separate packs/projects |
| Single-process worker in small installs blocking HTTP | River runs in goroutines with bounded concurrency; CPU-heavy jobs (PDF) are capped at `max(1, NumCPU/2)` |
| Multi-currency requests from border regions | Explicit non-goal for 1.0; the `Money` type and per-site currency make a later change contained |
