# OpenBekci — Project Plan (Overview)

| | |
|---|---|
| Status | Draft v0.1 |
| Date | 2026-11-06 |
| Scope | Whole project. Component plans: [backend](01-backend.md), [web](02-web.md), [mobile](03-mobile.md), [packs](04-packs.md), [infrastructure](05-infra.md) |

This is the top-level plan. It records what OpenBekci is, the decisions that bind every component, the shared contracts between components, and the milestone sequence. Each component has its own plan that must stay consistent with this one; when they conflict, this document wins and the component plan gets fixed.

---

## 1. What OpenBekci is

OpenBekci is an open-source **residential community management** application: apartment buildings, gated sites, housing estates, and mixed-use complexes. It replaces the spreadsheet, the WhatsApp group and the manager's notebook with one system that residents, owners, boards, managers and staff all use.

Primary users:

| User | What they need |
|---|---|
| **Site manager / management company** | Collect dues, pay expenses, keep books, handle tickets, communicate, keep legally required records |
| **Board members** (yönetim kurulu, denetçi) | Oversight: statements, approvals, meeting records, audit trail |
| **Owners** | Their units' statements, payments, voting, documents |
| **Residents / tenants** | Pay dues, open tickets, read announcements, book facilities, register visitors |
| **Staff** (guard, concierge, cleaner, technician) | Gate log, assigned tickets, schedules |

Design goals, in priority order:

1. **Core works everywhere.** With zero country packs installed, a site in any country can be fully managed: units, people, money, operations, governance.
2. **Country packs add, never patch.** Country-specific law, government integrations, payment providers and document formats plug into defined extension points. The core never contains `if country == "TR"`.
3. **Self-hostable by a non-expert.** One binary plus PostgreSQL. `docker compose up` is a complete installation.
4. **Money is trustworthy.** A real double-entry ledger underneath a simple "dues and expenses" UI. No floats, no silent rounding, immutable postings.
5. **Boring, long-lived technology.** The project should still build and run in ten years with a small maintainer team.

Explicit non-goals for v1: accounting for the management *company* itself (payroll, corporate tax), multi-currency sites, smart-home/IoT device control, a public marketing website inside the app, third-party untrusted plugins (official packs only).

---

## 2. Decisions

### 2.1 Decided

| Area | Decision |
|---|---|
| Backend | **Go**, modular monolith, single binary with embedded web assets |
| Web | **React + Vite + TanStack Router + TanStack Query**, SPA, shadcn/ui, i18n with RTL support from day one |
| Mobile | **Flutter**, Riverpod, go_router, must connect to any server instance (self-hosted or hosted) |
| Database | **PostgreSQL** (only required dependency). Row-level security as the tenant boundary |
| API | **OpenAPI 3.1, contract-first.** Go server, TypeScript client and Dart client are generated from one spec |
| Background jobs | **River** (Postgres-backed), no Redis/RabbitMQ requirement |
| Packs | Compile-time Go modules registered through a public SDK (`pkg/sdk`). WebAssembly sandboxing deferred until a real third-party need exists |
| IDs | UUIDv7 everywhere |
| Money | Integer minor units + ISO 4217 currency code, one currency per site |

### 2.2 Decided on 2026-11-06, after the plan review

These were open assumptions in v0.1 of this document; the [review](06-review.md) §2 made them blocking and the owner decided them. **Plans 01–05 have not yet been revised to reflect them** — until that pass lands, this table wins over any conflicting text elsewhere.

| # | Decision | Outcome | Affects |
|---|---|---|---|
| D4 | **Deployment model** | **Self-host first, SaaS-capable.** One build, multi-organization; self-hosting is the first-class install; a hosted service runs the same build. No open-core gating. Hosted-only concerns (signup, plans, invoicing organizations) live outside this repo | Multi-tenancy (§6.1), push relay (§6.5) |
| D2 | **Who owns the data** | **The organization is the access boundary; the site is the data-owning, portable unit.** An organization is "the office that runs things" (a management company, or a self-managed building wearing that hat). A site (one kat malikleri kurulu, one ledger) can be exported from one organization and imported into another with its history and ids intact; the site's own board and auditor memberships belong to the site, not the organization; an "offboard site" workflow exists. Designed into B1, not added later | Backend §4/§5/§7, site export/import, roles |
| D1 | **License** | **AGPL-3.0 for the server and web app, with an explicit pack-linking exception** (so privately written packs compiled into the binary do not become AGPL — stated honestly rather than implied). **MPL-2.0 for the mobile app** (avoids the App Store conflict). **Apache-2.0 for the pack SDK, the OpenAPI spec and the generated clients.** The exception text is a draft until a lawyer has read it; that must happen before the first outside contribution. Contributions under DCO | `LICENSE`, `LICENSING.md`, per-directory license files, `CONTRIBUTING.md` |
| D3 | **First public release** | **0.1: web-only, money-complete** as scoped in review §9 (~45–50 engineer-weeks). No mobile, meetings, bookings, visitors or pack SDK in 0.1; Turkish legal rules are core settings until the SDK exists. Milestones M0–M7 below are to be re-baselined around it | §8, all component milestone tables |
| D5 | **Phone-first sign-in** | **A minimal SMS gateway interface in core with one provider (Netgsm) from M1**, so phone OTP and SMS invitations work in 0.1. Other providers arrive through packs later | Backend identity and notifications |
| D6 | **Gate and automatic door control** | **Concepts in core, hardware in a separate module plus an on-site agent.** In 0.1 the core only *holds* resident vehicle plates per unit (and expected-visitor plates) and can export the list for the site's plate-recognition camera operator; nothing opens a gate. Real-time control — access points, credentials (plate, code, tag, app), opening log, guard "open" button — is an **access-control module** served by a small **on-site agent** (runs on the site LAN, connects outbound to the server, keeps the allow-list locally so gates work offline) with vendor **device drivers** registered the same way packs are. Scheduled **after 0.1 and before mobile** (0.2). App-opened doors come with the mobile app | Overview §3/§4, backend visitors module, a new `agent/` deployable in the infra plan |

Still assumed (not blocking):

| Decision | Assumption | Affects |
|---|---|---|
| **First country pack** | **Turkey** (`packs/tr`): Kat Mülkiyeti Kanunu (Law 634) rules, local payment providers, SMS providers, bank formats, KVKK texts. e-Devlet is **out** (no realistic path to the required agreement; TCKN validation + manual verification instead) | Which extension points get validated first |
| **Name / module path** | `github.com/openbekci/openbekci` (the repository currently lives at `github.com/ekremyk2/OpenBekci`; the module path is set when the GitHub organization exists) | Go module path, image names |

---

## 3. Product scope (core)

Grouped by domain. Each becomes a module in the backend and a feature area in the clients.

| Domain | Core capability |
|---|---|
| **Tenancy** | Organization → Site → Building → Unit hierarchy. Unit types (flat, shop, parking, storage). Unit shares for cost allocation. Facilities/common areas |
| **Identity** | Accounts with email and/or phone, password, passkeys, magic link, optional TOTP. External identity providers via packs (e-Devlet). Invitations |
| **People & roles** | Persons (may exist before they have an account), occupancies (owner / tenant / resident / representative with date ranges and ownership shares), memberships with role and scope (org / site / building / unit) |
| **Ledger** | Per-site double-entry ledger, chart of accounts, journal entries, periods with closing, unit sub-ledger (receivables), vendor sub-ledger (payables) |
| **Billing** | Dues plans (aidat): recurring or one-off, allocation policies (equal, by share, by area, by occupant count, fixed per unit), expense sharing, late-fee policy (pluggable), per-unit statements |
| **Payments** | Manual payment recording (cash, transfer), payment-to-charge allocation, bank statement import and matching (importers via packs), online payment providers (via packs), vendor bills and payments |
| **Tickets** | Maintenance and requests with workflow, assignment, comments, attachments, resident-visible vs internal notes |
| **Announcements** | Site/building scoped, audience targeting, scheduling, read receipts, notification fan-out |
| **Documents** | Folder tree per site, visibility rules, versions. Generated documents (statements, minutes, decision book) land here |
| **Meetings & voting** | General assemblies and board meetings: agenda, attendance and proxies, quorum rules (pluggable), votes per unit/share/person, decisions, minutes |
| **Bookings** | Facilities with rules and optional fees; bookings create charges |
| **Visitors & gate** | Expected visitors, arrivals/departures, vehicle (plate) registry per unit with export for camera operators, guard console. Physical gate/door control is **not core**: it is a separate access-control module with an on-site agent and device drivers (decision D6), after 0.1 |
| **Notifications** | In-app, email, push (core); SMS and others via packs. Per-user preferences, digests, per-org template overrides |
| **Audit** | Append-only audit log of every mutation with actor, before/after |
| **Privacy** | Per-person data export and anonymization that preserves ledger integrity; consent records |

---

## 4. Architecture at a glance

```
   Web app (React SPA)      Mobile app (Flutter)      External systems (banks,
   served by the server     any instance URL          payment providers, e-Devlet…)
          │                        │                          ▲
          │   HTTPS, OpenAPI v1    │                          │ pack adapters
          └────────────┬───────────┘                          │
                       ▼                                      │
   ┌──────────────────────────────────────────────────────────┴───┐
   │  openbekci  (one Go binary)                                  │
   │  ├─ HTTP API        generated from api/openapi.yaml          │
   │  ├─ Core modules    tenancy · identity · people · ledger ·   │
   │  │                  billing · payments · tickets · …         │
   │  ├─ Pack registry   compile-time packs (tr, …) enabled       │
   │  │                  per organization                         │
   │  ├─ Job worker      River; same process or `openbekci worker`│
   │  └─ Embedded web    web/dist via go:embed                    │
   └──────────────────────┬───────────────────────────┬───────────┘
                          ▼                           ▼
                   PostgreSQL                  Object storage
             data · RLS · jobs · outbox       local disk (default) or S3-compatible

   Optional: SMTP server, push relay (for mobile push), OpenTelemetry collector,
             pack-specific services (SMS gateway, e-invoice provider, …),
             on-site access agent (post-0.1: drives gates/doors on the site LAN, D6)
```

Principles behind this shape:

- **Modular monolith, not microservices.** One deployable, strong module boundaries inside. Modules talk through Go interfaces and domain events, never through each other's tables.
- **Contract first.** The OpenAPI spec is the source of truth; nobody hand-writes a client or a route.
- **Postgres does more than store rows.** Tenant isolation (RLS), job queue (River), event outbox, idempotency keys, rate-limit state. This keeps the required infrastructure to exactly one service.
- **Packs are first-class but not privileged.** A pack can only reach the core through `pkg/sdk`. If the core needs something a pack can't do through the SDK, the SDK is extended — the pack is never let inside.

---

## 5. Repository layout (monorepo)

```
openbekci/
├─ api/                    OpenAPI 3.1 source (split files) + bundled openapi.yaml   [Apache-2.0]
├─ server/                 Go backend                                              [AGPL-3.0]
│  ├─ cmd/openbekci/       main: serve | worker | migrate | admin …
│  ├─ internal/            core modules, platform adapters, HTTP layer, pack runtime
│  ├─ pkg/sdk/             public pack SDK                                         [Apache-2.0]
│  └─ db/                  migrations (goose) + sqlc queries
├─ web/                    React SPA                                               [AGPL-3.0]
├─ mobile/                 Flutter app                                             [AGPL-3.0]
├─ packs/
│  └─ tr/                  Turkish country pack (Go module + web/mobile pack assets)
├─ clients/
│  ├─ ts/                  generated TypeScript client (published to npm)           [Apache-2.0]
│  └─ dart/                generated Dart client (published to pub.dev)             [Apache-2.0]
├─ relay/                  push relay service (Go; holds FCM/APNs creds for store builds) [AGPL-3.0]
├─ deploy/                 compose/, systemd/, proxy/, helm/, observe/ (dashboards, alert rules)
├─ docs/                   this plan set, ADRs, user & admin docs source
├─ tools/                  codegen scripts, lint configs, dev scripts
├─ Taskfile.yml            task gen | lint | test | run | migrate …
└─ .github/                CI workflows, issue templates
```

Go workspace (`go.work`) ties `server`, `packs/*` and `pkg/sdk` together during development; packs are separately versioned modules.

---

## 6. Shared contracts (every component must follow these)

### 6.1 Multi-tenancy model

- **Organization** is the tenant. Every tenant-owned row carries `org_id`. A user may belong to several organizations.
- The API addresses an organization explicitly in the path: `/v1/orgs/{org_id}/...`. Resource IDs below that are globally unique UUIDs, so paths stay flat (`/v1/orgs/{org}/units/{unit}`), with `site_id` etc. as filters.
- **Platform administrator** (instance owner) is a separate concept from **organization administrator**. Self-hosters are both; SaaS operators are only the former.

### 6.2 API conventions

- JSON, `snake_case` field names.
- IDs: UUIDv7 strings. Timestamps: RFC 3339 in UTC. Dates: `YYYY-MM-DD` interpreted in the site's timezone.
- Money: `{ "amount": 123450, "currency": "TRY" }` — `amount` is an integer in minor units. Never a float, never a string.
- Lists: cursor pagination (`limit`, `cursor` → `next_cursor`), whitelisted `sort`, filters as query params. No unbounded lists.
- Errors: RFC 9457 `application/problem+json` with a stable machine-readable `code` and optional `errors[]` for field-level validation. **Clients translate by `code`; the server never sends user-facing prose for errors.**
- Mutations that create money movements require an `Idempotency-Key` header.
- Optimistic concurrency with `version` fields / `If-Match` on key resources.
- Versioning in path (`/v1`). Additive changes don't bump; breaking changes get `/v2` with an overlap period.

### 6.3 Authentication surface

- Web SPA (same origin as the server): httpOnly session cookie.
- Mobile and third parties: short-lived JWT access token + rotating opaque refresh token.
- Both back onto the same session store, so revocation works uniformly.

### 6.4 Permissions

- Permission names (`tickets.read`, `ledger.post`, …) and built-in roles are defined once in the backend and **exported through the OpenAPI spec**, so the web and mobile apps hide what the user can't do without duplicating rules. The server always enforces; the client only hides.

### 6.5 Mobile push

- Self-hosted servers cannot hold the store builds' FCM/APNs credentials. A small **push relay** service (run by the project for the official app builds, or by anyone for white-label builds) receives signed, minimal notifications from a server and forwards them. Servers register with a relay; devices register with their server. The payload carries no personal data — only an ID the app resolves by calling its server. Details in the infrastructure and mobile plans.

### 6.6 Internationalization

- UI strings live in the clients (web: Lingui catalogs; mobile: ARB files). Backend has its own catalogs only for server-rendered content: emails, PDFs, notification texts.
- Locale negotiation: user preference → `Accept-Language` → org default → `en`. Fallback chain `tr-TR → tr → en`.
- RTL is a layout requirement for the web and mobile apps from the first screen.
- Translation workflow: Weblate (or Crowdin) wired to the repo once strings stabilize (M3+).

### 6.7 Pack contract (summary; full detail in the backend plan)

A pack is a Go module implementing `sdk.Pack`, registered at compile time, enabled per organization. It may contribute: identity providers, payment providers, notification channels, statement importers, legal rules (quorum, voting weights, late-fee rules), allocation policies, document templates, settings (JSON Schema → generic forms in the clients), HTTP routes under `/v1/orgs/{org}/packs/{pack}/…`, jobs, event subscribers and its own namespaced tables (`pack_tr_*`). Web and mobile packs are optional UI bundles keyed by the same pack ID; most packs need no custom UI thanks to schema-driven settings.

---

## 7. Component plans

| Plan | Covers | Status |
|---|---|---|
| [01-backend.md](01-backend.md) | Go server: modules, domain model, multi-tenancy, auth, API, ledger rules, pack SDK, jobs, storage, security, testing, milestones | Draft v0.1 |
| [02-web.md](02-web.md) | React SPA: routes for management, portal and platform areas, generated client and data layer, cookie auth with passkeys/re-auth, permission-driven UI, feature areas per milestone, design system with RTL/a11y, Lingui i18n, RJSF schema-driven pack forms, embedding in the Go binary, testing, milestones W0–W6 | Draft v0.1 |
| [03-mobile.md](03-mobile.md) | Flutter: instance discovery, multi-account, auth/token storage, generated Dart client, offline cache and write queue, push via relay (FCM/APNs/UnifiedPush flavors), packs in mobile, release and white-label process | Draft v0.1 |
| [04-packs.md](04-packs.md) | Extension point catalogue, SDK principles and lifecycle, settings/secrets, data-only client contributions, versioning and conformance testing, authoring guide outline, pack governance, the Turkish pack by area with sequencing TR-1…TR-5, paper packs (DE/NL) as a generality check, milestones P0–P4 | Draft v0.1 |
| [05-infra.md](05-infra.md) | Artifacts and signing, compose/systemd/Helm/hosted targets, push relay design and operation, TLS/proxy, backups with tested restore, upgrade policy, observability stack with dashboards and alerts, security operations, scaling guidance, CI/CD and environments, docs site, milestones I0–I5 | Draft v0.1 |

---

## 8. Milestones

Milestones are cross-component. "Demo" is the sentence that must be true at the end.

| # | Name | Demo | Components |
|---|---|---|---|
| **M0** | Foundations | `docker compose up` serves a login page from one binary; CI is green; the OpenAPI spec has auth + health endpoints and generates Go/TS/Dart code | repo, server skeleton, web skeleton, spec pipeline, CI |
| **M1** | Manage a site | A manager creates an org and site, imports units and residents, assigns roles, publishes an announcement; residents log in and see it | tenancy, identity, people, announcements, web |
| **M2** | Money | Monthly dues are generated by allocation policy, payments recorded and allocated, every unit has a statement, late fees apply, the board sees a trial balance and expense breakdown | ledger, billing, payments, web |
| **M3** | Operations | Residents open tickets with photos, staff work them, documents are shared with visibility rules, facilities are booked, guards log visitors; email and in-app notifications flow | tickets, documents, bookings, visitors, notifications, web |
| **M4** | Governance | A general assembly is scheduled, quorum computed, votes cast (incl. proxies), decisions and minutes recorded and exported | meetings/voting, documents, web |
| **M5** | Mobile | The Flutter app connects to any instance, shows dues/tickets/announcements/bookings, receives push via the relay, submits tickets with camera photos | mobile, push relay, infra |
| **M6** | Turkish pack | With `tr` enabled: Law 634 quorum/voting/late-fee rules, decision-book and minutes templates, e-Devlet identity, a payment provider, an SMS channel, a bank statement importer, KVKK consent texts | pack SDK stable, packs/tr, schema-driven pack UI |
| **M7** | 1.0 | Data export/erasure, API freeze (`/v1` stable), docs site, upgrade path tested, translations for en/tr complete, security review done | all |

Milestones M1–M4 are web-first; the mobile app arrives at M5 against an API that has already stabilized. The pack SDK is developed alongside M2–M4 (the extension points are shaped by the core) and declared stable at M6.

---

## 9. Quality bars (definition of done for any feature)

- Spec first: the OpenAPI change is merged (or in the same PR) before/with the implementation.
- Server enforces authorization in the service layer; there is an authz matrix test for every new endpoint.
- Tenant isolation test: the feature's tables have RLS and a cross-tenant access test.
- Money paths have golden/property tests; ledger invariants never rely on the client.
- Every mutation writes an audit event.
- User-facing strings are in catalogs (no hard-coded prose), and RTL has been checked for new screens.
- Documentation: user-facing docs for the feature, ADR for any non-obvious decision.
- CI green: lint, generate-check, unit, integration, build matrix.

---

## 10. Governance and community

- Repository on GitHub under the `openbekci` organization. `main` protected; squash merges; Conventional Commits; semantic versioning with a single version for server+web (they ship together) and independent versions for mobile, SDK and packs.
- `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md` (private disclosure), `GOVERNANCE.md` (maintainers, decision process) from M0.
- Contributor License Agreement is **not** required; DCO sign-off is. (If the license split ever needs relicensing, that's an ADR — decide early.)
- Architecture Decision Records in `docs/adr/`. The first batch (ADR-0001…) records §2 of this document.
- Releases: GoReleaser builds binaries and images; GitHub Releases with SBOM and signed artifacts; a `latest` and a pinned tag.

---

## 11. Open questions

| Question | Owner | Needed by |
|---|---|---|
| Confirm the four assumptions in §2.2 | project owner | before M0 code |
| `oapi-codegen` vs `ogen` for the Go server (both fit; pick by a two-day spike) | backend | M0 |
| PDF generation approach (pure Go vs Gotenberg sidecar) | backend | M2 |
| Who operates the official push relay and under what terms | project owner | M5 |
| Translation platform (Weblate vs Crowdin) | project owner | M3 |
| Phone-only accounts in core (needs an OTP channel; core has email only) | backend | M1 |
