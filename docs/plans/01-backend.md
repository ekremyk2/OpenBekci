# OpenBekci — Backend Plan

| | |
|---|---|
| Status | **Draft v0.2** — revised on 2026-11-06 against the [plan review](06-review.md) and [`docs/decisions/`](../decisions/) |
| Parent | [00-overview.md](00-overview.md) — decisions and shared contracts there bind this document. Product behaviour comes from the decision files (M = money, S = setup & people, O = operations, G = governance, C = community) |

---

## 1. Goals and non-goals

**Goals**

- One Go binary that serves the API, the embedded web app and (by default) the job worker, with PostgreSQL as the only required dependency.
- A modular monolith with module boundaries strong enough to extract later, never paying the cost of microservices.
- Correct money as decided in M1–M8: debtor-aware receivables, allocation keys, continuous late-fee accrual, opening balances, a lock date, corrections only by reversal.
- Organization isolation enforced by the database (RLS, composite foreign keys); site scoping enforced in the service layer; a site exportable and importable whole (D2).
- Privacy that actually works: erasure against append-only tables, PII-safe audit diffs, retention.
- Contract-first API: handlers generated from the OpenAPI spec; the spec validated in tests.

**Non-goals (through 1.0)**

- Multi-currency within a site; multi-region; sharding.
- Runtime-loaded or untrusted plugins.
- GraphQL, gRPC, event streams beyond signed webhooks (0.4).
- Accounting for the management company itself; in-app chat; approvals workflow; binding online votes.

---

## 2. Technology stack

| Concern | Choice | Notes |
|---|---|---|
| Language | Go, current stable; the two latest releases supported | |
| HTTP | `net/http` + `chi` v5 | Standard handlers, mature middleware |
| OpenAPI codegen | `oapi-codegen` strict-server mode; spike `ogen` in the skeleton phase | Spec authored in the **3.0.3-compatible subset**; the spike verifies nullable / oneOf / open-enum handling across Go, TS and Dart generators |
| Request validation | `kin-openapi` middleware (always in dev/test, configurable in prod) + domain validation | |
| Database | `pgx` v5 pool; `sqlc`; `goose` migrations | Two DB roles plus a BYPASSRLS platform role (§5) |
| Jobs and outbox | `riverqueue/river` | `InsertTx` in the business transaction **is** the outbox; no `outbox` table |
| Auth | `golang.org/x/crypto/argon2`; opaque tokens (`crypto/rand`, SHA-256 hashes) | **No JWT.** Passkeys/TOTP/OIDC libraries added when those features are scheduled (later) |
| SMS | core `sms.Gateway` interface + Netgsm driver (D5) | Other providers via packs (0.4) |
| Email | `wneessen/go-mail` + `html/template` | |
| Storage | own `storage.Blob`: local FS (default), S3 driver via `aws-sdk-go-v2` | **Uploads always go through the server** (streamed to S3 when configured); no presigned URLs in v1 |
| PDF | pure Go (`go-pdf/fpdf` or `maroto` v2) with an **embedded TTF** (Noto Sans) covering `ğ ş İ ı` | Decided; no Gotenberg sidecar |
| Spreadsheets | `excelize` (XLSX) + `encoding/csv` | Imports and exports |
| Logging / tracing | `log/slog` JSON; OpenTelemetry (OTLP optional); Prometheus `/metrics` | |
| Config | env `OPENBEKCI_*` (+ `_FILE` variants), optional YAML | |
| CLI | `cobra` | |
| i18n (server) | `nicksnyder/go-i18n` v2 | Emails, SMS, PDFs, notification texts only |
| Testing | stdlib, `go-cmp`, `testcontainers-go`, `rapid` | Real Postgres in integration tests |
| Lint / security | `golangci-lint`, `govulncheck`, `gosec` | |
| Release | GoReleaser; distroless; cosign; SBOM | |

Deliberately not used: ORMs, decimal libraries for money, Redis/brokers, DI frameworks, JWT, a separate outbox, presigned uploads, a Postgres-backed rate limiter (in-memory per instance suffices until hosted multi-replica).

---

## 3. Repository layout (`server/`, `sdk/`)

```
sdk/                          PUBLIC pack SDK — separate Go module, Apache-2.0 (0.4)
server/
├─ cmd/openbekci/
│  ├─ main.go                 subcommands
│  ├─ serve.go · worker.go · migrate.go · admin.go
│  └─ packs.go                var Official = []sdk.Pack{…} (0.4); `-tags nopacks` leaves it empty
├─ internal/
│  ├─ app/                    config, wiring (hand-written), lifecycle, startup checks
│  ├─ httpapi/
│  │  ├─ gen/                 oapi-codegen output — never edited by hand
│  │  ├─ server.go            StrictServerInterface composed from module handlers
│  │  ├─ middleware/          request id, logging, session, org context, rate limit, idempotency, problem+json
│  │  └─ problem/             error → RFC 9457 mapping, code enum
│  ├─ platform/               infrastructure adapters, no business rules
│  │  ├─ db/                  pool(s), tenant tx, platform tx, sqlc output, RLS helpers
│  │  ├─ storage/  mail/  sms/  jobs/  otel/  clock/  id/  i18n/  crypto/  pdf/  sequence/
│  ├─ core/                   one package per module (§4); each: domain.go, service.go, repo.go, repo_pg.go, http.go, jobs.go, permissions.go
│  │  ├─ tenancy/     organizations, sites, blocks, units, allocation keys, site export/import
│  │  ├─ identity/    users, sessions, credentials, OTP, invitations, unit claims
│  │  ├─ people/      persons, occupancies, memberships, vendors, directory, move-out
│  │  ├─ ledger/      accounts, funds, journal, lock date, fiscal years, opening balances
│  │  ├─ billing/     dues plans and rates, allocation, charges, late fees, budgets
│  │  ├─ payments/    payments, receipts, cash boxes, allocation, bank import and matching, refunds, transfers
│  │  ├─ expenses/    light expenses, vendor bills, recurring expenses
│  │  ├─ collections/ dunning stages, collection cases, ihtarname export
│  │  ├─ tickets/  announcements/  documents/  notifications/  audit/  settings/  privacy/
│  │  ├─ governance/  (0.2) meetings, notices, attendance, polls, decisions, effects
│  │  ├─ community/   (0.3) facilities, bookings, visitors, deliveries; access/ (0.3) agent protocol
│  │  ├─ authz/       permissions, roles, occupancy-derived rules, Check()
│  │  └─ platformadmin/ instance endpoints (uses the BYPASSRLS pool)
│  ├─ packs/                  pack runtime (0.4): registry, per-org enablement, migration runner
│  ├─ sdktest/                AGPL test harness for packs (boots the core); kept out of `sdk/`
│  └─ webassets/              go:embed of ../../web/dist; `-tags nowebassets` serves a placeholder
├─ db/
│  ├─ migrations/             goose SQL
│  ├─ queries/<module>/*.sql  sqlc inputs
│  └─ sqlc.yaml
├─ locales/                   en.json, tr.json
└─ testdata/                  golden site fixture (§18), golden files
```

Module rules (unchanged from v0.1): a module's `service.go` is its only entry point; cross-module needs are declared as narrow interfaces by the consumer and satisfied in wiring; side effects between modules go through River jobs unless they must be atomic with the caller; `internal/core/*` never imports `httpapi` or `packs`.

---

## 4. Domain model

All tenant tables carry `id uuid (v7)`, `org_id`, `created_at`, `updated_at`, `version int` unless marked append-only. Tables also scoped to a site carry `site_id`. Release tags mark what is built when.

### 4.1 tenancy (0.1)

| Entity | Notes |
|---|---|
| `organization` | Access boundary (D2). `slug`, `name`, `default_locale`, `timezone`, `country_code`, `status`, `settings jsonb` |
| `site` | **Data-owning unit.** `name`, `address jsonb`, `timezone`, `currency`, `fiscal_year_start_month` (default 1), `lock_date` (§9), `settings jsonb` (tenants-are-portal-users, directory enabled, transparency to owners, legal presets …), `bank_details jsonb` (IBAN, holder — shown to payers) |
| `block` | Building within a site; auto-created and hidden for single buildings (S1) |
| `unit` | `block_id`, `door_number` ("A-12"), `legal_ref` (tapu bağımsız bölüm no), `type` (flat / shop / office / parking / storage / common / other), `area_m2`, `parent_unit_id` (appurtenance, S2), `status` (active / merged / split). Vacancy is derived |
| `allocation_key` | Per site: `code` (`arsa_payi`, `equal`, `area`, custom), `name`, `kind` (share / equal / area / table) (M2) |
| `allocation_key_value` | `(key_id, unit_id) → numeric` weight; `equal` and `area` are computed, `share`/`table` stored |
| `unit_tag` | Cost-group membership (`lift`, `pool`) for scoped allocations |
| `facility` | (0.3) bookable thing with `rules jsonb` (C1) |
| `site_export` | Record of an export/import (D2): `direction`, `archive_file_id`, `source_org`, `target_org`, `at` |

### 4.2 identity (0.1)

| Entity | Notes |
|---|---|
| `user` | Instance-wide. `email?`, `phone?` (E.164; at least one), `password_hash?`, `locale`, `status`, `is_platform_admin` |
| `session` | `user_id`, `kind` (web_cookie / access_token / refresh_token / api_key (0.4)), `token_hash`, `expires_at`, `revoked_at`, `last_used_at`, `ip`, `user_agent`, `rotated_from`. **Opaque tokens for every kind** |
| `otp_code` | `user_id?`/`phone`, `purpose` (sign_in / verify / visitor_approval), `code_hash`, `expires_at`, `attempts` |
| `invitation` | `kind` (manager / site_code / household), `org_id`, `site_id`, `person_id?`, `unit_id?` (household), `channel` (sms / email), `token_hash`, `expires_at`, `accepted_at` (S4) |
| `unit_claim` | From the site QR path: `user_id`, `unit_id`, `claimed_role` (owner / tenant / household), `status` (pending / approved / rejected), `decided_by` (S4) |
| `login_attempt` | Append-only, pruned; rate limiting and lockout |

Passkeys, TOTP and external identities are **later**; the tables are not created in 0.1.

### 4.3 people (0.1)

| Entity | Notes |
|---|---|
| `person` | Org-scoped; `user_id?`; `display_name`, `contact jsonb`, `national_id` (encrypted, with `country_code`; TCKN checksum validator; masked in reads; never in audit diffs) (S6), `gate_contact_phone?` (C2), `directory_consent` per site (S8), `notes` |
| `occupancy` | `person_id`, `unit_id`, `kind` (**owner / tenant / household**), `share_pct` (co-owners), `is_correspondent` (one per unit side), `may_see_money` (household, default false), `valid_from`, `valid_to` (S3). **Portal rights derive from this table only** |
| `membership` | `person_id`, `role` (org_admin / site_manager / board_member / auditor / accountant / staff / guard), `scope_type` (org / site / block), `scope_id`, `valid_from`, `valid_to`, `decision_id?` (G6), `site_owned` (board/auditor: true — cannot be removed by org admins without a decision, D2) |
| `move_out` | `occupancy_id`, `as_of`, `closing_statement_file_id`, `certificate_file_id`, `debt_disposition` (stays_with_person / transferred / written_off), `decided_by` (S7) |
| `vendor` | Suppliers and labels for ticket assignment; `name`, `tax_id`, `contact jsonb`, `default_expense_category_id` |

### 4.4 ledger (0.1)

| Entity | Notes |
|---|---|
| `account` | Per site. `code`, `name`, `type` (asset / liability / equity / income / expense), `subtype` (bank, cash_box, receivable_owner, receivable_tenant, person_receivable, prepayment_liability, deposit_liability, dues_contribution, late_fee_income, other_income, expense_category, opening_equity, prior_year_surplus, provision_severance, withholding_receivable …), `fund_id?`, `is_system`. Default chart created per site; packs localize names |
| `fund` | Per site: `operating` (default), `reserve` (demirbaş / yedek akçe), custom (M defaults) |
| `cash_box` | An asset account bound to a collector person (M5) |
| `journal_entry` | **Append-only.** `site_id`, `entry_date`, `document_date`, `fiscal_year_id`, `memo`, `source_type`, `source_id`, `posted_by`, `posted_at`, `reverses_entry_id`. Rejected if `entry_date ≤ site.lock_date` |
| `journal_line` | **Append-only.** `entry_id`, `account_id`, `fund_id`, `debit`, `credit` (exactly one non-zero), `unit_id?`, `debtor_kind?` (owner / tenant), `person_id?`, `block_id?`, `vendor_id?` |
| `fiscal_year` | `site_id`, `start`, `end`, `status` (open / closed / approved), `approved_by_decision_id?` |
| `opening_balance` | `site_id`, `as_of`, `kind` (unit_receivable / bank / cash / vendor), target ids, `principal`, `late_fee`, `journal_entry_id`; locked once `as_of ≤ lock_date` (M7) |

Invariants: Σ debit = Σ credit per entry (service + deferred constraint trigger); no `UPDATE`/`DELETE` grant on `journal_*` for the app role; `fund_id` on every line; no `account_balance` cache (SUM over an index led by `(org_id, site_id, account_id, entry_date)`).

### 4.5 billing (0.1)

| Entity | Notes |
|---|---|
| `budget` | Light, optional (M3): `site_id`, `fiscal_year_id`, `status` (draft / final), `decision_id?` |
| `budget_line` | `budget_id`, `expense_category_account_id`, `fund_id`, `amount` |
| `charge_plan` | `site_id`, `name`, `kind` (recurring / one_off), `charge_type` (dues / special_assessment / reserve_contribution / utility / booking_fee / legal_cost / deposit / adjustment / settlement), `default_debtor` (owner / tenant) (M1), `frequency`, `due_day`, `start`, `end`, `amount_mode` (per_unit / total_allocated), `allocation_key_id`, `scope` (site / block id / unit_tag), `fund_id`, `income_account_id`, `status` |
| `charge_plan_rate` | Effective-dated amount: `plan_id`, `from`, `to?`, `amount`, `decision_id?` (M3, G5) |
| `charge` | `(plan_id, unit_id, period)` unique; `unit_id`, `debtor_kind`, `debtor_person_id` (snapshot of the occupancy in force on the due date), `charge_type`, `amount`, `due_date`, `description`, `allocation_basis jsonb` (key, weight, total — snapshot), `journal_entry_id`, `status` (open / partially_paid / paid / cancelled) — status is derived and rebuildable |
| `expense_allocation` | A cost shared to units → charges, with the same key/scope/basis fields |
| `late_fee_policy` | Site settings: `rate_pct_per_month` (default 5, max = statutory via preset), `grace_days`, `simple` |
| `late_fee_accrual` | Computed, not stored: fee to date per `(unit, debtor_kind)` from open principal and dates |
| `late_fee_booking` | Monthly crystallization: `(unit_id, debtor_kind, period)` unique, `amount`, `charge_id`; the job reconciles expected-to-date minus booked (M4) |
| `late_fee_waiver` | `unit_id`, `debtor_kind`, `amount`, `reason`, `by`, `journal_entry_id` (reversal) |
| `late_fee_freeze` | `unit_id`, `debtor_kind`, `from`, `to?`, `reason` (in legal collection) |

### 4.6 payments (0.1)

| Entity | Notes |
|---|---|
| `payment` | `site_id`, `payer_person_id?`, `method` (cash / bank_transfer / card:provider (0.4)), `gross`, `fee` (0 until providers), `net`, `received_at`, `recorded_by`, `asset_account_id` (bank or cash box), `reference`, `status` (pending_declared / confirmed / refunded), `receipt_no` (gap-free per site per year, M5), `provider_txn_id?` (unique per org+provider), `journal_entry_id` |
| `payment_allocation` | `payment_id`, `charge_id`, `amount`; order policy (M default: oldest first, principal before fee; manual override) |
| `credit` | Unallocated money per `(unit, debtor_kind)` or person, as a prepayment liability line |
| `transfer` | Asset → asset (cash handover, bank deposit) |
| `refund` | Outgoing to a person/unit (overpayment, deposit return) |
| `bank_statement_import` | `importer` (core_csv / pack id), `file_id`, `opening_balance?`, `closing_balance?`, `status` |
| `bank_transaction` | `import_id`, `booked_at`, `amount`, `counterparty`, `description`, `raw jsonb` (stripped after reconciliation, §16), `match_status`, `payment_id?` |
| `matching_rule` | (later) description pattern → unit |
| `sequence` | Per site per year per kind (receipt, decision …) with row locking |

### 4.7 expenses (0.1)

`expense` (light: `site_id`, `category_account_id`, `fund_id`, `amount`, `paid_at`, `paid_from_account_id`, `vendor_id?`, `document_no?`, `file_id?`, `recurrence?` monthly) and, optional per site, `bill` + `bill_payment` (vendor invoice with due date and partial payments). No approvals (O/M6).

### 4.8 collections (0.1)

`dunning_stage` (site-defined: reminder 1 at N days, reminder 2, ihtarname), `dunning_event` (unit side, stage, sent_at, channel, document file), `collection_case` (`unit_id`, `debtor_person_id`, `opened_at`, `lawyer`, `file_no`, `status`, `costs_charged`, freezes late fees and reminders while open), `debt_breakdown` export (principal and fee per period as of a date).

### 4.9 tickets (0.1)

`ticket_workflow` (per site: ordered statuses with `name`, `semantic` open_like / closed_like; a default workflow until edited) (O1), `ticket_category` (default visibility, default assignee role), `ticket` (`unit_id?`, `reporter_person_id`, `category_id`, `status` (workflow status id), `visibility` private / common (O3), `assignee` person or vendor, `merged_into?`), `ticket_comment` (`visibility` public / internal), `ticket_attachment`, `ticket_vote` (person, ticket — one each), `ticket_status_history` (append-only).

### 4.10 announcements (0.1)

`announcement` (`scope`, `audience`, `title`, `body_md`, `publish_at`, `expires_at`, `pinned`, `channels[]` chosen by the sender, `recipient_count`, `sms_count`) (O4/O5), `announcement_read` (person, read_at).

### 4.11 documents (0.1)

`folder` (site tree, plus one private folder per unit created with the unit), `document` (`folder_id`, `title`, `file_id`, `visibility`, `source` uploaded / generated with `generated_from` type+id) (O8). No versions.

### 4.12 notifications (0.1)

`notification` (user, type, payload, read_at, link), `notification_delivery` (channel in_app / email / sms / push (0.4), status, attempts), `site_channel_default` (automatic message type → channels, O5 table), `notification_mute` (user, type; sign-in and invitations unmutable), `device` (0.4: `push_kind`, `token` encrypted, `relay_id`, `app_version`, `locale`), `template_override` (later).

### 4.13 audit and privacy (0.1)

`audit_event` (**append-only**: `actor`, `action`, `target`, `diff jsonb` where PII fields appear as `{field: {hash_before, hash_after}}`, `ip`, `request_id`, `at`), `consent` (person, text id, version, accepted_at), `data_export_request`, `erasure_request`, `retention_setting` (visitor logs, bank raw payloads, sessions, login attempts).

### 4.14 governance (0.2)

`meeting` (`site_id`, `kind` general_assembly / extraordinary, `scope` site (block/council reserved), `scheduled_at`, `second_call_at`, `status`, `eligibility_snapshot jsonb`, `minutes_document_id?`), `meeting_notice` (person, method registered_mail / signed / email / sms, sent_at, proof file) (G3), `agenda_item`, `poll` + `poll_answer` (non-binding, one per unit) (G2), `attendance` (person, units, mode, proxy_for, checked_in_at, checked_in_by, qr token) (G4), `vote` (in-room counts per item), `decision` (`sequence_no`, text, outcome, `dissents[]`, `notified_at`, `effect` kind + created record id) (G5), `term_reminder` job (G6). Quorum/threshold/weight tables are `site.settings` with presets.

### 4.15 community and access (0.3)

`booking` (facility, unit, person, range, status, `charge_id?`; exclusion constraint) (C1); `visitor_entry` (`unit_id`, name, plate, window, `qr_token`, `recurring_rule?`, `approval_status` pre_approved / requested / approved / denied / expired, `decided_by`, arrived/left) (C2); `visitor_approval_request` (notification with expiry); `delivery` (unit, received, collected) (C3); `vehicle` (0.1: plates per unit with export); `access_point`, `credential`, `access_event` and the agent protocol — specified in the 0.3 design, tables reserved.

---

## 5. Multi-tenancy and data isolation

- Single database, shared schema; every tenant table has `org_id` as the **leading column of every index** the app queries through.
- **RLS on every tenant table**, generated by a migration helper so the policy text is identical everywhere:
  ```sql
  ALTER TABLE unit ENABLE ROW LEVEL SECURITY;
  ALTER TABLE unit FORCE ROW LEVEL SECURITY;
  CREATE POLICY unit_tenant ON unit
    USING      (org_id = (SELECT NULLIF(current_setting('app.org_id', true), '')::uuid))
    WITH CHECK (org_id = (SELECT NULLIF(current_setting('app.org_id', true), '')::uuid));
  ```
  `NULLIF(…, '')` because a custom GUC that was once set returns `''` on that session afterwards; the `(SELECT …)` wrapper makes it an InitPlan evaluated once per query.
- Tenant context is set with `SELECT set_config('app.org_id', $1, true)` inside the transaction (`SET LOCAL` cannot take bind parameters). `db.InTenantTx(ctx, orgID, fn)` is the only way to a tenant connection; `platform/db` does not export the raw pool to `core`.
- **Three database roles:** `openbekci_migrator` (owner, **BYPASSRLS** so backfill migrations see all rows), `openbekci_app` (RLS-subject), `openbekci_platform` (**BYPASSRLS**, used by a second small pool that only `platformadmin` and the cross-org parts of `GET /v1/me` may use via `db.InPlatformTx`; enforced by `depguard`).
- **Composite foreign keys** `(org_id, child_id) REFERENCES parent(org_id, id)` on every tenant-to-tenant reference, so the database refuses cross-organization links at write time (FK checks bypass RLS).
- **Site scoping is application-level**: `authz` restricts a `site_manager` of site A from site B; RLS does not.
- Jobs iterate organizations and run each unit of work in its own tenant transaction.
- **Poolers**: `SET LOCAL`/`set_config(…, true)` inside an explicit transaction is safe under PgBouncer transaction mode. What is not: River's `LISTEN/NOTIFY` (run with `PollOnly` behind a transaction pooler) and pgx's prepared-statement cache (PgBouncer ≥ 1.21 with `max_prepared_statements`, or describe/simple exec mode). A startup check detects a pooler and configures both.
- **Site export/import (D2):** `admin export-site` / `import-site` and the equivalent API: all site-scoped rows plus the persons, occupancies and memberships that touch the site, documents and files, with ids preserved; persons already present in the target org (same user) are linked, not duplicated; the source org's access is revoked on offboarding.
- CI test: two orgs, rows in each; every table in `information_schema` has an RLS policy or is on the explicit instance-wide allowlist (`user`, `session`, `otp_code`, `login_attempt`, river tables); cross-org FK insert fails.

---

## 6. Authentication

| Method | Release | Notes |
|---|---|---|
| Email + password | 0.1 | argon2id (`t=3, m=64MiB, p=4`), minimum 10 chars, optional HIBP k-anonymity check |
| Magic link (email) | 0.1 | 15-minute single-use |
| **SMS one-time code** | 0.1 | 6 digits, 5 minutes, 5 attempts; via the core `sms.Gateway` (Netgsm) (D5) |
| Invitation | 0.1 | kinds per S4; accepting links a `user` to the `person` |
| Site QR + unit claim | 0.1 | `POST /v1/join/{code}:sign-up` (phone or email, optional name; public, 202 whether or not the contact has an account; limited per code, contact and address) sends the SMS code or link; verifying it (`auth.createToken` / `createSession`) activates the pending account and opens a session with no organization; then `POST /v1/join/{code}/claims` with the same code; the manager approves; `GET /v1/me/unit-claims` lists the caller's claims with status and labels |
| Passkeys, TOTP, OIDC | later | |

Sessions (overview §6.3): one `session` table, **opaque tokens everywhere**.

- **Web:** `POST /v1/auth/session` sets an httpOnly, Secure, `SameSite=Lax` cookie with the token; the hash is stored. Mutations require `Origin` consistent with `BASE_URL` (plus `EXTRA_ORIGINS`) and `X-Requested-With: openbekci-web`.
- **Mobile / integrations:** `POST /v1/auth/token` returns an access token (15 min, opaque, hash stored, checked on every request — instant revocation) and a rotating refresh token (60 days; reuse detection revokes the family). `DELETE /v1/me/sessions/{id|current}`.
- **Re-authentication:** sensitive operations (§16) answer `403 reauth_required`; `POST /v1/auth/reauth` with password or OTP marks the session for 10 minutes.
- `GET /v1/me` lists the user's organizations, roles, effective permissions, occupancies (units), policies; served through the platform pool for the cross-org part, then per-org.
- Link URL formats are defined here and reused by both clients: magic link `{BASE_URL}/login/magic?token=…`, invitation `{BASE_URL}/invite/{token}`, reset `{BASE_URL}/reset/{token}`, site QR join `{BASE_URL}/join/{code}` (the QR encodes this URL), web-login link `{BASE_URL}/login/link/{token}` (0.4); requests carry `client=web|mobile` so the server can emit the mobile form instead. **The mobile form names the instance**: `openbekci://<host>/<same path and query>`, where `<host>` is the host (with port, if any) of `BASE_URL` (`openbekci://bekci.example.org/invite/{token}`, `openbekci://bekci.example.org/login/magic?token=…`, `openbekci://bekci.example.org/join/{code}`). The app takes the server from the host and assumes `https`; a development instance served over plain `http` adds `scheme=http` to the query. Tokens and join codes are `<org>.<secret>` and contain dots, so the host is always the first segment and the token is the whole last path segment (or the whole `token` value), never split on a dot. A link that is only available in the web form (a manager-created invitation is sent as `{BASE_URL}/invite/{token}`) hands off to the app as `openbekci://invite/{token}?server=https%3A%2F%2F<host>`; the app accepts this `server=` form as a fallback next to the host form.

---

## 7. Authorization

- **Permissions** are string constants per module; the full list with the role → permission matrix is generated into the spec (`components.schemas.Permission`, `x-openbekci-roles`).
- **Two sources of rights, no overlap (S3):**
  - **Occupancies** give portal rights: owner → whole unit (both sides) and site-level owner documents/decisions; tenant → tenant side of the unit; household → tickets, announcements, bookings, visitors, and money only if `may_see_money`. Former occupants keep read access bounded by their occupancy dates (S7).
  - **Memberships** give role rights: `org_admin`, `site_manager`, `board_member`, `auditor` (read-everything + `audit.report`), `accountant` (money only), `staff`, `guard` (data-minimized endpoints only), scoped to org / site / block, time-bounded.
- Board and auditor memberships are site-owned (D2): removable only through a decision (0.2) or an audited override with re-auth.
- Site setting "financial transparency to owners": aggregate gelir-gider and budget always visible to owners; the debtor list is board-only by default and never public.
- Implementation: middleware resolves memberships and occupancies for the org once per request into `authz.Principal`; services call `authz.Require(ctx, perm, scope)` / `authz.CanAccessUnit(ctx, unitID, side)`. Checks live in the service layer.
- **Authz matrix test**: every operation × every role × own/other resource → expected status. The harness is a scheduled deliverable (≈ 1.5 weeks) in the skeleton phase.

---

## 8. API design

Conventions from overview §6.2. Backend specifics:

- **Paths:** `/v1/orgs/{org_id}/<resource>[/{id}]`; `?site_id=` for site scoping; `/v1/auth/…`, `/v1/me/…`, `/v1/platform/…`; files at `/v1/orgs/{org_id}/files…`; packs (0.4) at `/v1/orgs/{org_id}/packs/{pack_id}/…` for **webhooks/callbacks only**.
- **Operations** named `<module>.<verb><Noun>`.
- **Spec extensions** (the single list): `x-openbekci-roles` (role→permission matrix), `x-idempotent` (operation accepts/requires `Idempotency-Key`), `x-ui` (uiSchema hints for schema-driven settings), `x-secret` (write-only setting field), `x-stability` (`stable` / `preview` — clients may depend only on `stable`).
- **Problem codes** (enum in the spec): `validation_failed`, `bad_request`, `unauthenticated`, `not_found`, `conflict`, `version_conflict`, `idempotency_mismatch`, `locked_period`, `insufficient_permission`, `reauth_required`, `mfa_required` (reserved), `invalid_grant`, `rate_limited`, `pack_error` (0.4), `site_offboarded`, `internal_error`, `payload_too_large` (413), `method_not_allowed` (405), `unsupported_media_type` (415), `timeout` (504).
- **Idempotency:** every `POST` create honours `Idempotency-Key`; required for payments, charges, expenses, refunds, transfers, opening balances, bookings. Store `(org_id, principal_id, key)` with an in-progress row (insert first; unique violation → `409` while in flight), 7-day retention.
- **Discovery document** (normative; overview §6.8), unauthenticated `GET /.well-known/openbekci.json`:
  ```json
  {
    "instance_id": "uuid, stable for the life of the instance",
    "instance_name": "…", "api_base_url": "https://…/v1", "api_versions": ["v1"],
    "server_version": "0.1.0", "sdk_version": null,
    "min_app_version": { "android": "1.0.0", "ios": "1.0.0" },
    "auth_methods": [ { "id": "password" }, { "id": "sms_otp" }, { "id": "magic_link" },
                      { "id": "oidc:google", "label_key": "auth.google", "icon": "google" } ],
    "push": { "relay_ids": ["official"], "unified_push": false },
    "packs": ["tr"],
    "branding": { "logo_url": "…", "primary_color": "#…" },
    "relay_enrollment": { "challenge": "…ephemeral…" }
  }
  ```
  `GET /v1/instance` is the authenticated twin with pack details and policies.
- **Client-driven endpoints and their releases:** discovery + `/v1/instance` + `/v1/me` (0.1 skeleton); `GET /v1/orgs/{org}/me/dashboard` and the org-level portfolio dashboard (0.1); `POST …/units:import?dry_run=true` and opening-balance import (0.1); `POST …/charge-plans:preview` (0.1); `GET …/codes?locale=` (enum/error labels incl. pack labels, 0.1); platform endpoints `/v1/platform/orgs|settings|health|jobs|backup-status` (0.1 minimal, 1.0 complete); image `?variant=thumb|medium` (0.1, generated on upload); `POST/DELETE /v1/me/devices` (0.4); web-login link (0.4); checkout `checkout_url`/`return_url` + status poll (0.4); `GET /v1/orgs/{org}/reference/{dataset}` for pack-registered reference data such as il/ilçe lists (0.4). **No `updated_since` / ETag** in v1.
- **Bulk:** CSV/XLSX import with `dry_run` returning a row-level report; exports as CSV/XLSX via jobs.
- **Rate limiting:** in-memory token buckets per instance (per IP on auth/OTP/magic-link/invitation sending; per user elsewhere). `429` with `Retry-After`.
- **Webhooks / API keys (0.4):** `/v1/orgs/{org_id}/api-keys` (create with the one-time secret, list, `:rotate`, `:revoke`; bearer `obk_<prefix>_<secret>`, rights = key scopes ∩ the creator's grants at call time, re-authentication to manage) and `/v1/orgs/{org_id}/webhook-endpoints` (CRUD, `:test`, `/deliveries`). `docs/integrations.md`, `docs/adr/0002-webhook-signing.md`.
- **Realtime:** polling (unread count endpoint, visitor approval requests). SSE later.

---

## 9. Money rules (from M1–M8)

1. **Representation:** `Money{Amount int64, Currency}`; exponent from the currency table; no mixing.
2. **Double entry always**; every charge, payment, expense, fee, transfer, refund, opening balance posts a balanced entry with a `fund_id` on each line.
3. **Immutability:** entries are never updated or deleted; corrections are reversals dated **today** with `reverses_entry_id`; `document_date` keeps the original date.
4. **Lock date** per site instead of period state: postings with `entry_date ≤ lock_date` are rejected (`locked_period`); moving the lock date back requires re-auth and is audited. Fiscal years are reporting groupings with an approval status.
5. **Debtor sides:** receivables are keyed by `(unit, debtor_kind)`; each charge snapshots its debtor person; a person also has a personal receivable for debt carried after move-out (S7). Statements exist per unit side and per person.
6. **Allocation:** a plan or expense allocation names an allocation key and a scope; the remainder is distributed by largest-remainder in deterministic order so Σ = total; the basis is snapshotted on each charge.
7. **Charge generation** is an hourly, idempotent, catch-up job on `(plan, unit, period)`; effective-dated rates decide the amount; a decision id can be attached (G5).
8. **Late fees:** accrual computed on demand from open principal (fees never compound); monthly crystallization by a reconciling job (`expected − booked = delta`, may post a reversal after a backdated payment); waivers and freezes as first-class operations; policy parameters are site settings with national presets.
9. **Payment allocation:** default oldest-first, principal before fee; manual override always; over-payment becomes a prepayment liability (credit) on the unit side or the person.
10. **Receipts and decisions** take gap-free per-site per-year numbers from `sequence` with row locks.
11. **Opening balances** post against `opening_equity`; locked once the lock date passes them.
12. **Reports (0.1):** cash-basis gelir-gider, debtor list, statements (unit side / person) as PDF with running balance and principal/fee split, budget vs actual, trial balance, receivables aging, collector reconciliation, bank reconciliation (statement opening/closing vs ledger), fund balances.
13. **Invariants tested daily on the golden site:** per unit side, Σ open charges − credit == receivable line balance; Σ `payment_allocation` per payment ≤ net; fund totals reconcile; replay rebuilds `charge.status`.

---

## 10. Pack / extension system (0.4)

Unchanged in intent from v0.1 (see [04-packs.md](04-packs.md)), with these corrections:

- `sdk/` is a **top-level Go module** (Apache-2.0); the test harness that boots the core lives in `server/internal/sdktest` (AGPL) and drives packs through the registry.
- Official packs are listed in an explicit `var Official = []sdk.Pack{…}` — no `init()` registration.
- Pack `Routes` are **webhook/callback-only** (`Public()` registration sets tenant context from the path and skips session auth; provider signatures are verified by the pack); anything clients call goes through core endpoints.
- Event payloads are `{type, version, ids}`; handlers fetch details through readers.
- `LegalRules` is split: `QuorumPreset`, `ThresholdPreset`, `VoteWeightPreset`, `SchedulePreset`, `TransferRule`, `LateFeePreset` — all **presets that fill site settings**, so a site without a pack can still configure them.
- Additional registrations: `Validator`, `ReferenceData`, `PaymentAllocationOrder`; `InvoiceIssuer` reserved post-1.0.
- `PaymentProvider` is defined around gross / fee / net settlement, refunds and idempotent webhooks, with a merchant-of-record setting (M5).

Until 0.4, everything Turkish is **core**: `tr` locale, Law 634 presets as default site settings when `country_code = TR`, TCKN validator, Turkish document templates in the core template set.

---

## 11. Jobs (River is the outbox)

- Side effects are River jobs inserted with `InsertTx` in the business transaction, **one job per (event, subscriber)** with River's unique-job option for dedupe. No `outbox`/`event_delivery` tables; `audit_event` is the history.
- All periodic jobs are **catch-up jobs**: they compute everything missing up to now and are idempotent on natural keys.

| Job | Schedule | Module |
|---|---|---|
| `billing.generate_charges` | hourly; all missing `(plan, unit, period)` up to today | billing |
| `billing.crystallize_late_fees` | hourly; reconciles bookings for the last closed month and corrects after backdated payments | billing |
| `collections.run_stages` | hourly; sends reminders due per site stage settings in site time | collections |
| `payments.import_statement` | on upload | payments |
| `notifications.deliver` / `notifications.sms` | on demand, retries with backoff | notifications |
| `documents.render` | on demand (PDF) | documents |
| `people.move_out_documents` | on demand | people |
| `tenancy.export_site` / `import_site` | on demand | tenancy |
| `privacy.export` / `privacy.erase` / `maintenance.prune` (retention) | on demand / daily | privacy / platform |
| `identity.sweep_pending_signups` | hourly; deletes `pending` accounts (join-code sign-ups) older than 24 h that never proved their contact, with their codes and links; fixed, no configuration | identity |
| `governance.term_reminders`, `governance.notify_absentees` (0.2) | daily / on demand | governance |
| `community.visitor_request_expiry`, `community.booking_fees` (0.3) | on demand | community |
| `webhooks.deliver` (0.4) | one job per (event, endpoint), unique on both; exponential backoff, bounded attempts | integrations |

`openbekci serve` runs the worker in-process by default; `openbekci worker` runs it alone. CPU-heavy jobs (PDF, imports) are capped at `max(1, NumCPU/2)`.

---

## 12. Notifications and SMS

- Channels in 0.1: `in_app`, `email`, `sms`. `push` in 0.4 via the relay (infra plan §4) with a content-free payload `{instance_id, org_id, notification_id, type, collapse_key, badge}`; UnifiedPush endpoints are posted to directly.
- **Authored messages** (announcements, manual reminders): the sender picks channels; the API returns `recipient_count` and `sms_count` for preview before publish (O5).
- **Automatic messages** use `site_channel_default` with the O5 defaults; sign-in codes and invitations always go by SMS when a phone exists and by email otherwise (never both), and cannot be muted; statements, payment confirmations and rejections go in-app plus email, dues reminders and receipts in-app plus SMS (the site pays), SMS stays off for everything else unless the site turns it on; residents can mute non-essential types.
- `sms.Gateway` interface in `platform/sms` with a Netgsm driver (D5): send, delivery status callback, sender id, per-site credentials, per-site monthly counter visible to the manager. Packs add providers in 0.4 through the same interface.
- **Shipped in 0.1 versus deferred:** the gateway is instance-wide (`SMS_PROVIDER`, `SMS_NETGSM_*`); **per-site SMS credentials and the delivery status callback are deferred**, so `Delivered` in the SMS counter equals the messages the gateway accepted, and failures are the sends it refused. The counter month is the site's calendar month in its time zone. Dispatch is at-least-once: a delivery row is claimed atomically before sending (two workers never send the same row), but a crash between the send and the record can repeat one message. Notification keys (`<type>:<ref>`) are unique per person and type.
- Templates per type and locale in `locales/`; per-org overrides later.
- Notification `type` vocabulary is enumerated in the spec (`announcement.published`, `charge.due_soon`, `charge.overdue`, `payment.receipt`, `ticket.updated`, `visitor.approval_requested` (0.3), `meeting.called` (0.2) …) and shared by push titles and Android channels.

---

## 13. Files and storage

- `storage.Blob` with local FS (default) and S3 drivers; **all uploads stream through the server** (`POST /v1/orgs/{org}/files` multipart, size and mime checks by sniffing, EXIF location stripped, thumbnails and `medium` variants generated for images). Downloads via `GET /v1/orgs/{org}/files/{id}` with the attached resource's visibility check, streamed or redirected to a short-lived signed S3 URL.
- `file` table: `org_id`, `site_id?`, `key`, `size`, `mime`, `sha256`, `uploaded_by`, `scope`, `status` (ready / quarantined), `retention_until?`. Optional ClamAV hook.
- Generated documents (statements, receipts, certificates, minutes) are files with `document.source = generated` filed into the right folder with the right visibility (O8).

---

## 14. Internationalization

As overview §6.6. Server catalogs for emails, SMS, PDFs and notification bodies; `golang.org/x/text` for numbers and currencies; site timezone for all site-scoped dates; PDFs with an embedded TTF.

---

## 15. Observability

`slog` JSON (never emails or phone numbers), OpenTelemetry (HTTP, pgx, River, outbound), Prometheus `/metrics` behind a token, `/healthz`, `/readyz` (DB, migrations, storage, SMS gateway configured), `/debug/pprof` behind `DEBUG`. Metrics include job lag, SMS sent per site, delivery failures by channel.

---

## 16. Security and privacy

- Password hashing, lockout, session rules as §6. Security headers; strict nonce CSP for the embedded SPA (the Go server renders `index.html` with the nonce; Vite's `html.cspNonce`); CORS only for `EXTRA_ORIGINS`.
- All SQL via sqlc; boundary validation by the generated layer and domain rules.
- Field encryption (AES-256-GCM, key id stored with ciphertext, `admin rotate-keys`) for national ids, OTP secrets, provider credentials, device tokens; pack tables (0.4) use the same helper.
- **Re-authentication** for: changing email/phone/password, moving the lock date back, opening-balance edits, waivers above a site threshold, erasing a person, removing site-owned memberships, managing API keys.
- Abuse limits on OTP, magic-link and invitation sending (per phone/email and per IP); provider webhooks verify provider signatures (0.4); signed file URLs are short-lived.
- Server **refuses placeholder or short keys** at startup; `_FILE` secret variants; `TRUSTED_PROXIES` required for `X-Forwarded-*`.
- Uploaded files: sniffing, size limits, `Content-Disposition: attachment` for non-images.

**Privacy (KVKK/GDPR-ready):**

- **Audit diffs are PII-safe**: fields registered as PII (names, contacts, national ids, plates, free-text notes) are stored as hashes before/after; non-PII fields as values.
- **Erasure** = `privacy_erase(person_id)` — a `SECURITY DEFINER` function owned by the migrator, the only path that rewrites PII columns in append-only tables (`journal_line.person_id` → null, `ballot`/`attendance` names → "Former owner #n", audit hashes untouched). The `person` row is anonymized; the `user` unlinked; occupancies keep unit and dates. Blocked while the person has an open balance unless an admin overrides (re-auth, audited). Backups age out per the retention policy page.
- **Retention**: `bank_transaction.raw` and `payment.provider_payload` stripped after reconciliation (default 90 days); sessions, login attempts, visitor logs (default 90 days, C) per `retention_setting`; `maintenance.prune` enforces.
- **Export**: per-person JSON + files archive by job.
- **Consent**: `consent` records with versioned texts (generic core texts; Turkish texts in 0.4 pack).
- Secrets (`SECRET_KEY`, `ENCRYPTION_KEY`) are state and part of the backup set (infra plan).

---

## 17. Configuration and operations

All variables carry the `OPENBEKCI_` prefix (`OPENBEKCI_DATABASE_URL`, …); the prefix is omitted in the table for brevity. Every secret-bearing variable accepts a `_FILE` variant.

| Variable | Required | Notes |
|---|---|---|
| `DATABASE_URL` | yes | app role |
| `MIGRATOR_DATABASE_URL`, `PLATFORM_DATABASE_URL` | no | default to `DATABASE_URL` with the respective role; the platform role must have BYPASSRLS |
| `DATABASE_SIDE_POOL_MAX` | no | size of the second app-role pool for transactions opened while another is held (draft file storage inside a filing); default 4, in addition to the app pool |
| `BASE_URL` | yes | public URL; cookies, links, Origin checks |
| `EXTRA_ORIGINS` | no | additional accepted origins (LAN name + public domain) |
| `SECRET_KEY`, `ENCRYPTION_KEY` | yes | ≥ 32 random bytes; placeholders refused; `_FILE` variants accepted |
| `DATA_DIR` | no | local storage root |
| `STORAGE_*` | no | S3 driver |
| `SMTP_*` | no | email channel disabled without it |
| `SMS_PROVIDER`, `SMS_NETGSM_*` | no (yes for phone-first sites) | core gateway; per-site credentials may override |
| `SEND_DAILY_BUDGET`, `SMS_HOURLY_BUDGET`, `MAIL_HOURLY_BUDGET` | no | sign-in message budgets: per identifier per day (default 10), per instance per hour for SMS (120) and mail (300); negative removes the limit |
| `API_KEY_RATE_PER_MINUTE` (0.4) | no | requests per minute per organization API key (default 600, with a burst of ten seconds' worth); negative removes the limit |
| `WEBHOOK_MAX_ATTEMPTS` (0.4) | no | delivery attempts per (event, endpoint) before the endpoint is marked failing (default 8, 1 to 20); backoff doubles from 30 s up to 4 h |
| `WEBHOOK_ALLOWED_CIDRS` (0.4) | no | comma separated CIDRs or IPs outbound webhooks may reach although private, loopback or link-local (self-hosted receivers on the LAN); empty keeps the SSRF guard strict. Webhook secrets are sealed with `ENCRYPTION_KEY` |
| `PUSH_RELAYS` (0.4) | no | list of `relay_id=url;server_id;secret_file;app_id` |
| `TRUSTED_PROXIES` | yes behind a proxy | CIDRs; also what lets the agent enrol rate limit (10/min) count per real client IP: without it behind a proxy every enrolment shares one budget |
| `OTEL_*`, `METRICS_TOKEN`, `SENTRY_DSN` | no | |
| `WORKER` | no | `embedded` (default) / `off` |
| `AUTO_MIGRATE` | no | `true` for simple compose setups |
| `INSECURE_DEV` | no | allows http cookies on localhost only |
| `CLAMAV_ADDR` | no | upload scanning |
| `API_VALIDATE_RESPONSES`, `DEBUG` | no | dev aids |

Commands: `serve [--no-worker]`, `worker`, `migrate up|down|status`, `admin create-platform-admin`, `admin create-org`, `admin export-site --site … --out …`, `admin import-site --org … --in …`, `admin anonymize-person --person …`, `admin rotate-keys`, `admin rebuild-charge-status --site …`, `admin backup-marker` (writes the "last backup" marker the platform dashboard reads), `admin packs list|enable|disable|purge` (0.4), `admin push-relay enroll` (0.4), `version`.

Startup checks: key strength, `BYPASSRLS` on the platform role, pooler detection (LISTEN support, prepared-statement mode), SMS gateway reachability (warn), storage writable.

Operations: migrations expand/contract; `migrate` explicit unless `AUTO_MIGRATE`; images distroless non-root multi-arch; backups = `pg_dump` + `DATA_DIR` + secrets (infra plan).

---

## 18. Testing strategy

| Layer | What | Tooling |
|---|---|---|
| Domain unit | Allocation with keys/scopes/zero weights, rounding, late-fee accrual and reconciliation incl. backdated payments, payment allocation orders, debtor snapshots, workflow transitions, quorum math (0.2) | `testing`, `rapid` |
| Repository/integration | sqlc queries on real Postgres; RLS isolation; composite FK rejection; triggers; migrations up→down→up | `testcontainers-go` |
| API | In-process server + generated client; responses validated against the spec; **authz matrix**; idempotency incl. concurrent same-key; dry-run imports | `kin-openapi` |
| Golden site | **Two-block site with a shop, a co-owned unit, a tenant change and an owner sale mid-year, opening balances, consumption-table allocation, a late-fee waiver, a collection case, a full year of postings** — shared with web e2e and docs | `testdata/golden-site` |
| Jobs | River in test mode; fake clock; catch-up after simulated downtime | `rivertest` |
| Golden files | PDFs (text layer, Turkish glyphs), emails, SMS texts, statement CSVs | `-update` flag |
| Performance | k6 smoke on a 500-unit site before 1.0 | nightly |

Targets: domain ≥ 90 %, overall ≥ 70 %; PR CI < 10 min.

---

## 19. Development workflow

- `Taskfile.yml`: `task gen` (bundle spec → oapi-codegen → sqlc → permission matrix → TS types; Dart only in CI), `task lint`, `task test`, `task test:integration`, `task run` (hot reload), `task migrate:new`, `task db:reset`.
- `deploy/compose/compose.dev.yml`: Postgres, Mailpit, MinIO (optional), an SMS stub that logs messages.
- Committed generated code: `httpapi/gen`, `clients/ts` types. Not committed: Dart client, MSW handlers, zod schemas (generated at build). `.gitattributes` marks generated files `linguist-generated`.
- `-tags nowebassets` for Go-only contributors; path-filtered CI so a web PR doesn't run Go integration tests and vice versa.
- Trunk-based; Conventional Commits; squash; DCO.
- ADRs for: codegen choice, PDF library, SSE vs polling (0.2), webhook signing (0.4).

---

## 20. Backend milestones by release

Estimates are realistic engineer-weeks for one backend engineer; the web plan runs in parallel with its own.

### 0.1 (≈ 26 weeks)

| Phase | Weeks | Deliverables | Exit criteria |
|---|---|---|---|
| **A — Skeleton** | 3 | Layout, config + startup checks, three DB roles, RLS helper + composite-FK convention + "every table has a policy" test, pgx/sqlc/goose, River, OpenAPI pipeline (3.0.3 subset, spec extensions, problem codes), discovery document + `/v1/instance`, sessions (opaque, cookie + bearer), `GET /v1/me`, **authz matrix harness**, audit with PII-safe diffs, slog/otel, Dockerfile + compose, CI | `/readyz` green from compose; TS types generated; authz harness runs against health/me; ADR-0001 codegen |
| **B — Setup & people** | 7 | Orgs, sites (settings, bank details, lock date), blocks, units (types, appurtenances, legal ref), allocation keys and values, persons (encrypted national id), occupancies (sides, household, correspondent), memberships (site-owned flag), vendors; password / magic link / **SMS OTP (Netgsm gateway)** / invitations (3 kinds) / site QR + unit claims; CSV/XLSX import with dry-run; directory consent; move-out use case with closing statement + certificate (PDF pipeline with embedded TTF); site export/import; `/codes`; platform minimal endpoints | Cross-tenant and composite-FK tests; authz matrix complete for all B endpoints; export→import round-trip of the golden site preserves ids |
| **C — Money** | 10 | Chart + funds + cash boxes; journal with lock date and fiscal years; opening balances import; dues plans with rates, allocation by key/scope with snapshots, charge generation (catch-up); late-fee accrual, crystallization, waivers, freezes; payments (cash with receipt sequences, transfer), allocation policy + manual override, credits, transfers, refunds; expenses (light, recurring) + optional bills; bank statement CSV import with manual matching and reconciliation balances; collections stages, ihtarname export, collection cases; statements (unit side / person) PDF; reports (cash-basis gelir-gider, debtor list, budget vs actual, trial balance, aging, collector and bank reconciliation, fund balances); charge-plan preview; `Idempotency-Key` required on money ops; re-auth list | Golden year passes with all §9.13 invariants; backdated-payment fee reversal property tests; receipt sequence gap-free under concurrency |
| **D — Operations** | 6 | Tickets (configurable workflows, categories, private/common, votes, merge, internal notes, photos), announcements (sender channels, counts, read receipts), documents (site folders, unit folders, generated filing), files (server-proxied, variants), notifications (in-app/email/SMS, site defaults, mutes), dashboards (site + org portfolio), privacy CLI (`anonymize-person`, export job), retention pruning | Delivery golden tests; `privacy_erase` test proves no PII remains in any table incl. audit; pruning honours retention |

### 0.2 — Governance (≈ 8 weeks)

Meetings, notices with delivery tracking, agenda, polls, attendance with proxies and QR, quorum/threshold/weight site settings with Law 634 presets (`(verify)` items checked by the legal maintainer first), decisions with effects (dues rate, membership with term, budget final, special assessment), minutes and decision history, term reminders, absentee notification.

### 0.3 — Gate & community (≈ 8 weeks backend + agent separately)

Facilities and bookings with fees → charges; visitors with QR tokens, approval requests (notification + OTP-style decision link), recurring visitors, deliveries; guard endpoints (data-minimized); access-control module: access points, credentials, opening log, agent protocol (outbound WebSocket/long-poll, local allow-list sync, offline operation); plate-recognition and relay driver interfaces. Agent design doc precedes the phase.

### 0.4 — Mobile & integrations (≈ 10 weeks)

Push relay client and `device` registration; web-login link; `sdk/` module, registry, enablement, `sdktest` (official packs only in 0.4 — a binary-driving mode for external packs is a 1.0 item); Turkish pack contents moved out of core where they are pack-shaped (templates, legal presets, bank formats, extra SMS providers, and the vergi numarası / TR IBAN / plate validators — **the TCKN checksum stays core per S6**); reference-data endpoint; first `PaymentProvider` (iyzico) with checkout endpoints and settlement accounting; webhooks; API keys.

### 1.0 (≈ 6 weeks backend)

Privacy UI endpoints, `/v1` freeze, performance pass (500-unit site), key rotation runbook tested, external security review findings closed, documentation.

---

## 21. Risks and open questions

| Risk / question | Mitigation / decision needed |
|---|---|
| Legal presets encoded from unverified claims | `(verify)` list in governance decisions; presets are site-editable; legal maintainer reviews before 0.2 |
| Debtor-side model is new ground for most open-source ledgers | Golden site includes tenant change and sale; invariants per side; statements per side and per person |
| Late-fee reconciliation after backdated payments | Pure accrual function + reconciling job; property tests are the gate |
| Bank formats | Need anonymized sample files from two banks before phase C ends |
| Site export/import completeness | Round-trip test on the golden site in phase B; every new table joins the export manifest (CI check) |
| Erasure function drifting from new PII columns | PII columns registered in one Go table used by audit, export and `privacy_erase`; CI fails on an unregistered column named like PII |
| Codegen choice | Spike in phase A; ADR |
| SMS costs surprising sites | Per-site monthly counter and preview counts; site defaults can turn SMS off for reminders/receipts |
| Solo-engineer wall clock | Phases are sequential for one person (≈ 26 weeks backend + 18 web); two people run B/C and the web in parallel |
