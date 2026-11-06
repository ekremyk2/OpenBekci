# OpenBekci — Project Plan (Overview)

| | |
|---|---|
| Status | **Draft v0.2** — revised on 2026-11-06 against the [plan review](06-review.md) and the decisions in [`docs/decisions/`](../decisions/) |
| Scope | Whole project. Component plans: [backend](01-backend.md), [web](02-web.md), [mobile](03-mobile.md), [packs](04-packs.md), [infrastructure](05-infra.md) |

This is the top-level plan. It records what OpenBekci is, the decisions that bind every component, the shared contracts between components, and the release sequence. Component plans must stay consistent with it; when they conflict, this document wins and the component plan is fixed. Product behaviour is decided in plain language in `docs/decisions/`; those files are the source for the feature lists below.

---

## 1. What OpenBekci is

OpenBekci is an open-source **residential community management** application: apartment buildings, gated sites (site), housing estates and mixed-use complexes. It replaces the spreadsheet, the WhatsApp group and the manager's notebook with one system that residents, owners, boards, managers and staff all use.

Primary users:

| User | What they need |
|---|---|
| **Management company / yönetici** | Collect dues, pay expenses, keep trustworthy books, handle tickets, communicate, run the annual assembly, keep legally required records — across one site or fifty |
| **Board members** (yönetim kurulu, denetçi) | Oversight: statements, reports, meeting records, audit trail |
| **Owners** | Their units' statements, payments, decisions, documents |
| **Tenants and household members** | Pay dues (tenants), open tickets, read announcements, book facilities, register visitors |
| **Site staff** (kapıcı, guard, cleaner) | Gate log, assigned tickets |

Design goals, in priority order:

1. **Core works everywhere.** With zero country packs installed, a site in any country can be fully managed. Country-specific law is expressed as **site settings with national defaults**, so a site in a country without a pack can still configure its rules.
2. **Country packs add, never patch.** Government integrations, payment providers, SMS and bank formats, document templates, legal presets and validators plug into defined extension points. The core never contains `if country == "TR"`.
3. **The site owns its data.** A site (one kat malikleri kurulu, one ledger) is a self-contained, portable unit that can move between management companies with its history intact (decision D2).
4. **Self-hostable by a non-expert.** One binary plus PostgreSQL. `docker compose up` is a complete installation.
5. **Money is trustworthy.** A real double-entry ledger underneath a simple "dues and expenses" UI. Integer money, immutable postings, every correction visible.
6. **Boring, long-lived technology.** The project should still build and run in ten years with a small maintainer team.

**Non-goals (through 1.0):** accounting for the management *company* itself (payroll, corporate tax, outbound e-invoices for its fees); multi-currency sites; in-app chat or messaging; contractor/vendor logins; binding online votes; an approvals workflow (boards decide in meetings, the system records); scheduled-maintenance/equipment registry; untrusted third-party plugins; a public marketing site inside the app.

---

## 2. Decisions

### 2.1 Technology (decided before the review, unchanged)

| Area | Decision |
|---|---|
| Backend | **Go**, modular monolith, single binary with embedded web assets |
| Web | **React + Vite + TanStack Router + TanStack Query**, SPA, shadcn/ui, i18n with logical CSS from day one |
| Mobile | **Flutter**, Riverpod, go_router; connects to any server instance |
| Database | **PostgreSQL** (only required dependency). Row-level security as the *organization* isolation boundary |
| API | **OpenAPI**, contract-first, authored in the 3.0.3-compatible subset; Go server, TypeScript and Dart clients generated from one spec |
| Background jobs | **River** (Postgres-backed); it is also the transactional outbox — no separate outbox tables |
| Sessions | **Opaque tokens** for web (cookie) and mobile (bearer); no JWT |
| Packs | Compile-time Go modules behind a public SDK (`sdk/`, its own module and license). SDK arrives in 0.4; until then national rules are core settings |
| IDs | UUIDv7 everywhere |
| Money | Integer minor units + ISO 4217 code, one currency per site |

### 2.2 Blocking decisions (owner, 2026-11-06)

| # | Decision | Outcome |
|---|---|---|
| D1 | **License** | AGPL-3.0 for server and web **with an explicit pack-linking exception** (draft pending legal review); **MPL-2.0** for the mobile app; **Apache-2.0** for `sdk/`, `api/` and generated clients. DCO, no CLA. See [LICENSING.md](../../LICENSING.md) |
| D2 | **Who owns the data** | The **organization** is the access boundary ("the office": a management company, or a self-managed building). The **site** is the data-owning, portable unit: export/import between organizations with ids preserved; board and auditor memberships belong to the site; an "offboard site" workflow exists. Designed into 0.1 |
| D3 | **First public release** | **0.1: web-only, money-complete** (§8). No mobile, meetings, bookings, visitors beyond a plate registry, or pack SDK |
| D4 | **Deployment model** | **Self-host first, SaaS-capable.** One multi-organization build; hosted-only concerns live outside this repo |
| D5 | **Phone-first sign-in** | A **minimal SMS gateway in core with one provider (Netgsm)** from 0.1: OTP codes, invitations, receipts, reminders |
| D6 | **Gate and door control** | Concepts in core (plate registry in 0.1); real-time control in a separate **access-control module + on-site agent** with device drivers, in 0.3 |
| D7 | **Release order** | 0.1 money + operations (web) → 0.2 governance → 0.3 gate control + community → 0.4 mobile + integrations → 1.0 |
| D8 | **Repositories** | **Core monorepo** (`OpenBekci`: server, web, API spec, SDK, packs, docs) **plus a separate mobile repository** (`openbekci-mobile`, created 2026-11-06, private until the owner says otherwise). The push relay and the on-site agent get their own repositories when built. Repositories live under `ekremyk2` for now and move to an organization later |

Still assumed: **Turkey** is the first country pack (e-Devlet is out: no realistic path to the agreement; TCKN validation + manual verification instead). Module path `github.com/openbekci/openbekci` once the GitHub organization exists (repository currently `github.com/ekremyk2/OpenBekci`).

### 2.3 Product decisions

Five plain-language files, one per area, each with decisions, defaults set without asking, and open points. They are the source of truth for behaviour; §3 summarizes them.

| Area | File | Release |
|---|---|---|
| Money | [decisions/money.md](../decisions/money.md) | 0.1 |
| Setup & people | [decisions/setup-people.md](../decisions/setup-people.md) | 0.1 |
| Daily operations | [decisions/daily-operations.md](../decisions/daily-operations.md) | 0.1 |
| Governance | [decisions/governance.md](../decisions/governance.md) | 0.2 |
| Community | [decisions/community.md](../decisions/community.md) | 0.3 |

---

## 3. Product scope by area

Tags: **0.1 / 0.2 / 0.3 / 0.4 / later**.

| Area | Capability |
|---|---|
| **Setup** (0.1) | Organization → Site → Block → Unit. Single buildings and multi-block sites. Unit types; parking/storage as appurtenances of a flat or as independent units (per site); door number vs legal reference; **allocation keys** per site (arsa payı, equal, area, custom) with a value per unit; facilities (0.3). Site export/import between organizations (D2) |
| **People** (0.1) | Persons exist before accounts. **Occupancies**: owner (co-owners with shares), tenant, household member (no money unless granted), with dates. **Memberships** for roles only: org admin, site manager, board member, auditor, accountant, staff; time-bounded; board/auditor memberships site-owned. Onboarding: spreadsheet import + SMS/email invitations, site QR with unit claim and manager approval, household invites by the unit's owner/tenant. Optional encrypted national ID (TCKN validator). Move-out/sale: closing statement, debt/no-debt certificate, debt stays with the person, former residents keep read access to their period. Directory off by default, opt-in per person |
| **Money** (0.1) | Light yearly **expense budget by category** (optional). **Dues plans** with a directly set amount, effective-dated rates linked to decisions, each plan naming an allocation key and scope (site/block); charge types with a default **debtor (owner / tenant)**; charge generation with catch-up. **Payments**: cash with gap-free numbered receipts and per-collector cash boxes; bank transfer matched from statement files (manual matching; rules later); card payments designed-for (gross/fee/net) and delivered with the first provider in 0.4. **Late fees**: continuous accrual, monthly crystallization, waivable, freezable. **Expenses**: light entry with receipt photo, recurring; vendor bills optional. Transfers between accounts, refunds, deposits as liabilities, reserve fund as a separate fund. **Opening balances** import with dry-run. **Collections**: reminder stages, ihtarname export, collection cases with freeze. Lock date; fiscal year; corrections only by reversal. **Reports**: cash-basis gelir-gider, debtor list, per-unit/per-person statement PDF, budget vs actual, trial balance, receivables aging |
| **Tickets** (0.1) | Simple default flow, **per-site configurable statuses** (open-like / closed-like semantics). Categories with default visibility and assignee role. **Private vs common** tickets; "+1" on common tickets; manager merges duplicates. Photos; internal notes; assignment to staff or a vendor label. No contractor logins; no SLAs |
| **Announcements** (0.1) | One-way, audience targeting (site/block/owners/tenants/units), scheduling, pinning, read receipts. **Sender chooses channels** per message with recipient and SMS count preview |
| **Documents** (0.1) | Site folders with visibility rules; a private folder per unit; generated documents (statements, receipts, certificates, later minutes) filed automatically. No versions |
| **Notifications** (0.1) | In-app, email, SMS (core gateway, D5); push with mobile (0.4). Site defaults for automatic messages; per-person mute for non-essential types; sign-in codes and invitations cannot be muted |
| **Governance** (0.2) | Full assembly paperwork: call letter with agenda and second date, per-owner delivery tracking, pre-meeting non-binding polls, attendance desk with proxies and QR check-in, live quorum under site settings with Law 634 defaults, decisions with counts and dissents, minutes PDF, numbered decision history. **Decisions trigger changes** (dues rate, elected roles with terms, budget approval, special assessments). Term-expiry reminders. Site-level assembly only; block assemblies and representatives' council later |
| **Gate & access** (0.3) | Access-control module + on-site agent + device drivers; plate recognition and relay drivers first; opening log; guard "open" button. (0.1 has only the plate registry and an export for camera operators) |
| **Community** (0.3) | Facility bookings with rules per facility, instant confirmation, optional fee → charge. Visitors: pre-registration with a QR for the guest, guard scans and admits; no QR → approve/deny request to the resident with a phone-call fallback; recurring visitors; guard console (web, tablet-sized); parcels log |
| **Mobile** (0.4) | Resident app: statement and payments, tickets, announcements, documents, bookings, visitors (QR share, approve from notification), notifications; push via relay; instance discovery; single account, multi-org |
| **Integrations & SDK** (0.4) | Pack SDK; Turkish pack (templates, legal presets, validators, bank formats, SMS providers); first payment provider (iyzico); webhooks and API keys |
| **Privacy** (0.1 CLI, 1.0 UI) | Per-person data export; erasure that works against append-only tables (§6.9); consent records; retention settings |
| **Audit** (0.1) | Append-only audit log of every mutation with actor and PII-safe diff |

---

## 4. Architecture at a glance

```
   Web app (React SPA)      Mobile app (Flutter, 0.4)     External systems (banks, SMS,
   served by the server     any instance URL              payment providers, cameras …)
          │                        │                          ▲
          │   HTTPS, OpenAPI v1    │                          │ core gateways + pack adapters
          └────────────┬───────────┘                          │
                       ▼                                      │
   ┌──────────────────────────────────────────────────────────┴───┐
   │  openbekci  (one Go binary)                                  │
   │  ├─ HTTP API        generated from api/openapi.yaml          │
   │  ├─ Core modules    tenancy · identity · people · money ·    │
   │  │                  tickets · announcements · documents ·    │
   │  │                  governance (0.2) · community (0.3) · …   │
   │  ├─ SMS gateway     core, one provider (D5)                  │
   │  ├─ Pack registry   compile-time packs (0.4)                 │
   │  ├─ Job worker      River; same process or `openbekci worker`│
   │  └─ Embedded web    web/dist via go:embed (`nowebassets` tag │
   │                     builds without it)                       │
   └──────────────────────┬───────────────────────────┬───────────┘
                          ▼                           ▼
                   PostgreSQL                  File storage
             data · RLS · jobs · sequences    local disk (default) or S3-compatible,
                                              always through the server

   Optional: SMTP, push relay (0.4, for mobile push), OpenTelemetry collector,
             on-site access agent (0.3: drives gates/doors on the site LAN, D6)
```

Principles behind this shape:

- **Modular monolith, not microservices.** One deployable, strong module boundaries inside. Modules talk through Go interfaces and River jobs, never through each other's tables.
- **Contract first.** The OpenAPI spec is the source of truth; nobody hand-writes a client or a route.
- **Postgres does more than store rows.** Organization isolation (RLS), job queue and outbox (River), idempotency keys, sequences. Exactly one required service.
- **The site is the unit of ownership; the organization is the unit of access.** RLS isolates organizations; site scoping is enforced in the service layer; a site can be exported and imported whole.
- **Packs are first-class but not privileged.** A pack reaches the core only through `sdk/`. If the core needs something a pack can't do through the SDK, the SDK is extended.

---

## 5. Repositories and layout (decision D8)

| Repository | Holds | License |
|---|---|---|
| **`OpenBekci`** (core monorepo, this one) | server, web, API spec, SDK, official packs, docs, deploy | per directory, below |
| **`openbekci-mobile`** | the Flutter app (0.4). Consumes the published spec and Dart client; its own release cadence and store process | MPL-2.0 |
| `openbekci-relay` (0.4), `openbekci-agent` (0.3) | push relay; on-site access agent — created when built | AGPL-3.0 |

Core monorepo:

```
OpenBekci/
├─ api/                    OpenAPI source (split files) + bundled openapi.yaml       [Apache-2.0]
├─ sdk/                    public pack SDK — its own Go module and LICENSE           [Apache-2.0]
├─ server/                 Go backend                                                [AGPL-3.0 + pack exception]
│  ├─ cmd/openbekci/       main: serve | worker | migrate | admin …
│  ├─ internal/            core modules, platform adapters, HTTP layer, pack runtime, sdktest (AGPL testkit)
│  └─ db/                  migrations (goose) + sqlc queries
├─ web/                    React SPA                                                 [AGPL-3.0]
├─ packs/
│  └─ tr/                  Turkish pack (0.4)                                        [AGPL-3.0]
├─ clients/
│  ├─ ts/                  generated TypeScript types (committed; consumed by web)   [Apache-2.0]
│  └─ dart/                generated in CI and published for the mobile repo; not committed [Apache-2.0]
├─ deploy/                 compose/, systemd/, proxy/, helm/ (later), observe/ (later)
├─ docs/                   plans/, decisions/, adr/, ops/, user docs source
├─ tools/                  codegen scripts, lint configs, dev scripts
├─ Taskfile.yml            task gen | lint | test | run | migrate …
├─ LICENSE, LICENSING.md
└─ .github/                CI workflows (path-filtered), issue templates
```

`go.work` ties `server`, `sdk` and `packs/*` together in development. A Go-only contributor needs neither Node nor a JVM: `-tags nowebassets` serves a "web not built" page, and Dart generation runs only in CI. The mobile repository pins a spec version and gets the Dart client from the core repo's releases (a published package or a release asset).

---

## 6. Shared contracts (every component must follow these)

### 6.1 Tenancy model

- **Organization** = access boundary and RLS tenant. Every tenant-owned row carries `org_id`. Users are instance-wide and may belong to several organizations.
- **Site** = data-owning unit: one ledger, one currency, one timezone, its own board/auditor memberships. **Site scoping is enforced in the service layer, not by RLS** — RLS gives organization isolation only.
- **Site transfer**: `export-site` produces an archive (people, occupancies, money history, documents, decisions, settings) with identifiers preserved; `import-site` into another organization; residents' logins survive because accounts are instance-wide; the source organization loses access on offboarding.
- The API addresses the organization in the path: `/v1/orgs/{org_id}/...`; resource ids below are globally unique UUIDs, so paths stay flat with `site_id` as a filter.
- **Platform administrator** (instance owner) is separate from organization administrator.

### 6.2 API conventions

- OpenAPI authored in the **3.0.3-compatible subset**; enums are **open** (clients must tolerate unknown values).
- JSON, `snake_case`. IDs UUIDv7. Timestamps RFC 3339 UTC; dates `YYYY-MM-DD` in the site's timezone.
- Money: `{ "amount": 123450, "currency": "TRY" }` — integer minor units.
- Lists: cursor pagination (`limit`, `cursor` → `next_cursor`), whitelisted `sort`, explicit filters. No `updated_since`/ETag sync in v1.
- Errors: RFC 9457 `application/problem+json` with a stable `code`; `errors[]` for fields. Clients translate by `code`.
- **Every `POST` that creates accepts `Idempotency-Key`**; it is *required* on money-moving operations. Keyed by `(org, principal, key)` with an in-progress row.
- Optimistic concurrency with `version` / `If-Match` on key resources.
- Spec extensions (defined once in backend §8): `x-openbekci-roles`, `x-idempotent`, `x-ui`, `x-secret`, `x-stability`.
- Files live under `/v1/orgs/{org_id}/files…`; uploads always go through the server.
- Versioning in path (`/v1`); `/v1` is **not frozen until 1.0**.

### 6.3 Authentication surface

- Web SPA (same origin): httpOnly session cookie holding an **opaque** token.
- Mobile and integrations: **opaque** bearer access token (short-lived) + rotating refresh token. Same `session` table, instant revocation, one code path.
- Sign-in methods in 0.1: password, magic link (email), **SMS OTP** (D5), invitations. Passkeys, TOTP, OIDC: later.

### 6.4 Permissions

- Permission names and built-in roles are defined once in the backend and **exported through the OpenAPI spec**. **Portal rights derive only from occupancies** (owner / tenant / household member); **memberships** carry staff and governance roles. The server enforces; the client hides.

### 6.5 Mobile push (0.4)

- Self-hosted servers cannot hold store-build FCM/APNs credentials. A **push relay** forwards content-free notifications; its protocol is specified in the infra plan. Content-free payload; the app fetches details. The official relay is a shared dependency and is documented as such.

### 6.6 Internationalization

- UI strings live in the clients (web: Lingui; mobile: ARB). Backend catalogs only for server-rendered content (emails, SMS, PDFs). Locale chain: user → `Accept-Language` → org default → `en`; fallback `tr-TR → tr → en`.
- **Logical CSS properties from day one** (lint rule); RTL *testing* is not a release gate before 1.0.
- PDFs are generated in pure Go with an **embedded TTF** that covers Turkish glyphs; no HTML-to-PDF sidecar.

### 6.7 Pack contract (0.4; full detail in the packs plan)

A pack is a Go module implementing `sdk.Pack`, compiled in through an explicit slice (no `init()` magic), enabled per organization. It may contribute: payment providers, SMS/notification channels, statement importers, legal **presets** (quorum/threshold/weight tables, late-fee rate), allocation policies, document templates, chart-of-accounts names, address formats, validators, reference data, consent texts, settings schemas, namespaced tables, **webhook/callback routes only**, jobs and event subscribers. Event payloads carry ids + type + version, never full records. **Nothing a pack does requires client code**; pack UI bundles are post-1.0.

### 6.8 Instance discovery

`GET /.well-known/openbekci.json` (unauthenticated) and `GET /v1/instance` (authenticated twin) are defined normatively in backend §8 and shipped in 0.1; web and mobile reference that schema and do not restate it.

### 6.9 Privacy

- Erasure must work against append-only tables: audit diffs store **field names and hashes** for PII fields, raw provider/bank payloads are stripped after reconciliation and have retention, and a single owner-side `privacy_erase()` function is the only path that rewrites PII columns in append-only tables (backend §16).
- Secrets (`SECRET_KEY`, `ENCRYPTION_KEY`) are part of the backup set.

---

## 7. Component plans

| Plan | Covers | Status |
|---|---|---|
| [01-backend.md](01-backend.md) | Go server: modules, domain model (from the decision files), multi-tenancy, auth, API, money rules, pack SDK, jobs, storage, security, testing, milestones per release | v0.2 |
| [02-web.md](02-web.md) | React SPA: routes for management, portal and platform, data layer, feature areas per release, design system, i18n, schema-driven pack forms (0.4), testing | v0.2 |
| [03-mobile.md](03-mobile.md) | Flutter app (0.4): instance discovery, auth, features, push via relay, release and white-label | v0.2 |
| [04-packs.md](04-packs.md) | Pack SDK (0.4), authoring, governance, the Turkish pack | v0.2 |
| [05-infra.md](05-infra.md) | Deployment targets, backups with secrets, upgrades, push relay (0.4), on-site agent (0.3), observability, release engineering | v0.2 |
| [06-review.md](06-review.md) | The review that drove v0.2; kept for the reasoning | applied |

---

## 8. Releases

The milestone scheme M0–M7 from v0.1 is retired. Releases are feature cuts; estimates are realistic engineer-weeks (roughly 2× the v0.1 face values), web and backend counted separately.

| Release | Contents | Demo | Estimate |
|---|---|---|---|
| **0.1 — "A Turkish site runs on it"** | Setup, people, money, tickets, announcements, documents, notifications (in-app/email/SMS), plate registry, audit, privacy CLI, site export/import. **Web only.** Compose install with Caddy, nightly backups incl. secrets, install/backup/restore/upgrade docs | A management company imports a site from Excel with opening balances, sets dues, records cash and bank payments, late fees accrue, every unit has a statement, the assembly gets its gelir-gider and debtor list; residents log in by SMS code, open tickets, read announcements | Backend ≈ 26 wks · Web ≈ 18 wks · Infra ≈ 4 wks → **≈ 48 engineer-weeks** (two people ≈ 6 months) |
| **0.2 — Governance** | Assemblies end to end, decisions that trigger changes, terms and reminders, call-letter delivery tracking, polls, QR check-in, minutes and decision history | A site runs its January assembly from the system and the elected board and new dues rate come out of the recorded decisions | Backend ≈ 8 wks · Web ≈ 8 wks |
| **0.3 — Gate & community** | Access-control module, on-site agent, plate-recognition and relay drivers, guard console; bookings, visitors with QR admission and resident approval, recurring visitors, parcels | A guest's QR opens the barrier; an unknown visitor is approved from the resident's portal; the hall is booked and charged | Backend ≈ 8 wks · Agent ≈ 6 wks · Web ≈ 6 wks |
| **0.4 — Mobile & integrations** | Flutter app, push relay, pack SDK, Turkish pack (templates, presets, validators, bank formats, SMS providers), first payment provider, webhooks, API keys | A resident pays by card from the app and gets a push when the guard logs a parcel | Backend ≈ 10 wks · Mobile ≈ 20 wks · Relay ≈ 4 wks · Packs ≈ 8 wks |
| **1.0** | Privacy UI, `/v1` freeze, Helm, observability stack, docs site, translations complete, external security review, legal review of the pack exception | A self-hoster migrates to the hosted service with `export-site` and back | ≈ 12 wks |

Later (unscheduled): block assemblies and representatives' council, board meetings, passkeys/TOTP/OIDC, scheduled maintenance and equipment, document versions, vendor logins, direct debit and ISO 20022 imports, budget-driven dues and year-end settlement, a second country pack, WebAssembly sandbox.

---

## 9. Quality bars (definition of done for any feature)

- Spec first: the OpenAPI change is in the same PR as the implementation.
- Server-side authorization in the service layer; a row in the authz matrix test for every new operation.
- Organization isolation: the feature's tables have RLS and composite-FK tenant consistency; a cross-tenant negative test exists.
- Money paths have golden/property tests; invariants never rely on the client.
- Every mutation writes an audit event with a PII-safe diff.
- User-facing strings in catalogs; logical CSS only.
- CI green: lint, generate-check, unit, integration, build matrix.

Not required per feature before 1.0: user docs, an ADR, a manual accessibility pass. Those happen per release.

---

## 10. Governance and community

- GitHub, `main` protected, squash merges, Conventional Commits, DCO sign-off. `SECURITY.md` (private disclosure) and `CONTRIBUTING.md` from the first code commit; `CODEOWNERS`, `GOVERNANCE.md` and a translation platform when the second regular contributor appears.
- Licensing per [LICENSING.md](../../LICENSING.md); the pack-linking exception is reviewed by a lawyer before the first outside contribution.
- Architecture Decision Records in `docs/adr/` for non-obvious *technical* choices; product behaviour lives in `docs/decisions/`.
- Releases: GoReleaser binaries and images, SBOM, keyless cosign signatures; one version for server + web; mobile, SDK, packs, relay and agent versioned separately.

---

## 11. Open questions

| Question | Owner | Needed by |
|---|---|---|
| Legal claims marked **(verify)** in [decisions/governance.md](../decisions/governance.md) and review §10.F (quorum bases, vote caps, proxy caps, second-meeting window, tebligat validity, late-fee pro-rating, KVKK 2024 regime) | a Turkish legal/practice maintainer | before 0.2 is encoded |
| Which banks' statement formats first; anonymized sample files | owner | 0.1 money phase |
| Apsiyon (or other) export importer — needs a sample export | owner | 0.1, optional |
| Merchant of record for card payments: site or management company | owner | before the 0.4 provider work |
| Who operates the official push relay, docs and demo domains | owner | 0.4 |
| Which plate-recognition camera brand the first sites use (first driver) | owner | before 0.3 |
| Module path / GitHub organization | owner | before the first code commit |
| `oapi-codegen` vs `ogen` (two-day spike); TS client generator spike | backend / web | 0.1 skeleton |
