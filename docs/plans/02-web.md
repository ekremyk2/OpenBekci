# OpenBekci — Web Plan

| | |
|---|---|
| Status | Draft v0.1 |
| Date | 2026-11-06 |
| Parent | [00-overview.md](00-overview.md) — shared contracts there bind this document. API details in [01-backend.md](01-backend.md); the mobile counterpart is [03-mobile.md](03-mobile.md) |

---

## 1. Goals and non-goals

**Goals**

- The **complete** interface: everything the system can do is reachable from the web app. Managers, accountants, boards and platform admins live here; residents and owners get a portal in the same app, shaped by their permissions.
- A single-page app, built by Vite, **embedded in the Go binary** and served from the same origin as the API. No Node server in production.
- Generated, type-safe API access from the OpenAPI spec; no hand-written request code.
- i18n with RTL and full keyboard/screen-reader accessibility from the first screen — this app is used by boards of mixed ages and abilities.
- Schema-driven forms so country packs need no custom React for their settings.
- Fast on cheap hardware and slow connections: route-level code splitting, small initial bundle, cached queries.

**Non-goals (v1)**

- Server-side rendering, SEO, or a marketing site (Astro, separately, if ever).
- Offline write support (mobile owns that). Offline *reading* of already loaded data is a side effect of query caching, not a feature.
- Micro-frontends or runtime-loaded pack bundles. Packs are data-driven (schemas, texts, URLs); custom pack React is a last resort behind a feature flag.
- Native desktop wrappers.

---

## 2. Users and the app's shape

| User | Area | What they do here |
|---|---|---|
| Site manager / management company | **Management** | Units, people, dues plans, charges, payments, bank import and matching, bills, tickets, announcements, documents, bookings, visitors, meetings, reports, settings |
| Accountant | Management (money) | Journal, periods, trial balance, reports, exports |
| Board member / auditor | Management (read-mostly) | Oversight dashboards, approvals, meeting records, audit log |
| Staff / guard | Management (narrow) | Ticket queue, gate console (a large-target layout for a desk tablet/PC) |
| Owner / resident | **Portal** | Statement, pay, tickets, announcements, documents, bookings, visitors, votes, profile |
| Platform admin | **Platform** | Organizations, instance settings, packs available, health, usage |

One app, one navigation component that builds its menu from the effective permissions in `GET /v1/me` (overview §6.4). A manager who is also a resident sees both areas under one login. The server enforces; the client hides.

---

## 3. Technology stack

| Concern | Choice | Notes |
|---|---|---|
| Language | TypeScript, `strict`, `noUncheckedIndexedAccess` | |
| UI library | React 19 | |
| Build | Vite | Dev server proxies `/v1` and `/.well-known` to the Go server |
| Routing | **TanStack Router** (file-based routes, typed params/search, loaders) | Search params are typed and validated with zod — filters/pagination live in the URL |
| Server state | **TanStack Query** | Query keys derived from the generated client's operation ids |
| API client | `openapi-typescript` (types) + `openapi-fetch` (runtime, ~2 kB) + `openapi-react-query` (TanStack Query bindings) — all generated/driven from `api/openapi.yaml` | Spike `orval` at W0 as the alternative (generates hooks directly); ADR |
| Forms | `react-hook-form` + `zod` resolvers; zod schemas for request bodies generated from the spec (`openapi-zod-client` or `typed-openapi`) | Field-level server errors (`errors[].field`) are mapped back onto form fields by code |
| Schema-driven forms | **react-jsonschema-form** (`@rjsf/core` + `@rjsf/validator-ajv8`) with a custom shadcn theme | For pack settings and org settings declared by JSON Schema (backend §10.1) |
| Components | **shadcn/ui** (Radix primitives + Tailwind) — component source lives in the repo | Owned code: theming, RTL and a11y fixes are ours to make |
| Styling | Tailwind CSS v4; logical properties (`ms-`, `pe-`, `start-`) only, never `ml/mr/left/right` | Enforced by an ESLint rule |
| Tables | TanStack Table + TanStack Virtual for long lists; server-side cursor pagination | |
| Charts | Recharts (reports, dashboards); kept behind a lazy chunk | |
| Dates/numbers | `Intl.*` for formatting; `date-fns` + `@date-fns/tz` for arithmetic in the site's timezone | Never format with the browser timezone for site-scoped dates |
| Markdown | `react-markdown` + `remark-gfm` + `rehype-sanitize` for rendering; a lightweight toolbar textarea editor (`@uiw/react-md-editor`) for authoring | Bodies are Markdown per backend §4.8 |
| i18n | **Lingui** (ICU messages, catalogs per locale, extraction in CI, pseudo-locale) | RTL via `<html dir>` driven by locale |
| Icons | `lucide-react` | Direction-sensitive icons mirrored in RTL via a wrapper |
| PDF viewing | Native browser viewer in an `<iframe>` from a signed URL | No PDF.js bundle |
| Component workshop | Storybook (with interaction tests and a11y addon) | The design system's documentation |
| Testing | Vitest, Testing Library, MSW (handlers generated from the spec with `openapi-msw`), Playwright (e2e + axe + visual snapshots) | |
| Lint/format | ESLint flat config (`typescript-eslint`, `react-hooks`, `jsx-a11y`, `tailwindcss`, custom rules), Prettier, `knip` for dead code | |
| Package manager | pnpm, single `web/` package (no JS monorepo tooling needed; the generated client is published from `clients/ts`) | |

Deliberately avoided: Next.js/Remix (no SSR need; would break the single binary), Redux/MobX (TanStack Query covers server state; local UI state is React state or small zustand stores where needed), CSS-in-JS, Material UI, moment.

---

## 4. Architecture

```
web/
├─ src/
│  ├─ main.tsx                  bootstrap: i18n, query client, router
│  ├─ routes/                   TanStack Router file routes (see §5)
│  ├─ app/
│  │  ├─ router.tsx             route tree, guards (auth, org, permission), pending/error components
│  │  ├─ providers.tsx          QueryClient, I18n, Theme, Toaster, Dialog manager
│  │  ├─ instance.ts            discovery document (/.well-known/openbekci.json), branding, auth methods
│  │  ├─ session.ts             current user, orgs, permissions (from /v1/me), re-auth
│  │  └─ theme/                 tokens, dark mode, density, brand color → CSS variables
│  ├─ api/
│  │  ├─ schema.d.ts            generated by openapi-typescript (do not edit)
│  │  ├─ client.ts              openapi-fetch instance + middleware (headers, problem+json, 401/412 handling, idempotency keys)
│  │  ├─ query.ts               openapi-react-query binding, query key helpers, invalidation map
│  │  └─ errors.ts              AppError { code, fieldErrors, status }, code → message lookup
│  ├─ features/                 one folder per domain (tenancy, people, money, tickets, …)
│  │  └─ tickets/
│  │     ├─ components/         feature-specific UI
│  │     ├─ hooks/              queries/mutations composed from api/query
│  │     ├─ forms/              zod schemas + form components
│  │     └─ pages/              route components (thin: compose hooks + components)
│  ├─ components/
│  │  ├─ ui/                    shadcn/ui primitives (owned)
│  │  ├─ data/                  DataTable, filters, cursor pagination, Money, SiteDate, EmptyState, ErrorState
│  │  ├─ forms/                 FormField wrappers, MoneyInput, DateInput (site tz), UnitPicker, PersonPicker
│  │  ├─ schema-form/           RJSF theme for shadcn
│  │  └─ layout/                AppShell, Sidebar (permission-driven), Topbar, OrgSwitcher, CommandPalette
│  ├─ lib/                      utils: money, dates, permissions (`can()`), files, markdown
│  └─ locales/                  Lingui catalogs: en/, tr/, pseudo/
├─ public/
├─ .storybook/
├─ e2e/                         Playwright
├─ index.html                   template; the Go server injects the CSP nonce and instance branding at serve time
├─ vite.config.ts
└─ package.json
```

Rules:

- Route components are thin; business UI lives in `features/*`. Features import from `components/`, `api/`, `lib/`, and other features' **hooks** only (never their components' internals).
- No fetch outside `api/`. Every request goes through the generated client so a spec change fails the type-check, not production.
- URL is the state for anything a user might bookmark or share: filters, tabs, selected period, page cursor.
- Every list has loading (skeleton), empty, error and "no permission" states via the shared `DataTable`/`EmptyState` components.

---

## 5. Routes

```
/login                               methods from the discovery document
/login/passkey, /login/magic, /login/oidc/callback
/invite/$token                       accept invitation, create/link account
/reset/$token
/select-org                          when the user has >1 org and none remembered
/orgs/$orgId
   /                                 home: role-aware dashboard (manager KPIs or resident summary)
   /sites, /sites/$siteId            site settings, buildings, units (+ import wizard), facilities
   /people                           persons, occupancies, memberships, invitations; /vendors
   /money
      /overview                      receivables, cash, aging, recent payments
      /plans                         charge plans (dues) and allocation preview
      /charges                       all charges, filters, bulk actions
      /payments                      record, allocate, history; /payments/import (bank statement matching)
      /bills                         vendor bills, bill payments
      /statements                    per unit statement + PDF
      /journal, /periods             accountant views, period close
      /reports                       trial balance, income/expense, aging, budget vs actual (later)
   /tickets, /tickets/$id            list/board, detail timeline; /tickets/queue (staff)
   /announcements, /announcements/$id, /announcements/new
   /documents, /documents/f/$folderId
   /bookings                         calendar + list; /bookings/facilities
   /visitors                         list; /visitors/gate (guard console layout)
   /meetings, /meetings/$id          agenda, attendance, votes (live), decisions, minutes
   /notifications
   /settings
      /organization, /roles, /notifications (templates & overrides), /packs, /packs/$packId,
      /api-keys, /webhooks, /audit, /privacy (export/erasure requests)
   /me                               profile, my units, sessions, security (passkeys, TOTP), consents, preferences
/platform                            platform admin: /orgs, /settings, /packs, /health, /jobs
```

Resident/owner users see the same `/orgs/$orgId/...` tree; routes they can't access are absent from navigation and guarded (`403` page with "ask your manager"). The **portal** is thus a filtered view, not a second app — one set of components to maintain, matching the mobile app's screens one-to-one so support can reason about both.

---

## 6. Data layer

- **Client**: `openapi-fetch` with middleware that adds `Accept-Language`, `X-Requested-With: openbekci-web` (CSRF per backend §6), `Idempotency-Key` for operations tagged `x-idempotent` in the spec (a UUID generated per form submission and reused on retry), and `If-Match` from the entity's `version` for `PATCH/PUT`.
- **Errors**: `application/problem+json` → `AppError`. `401` → clear session cache and redirect to `/login?next=`; `403` → inline permission state; `412 version_conflict` → "this record changed, reload?" dialog with a diff where feasible; `422 validation_failed` → field errors onto the form by `errors[].field`; `429` → toast with retry-after; network errors → retry button.
- **Queries**: keys are `[operationId, params]` from `openapi-react-query`; `staleTime` 30 s default, 5 min for reference data (units, people, accounts), `0` for money mutations' targets. An **invalidation map** in `api/query.ts` says which operation ids a mutation invalidates (e.g. `payments.createPayment` → charges, statements, dashboard). Optimistic updates only for trivial toggles (read receipts, pin, ticket status), never for money.
- **Lists**: cursor pagination (`limit`, `cursor`) with `useInfiniteQuery` for feeds and a page-style `DataTable` for admin grids; sort/filter in the URL search params (typed by the route).
- **Reference data**: units, people, accounts, categories are fetched once per org and shared by pickers; `updated_since` keeps them fresh.
- **Polling**: notifications badge polls `GET /v1/me/notifications?unread=1` every 60 s while the tab is visible; a live vote view polls every 5 s. SSE (`/v1/me/events`) replaces polling when the backend ships it (ADR at B3/W3).
- **Prefetch**: route loaders prefetch the primary query; links prefetch on hover/intent.

---

## 7. Authentication and session

- Cookie session (backend §6); the SPA never sees a token. On load the app fetches the discovery document and `GET /v1/me`; a `401` shows the login page.
- Login page renders the methods the instance supports: password (+ TOTP step), magic link, **passkeys** (WebAuthn via `@simplewebauthn/browser`; conditional UI/autofill on the username field), external providers (redirect flow; the server owns the OAuth dance; the SPA only handles `/login/oidc/callback` by refetching `/v1/me`).
- Invitation and reset links land on their routes and continue to account creation/linking.
- **Re-authentication modal** for sensitive actions (backend §16.1 list): the API answers `403 reauth_required`; the modal collects password/passkey, calls `POST /v1/auth/reauth`, then replays the original request.
- Org context: `/orgs/$orgId` is the source of truth; the switcher navigates. Last org remembered in `localStorage`.
- Session management under `/me/security`: active sessions, revoke, passkey enrolment, TOTP setup with recovery codes.
- Idle logout is server-driven (cookie expiry); the app shows a "signed out" banner when a request returns `401` mid-session and preserves unsaved form state in memory for the re-login round trip.

---

## 8. Permissions in the UI

- `Permission` is a generated string union from the spec; `can(perm, scope?)` reads the effective permissions in `/v1/me` (per org, with site/building/unit scopes).
- `<Can perm="ledger.post" site={siteId}>…</Can>` and route `beforeLoad` guards. Navigation items declare their permission; the sidebar filters itself.
- Resource-level rules (own units only) are not re-implemented client-side: the API already returns only what the user may see; the UI just avoids offering actions that would `403`.
- The authz matrix test in the backend is the contract; a web e2e suite logs in as each built-in role and snapshots the navigation to catch regressions in what is shown.

---

## 9. Feature areas

Grouped by the project milestone that delivers them.

### 9.1 M1 — Manage a site
- **Onboarding**: create org → first site → buildings → units. **Import wizard** for units and people from CSV/XLSX: upload → server `dry_run` preview with row-level errors → confirm → async job → result report. Column mapping UI with remembered mappings.
- **Units**: table with inline edit for shares/area/type; merge/split later. Facilities.
- **People**: persons with occupancies timeline per unit (who lived/owned when), memberships with scope, invitations (email/phone), vendor directory.
- **Announcements**: Markdown editor with preview, audience and scope pickers, schedule, pinned, attachments, read-receipt counts.
- **Settings**: organization, site (timezone, currency, fiscal year), roles overview (built-in, read-only in v1), audit log viewer with filters and diff view.
- **Portal**: home summary, announcements, profile.

### 9.2 M2 — Money
- **Money overview**: receivables aging, cash/bank balances, collection rate this period, top overdue units.
- **Charge plans**: create/edit; allocation policy picker with **live preview** (what each unit would pay) before saving; one-off expense sharing wizard from a bill.
- **Charges**: filterable grid (period, unit, status), cancel/adjust with reason (creates reversal), bulk reminders.
- **Payments**: record (cash/transfer) with unit/person picker and automatic oldest-first allocation shown and editable; payment history; receipts (PDF).
- **Bank import**: upload statement → importer (core CSV or pack) → matching screen: auto-matched, suggested, unmatched; keyboard-driven confirm; create matching rules from a match.
- **Bills/vendors**: bill entry with expense account, due tracking, bill payment, attachments.
- **Statements**: per unit, period range, running balance, PDF download, "send to resident".
- **Accountant views**: journal with entry detail (lines, source link, reversal), periods with close/reopen (re-auth), trial balance, income/expense by account and period, exports (CSV/XLSX via API).
- **Portal**: statement, charges, pay (provider checkout URL or bank details + "I've paid" note).

### 9.3 M3 — Operations
- **Tickets**: list and kanban board (status columns), filters by category/assignee/unit/priority, detail with timeline (status history, public/internal comments, attachments with image lightbox), assignment, SLA indicators (later), staff queue view.
- **Documents**: folder explorer with breadcrumb, drag-drop upload with progress, visibility editor, versions, preview (PDF/images), move/rename.
- **Bookings**: facility calendar (week/month), slot rules shown, create/approve/reject, fee preview → charge link, conflicts prevented by the server and reflected live.
- **Visitors**: list by day, pre-registration form, vehicle registry, **gate console**: a full-screen, large-target page for guards (today's expected, search by name/plate, check-in/out, walk-in entry); auto-refresh.
- **Notifications**: center, per-type channel preferences, org-level template overrides editor with variable help and preview per locale.
- **Files**: shared upload component (presigned/direct, progress, cancel, mime/size validation mirroring server limits).

### 9.4 M4 — Governance
- **Meetings**: create with agenda builder, invitation dispatch (channels), attendance desk (check-in list, proxy entry with document upload), quorum indicator live (from `LegalRules`), per-item **vote runner** (open/close, live tally polling, eligibility explanation), decisions with sequence numbers, minutes editor (Markdown → PDF via templates), decision-book export.
- **Portal**: upcoming meetings, proxy declaration, online ballot while open, results.

### 9.5 M5 — Mobile support (small)
- "Connect the mobile app" page: QR code with server URL and optional invitation; web-login link acceptance route for deep links from the app.

### 9.6 M6 — Packs and TR
- **Packs settings**: `/settings/packs` lists packs from the instance; enable per org; **schema-driven settings form** (RJSF + uiSchema from the pack), validation errors from the server mapped to fields. Secrets fields rendered as write-only.
- Pack-provided consent texts in the portal; pack identity providers on the login page; pack payment providers in the pay flow; pack importers in bank import; pack templates in document generation — all data-driven, no TR-specific React.
- API keys and webhooks management screens.

### 9.7 M7 — 1.0
- Privacy: export/erasure request handling screens; retention settings.
- Command palette (`⌘K`) for navigation and quick actions; keyboard shortcuts for the matching and gate screens.
- Platform admin area complete; health/jobs views.
- Performance and a11y audit closure; full tr/en catalogs.

---

## 10. Design system

- **shadcn/ui** components live in `components/ui/` and are modified freely; upstream updates are pulled deliberately, not automatically.
- **Tokens** as CSS variables: brand seed (from the instance's discovery `branding.primary_color`, with safe defaults), semantic colors (success/warning/danger/info), radius, spacing, density (comfortable/compact — managers with big tables want compact).
- Light/dark by system preference with a manual override; both themes checked in Storybook and Playwright snapshots.
- **RTL**: `dir` on `<html>` from the locale; only logical Tailwind utilities; mirrored icons; tested with the `pseudo-rtl` locale in Storybook and e2e.
- **Accessibility** targets: WCAG 2.2 AA. Keyboard-complete (every dialog, menu, table action), focus management on route change, visible focus rings, `aria-live` for toasts and live tallies, form errors associated with fields, `prefers-reduced-motion` respected. `jsx-a11y` lint + axe in e2e + a manual screen-reader pass per milestone.
- Shared domain widgets with one implementation each: `Money`, `MoneyInput` (minor units ↔ display, currency-aware), `SiteDate`/`DateInput` (site timezone), `UnitPicker`, `PersonPicker`, `PeriodPicker`, `StatusBadge`, `AuditDiff`.
- Storybook documents every `components/*` item with states, RTL and dark variants; interaction tests run in CI.

---

## 11. Internationalization

- Lingui with ICU MessageFormat; `en` source, `tr` complete before each milestone's release; catalogs compiled at build, loaded per locale lazily.
- Extraction (`lingui extract`) runs in CI and fails on untranslated `tr` strings at release time (warning on PRs).
- Pseudo-locale (`pseudo` and `pseudo-rtl`) for layout testing.
- Formatting via `Intl.NumberFormat`/`DateTimeFormat` with the user's locale and the **site's timezone** for site-scoped dates; money through `Money` only.
- API codes (errors, enums like ticket status, charge kind) are mapped in `lib/codes.ts`; adding a code is a single entry plus catalog strings.
- Locale resolution mirrors the overview: user setting → browser → org default → `en`; the choice is persisted to the user profile when signed in.

---

## 12. Schema-driven forms (packs and org settings)

- Packs declare settings as JSON Schema (draft 2020-12) plus an optional uiSchema (field order, widgets, groups, help texts with i18n keys). The server serves `GET /v1/orgs/{org}/packs/{pack}/settings/schema`.
- RJSF renders it with the shadcn theme: text, number, boolean, enum/select, arrays, nested objects, `format: secret` (write-only), `format: money`, `format: date`, `format: unit-ref`/`person-ref` (custom pickers).
- Client validates with ajv for immediate feedback; the server is authoritative and its field errors are mapped back.
- The same mechanism renders org/site settings that core declares by schema, so there is one settings-form implementation.

---

## 13. Files and documents in the browser

- Upload component handles both backend modes (presigned S3 PUT or direct multipart to the server), parallel uploads with progress, cancellation, client-side mime/size checks matching the server's limits, image downscaling before upload for ticket photos (canvas, ≤2000 px).
- Downloads always via `GET /files/{id}` → signed URL; the SPA never constructs storage URLs.
- Previews: images inline, PDFs in an `<iframe>` with the signed URL, others as download cards.

---

## 14. Performance budget

- Initial route (login or dashboard) ≤ 250 kB gzipped JS; each feature route lazily loaded; charts, markdown editor, RJSF and QR code generation in their own chunks.
- `pnpm build` prints bundle sizes; CI fails on budget regression (`size-limit`).
- TanStack Virtual for tables over ~200 rows; cursor pagination server-side regardless.
- Images via server variants (`?variant=thumb`), lazy loaded.
- Lighthouse CI on the dashboard and statement pages: performance ≥ 85, accessibility 100, best practices 100.

---

## 15. Security

- Strict CSP from the server with a per-response nonce injected into `index.html` (backend §16.1); no inline scripts/styles except nonce'd; Tailwind's generated CSS is a static file. `connect-src 'self'` plus configured storage origin for presigned uploads.
- No tokens in JavaScript (cookie auth); `X-Requested-With` + same-origin; CORS not needed.
- Markdown rendering sanitized (`rehype-sanitize` allowlist); no `dangerouslySetInnerHTML` elsewhere (lint rule).
- Links to external hosts open with `rel="noopener noreferrer"`; payment checkout and OIDC are top-level navigations, never iframes.
- Dependency hygiene: `pnpm audit` and Dependabot; lockfile committed; `knip` for unused deps; no `postinstall` scripts from dependencies (`pnpm` config).
- Sensitive views (statements) are not cached by the browser (`Cache-Control: no-store` from the API).

---

## 16. Testing

| Layer | What | Tooling |
|---|---|---|
| Unit | `lib/*` (money, dates, permissions, codes), query invalidation map, form schemas | Vitest |
| Component | Feature components and forms with MSW handlers generated from the spec; all list states; field-error mapping | Vitest + Testing Library + `openapi-msw` |
| Storybook | Every design-system component with interaction tests and the a11y addon | Storybook test runner in CI |
| E2E | Against the real backend from `docker compose` seeded with the backend's golden 24-unit site (shared with the mobile plan): onboarding + import, dues cycle (plan → charges → payment → statement), bank matching, ticket lifecycle, booking, meeting with a vote, pack settings form; one login per built-in role with navigation snapshot | Playwright |
| Accessibility | axe on every e2e page; keyboard-only run of the critical flows | `@axe-core/playwright` |
| Visual | Snapshots of key pages × {light, dark} × {ltr, rtl} | Playwright snapshots |
| Contract | `tsc` against regenerated `schema.d.ts` — a breaking spec change fails CI | `task gen` + typecheck |

PR CI: lint, typecheck, unit/component, Storybook tests, build + size budget (< 8 min). Nightly and release branches: e2e, visual, Lighthouse.

---

## 17. Build, embedding and development workflow

- Dev: `pnpm dev` (Vite on :5173, proxy to Go on :8080 for `/v1`, `/.well-known`, `/files`); the Go server is started with `task run` from `server/`, or the whole stack via `deploy/compose.dev.yml`.
- `task gen` (repo root) regenerates `schema.d.ts`, zod schemas and MSW handlers from `api/openapi.yaml`; CI's generate-check covers the web output too.
- Production: `pnpm build` → `web/dist` → copied/embedded by the Go build (`internal/webassets`). `index.html` is a template the Go server renders with the CSP nonce and the instance branding; all other assets are immutable, content-hashed, served with long cache headers.
- The web app has **no version of its own**: it ships inside the server release (overview §10). A `/v1/instance` version mismatch (stale SPA cached after a server upgrade) triggers a "reload to update" banner.
- Node LTS pinned via `.nvmrc`/`packageManager`; `corepack` for pnpm.

---

## 18. Backend requirements driven by this plan

Recorded in the backend plan (§8 and milestones):

1. `index.html` served as a template with CSP nonce and branding injection; immutable asset caching.
2. Import endpoints support `dry_run=true` returning a row-level validation report before the async job runs.
3. Allocation **preview** endpoint for charge plans (`POST …/charge-plans:preview`) returning per-unit amounts without persisting.
4. Operations tagged `x-idempotent` in the spec so the client knows where to send `Idempotency-Key`.
5. `403 reauth_required` + `POST /v1/auth/reauth`.
6. Pack settings schema endpoint and uiSchema conventions (`x-ui` extensions).
7. Unread count endpoint cheap enough to poll; SSE decided by ADR at B3/W3.
8. `Cache-Control: no-store` on statement and personal-data responses.

---

## 19. Milestones

| # | Name | Deliverables | Exit criteria |
|---|---|---|---|
| **W0** | Skeleton (≈2 wks, M0) | Vite + TS + TanStack Router/Query, generated client pipeline, openapi-fetch middleware, shadcn baseline + tokens + dark/RTL, Lingui with en/tr/pseudo, AppShell + permission-driven sidebar, login (password) + `/v1/me`, Storybook, Vitest/Playwright/MSW setup, size budget, embedding in the Go build | Login → empty dashboard served from the binary; CI green; ADR on client generator |
| **W1** | Manage a site (≈4 wks, M1) | Onboarding, sites/units/facilities, import wizard, people/occupancies/memberships/invitations, announcements, org/site settings, audit viewer, portal home/profile, passkeys/TOTP/magic link on login | E2E: onboarding + import + announcement; role navigation snapshots |
| **W2** | Money (≈6 wks, M2) | Money overview, charge plans with preview, charges, payments + allocation, bank import matching, bills/vendors, statements + PDF, journal/periods/reports/exports, portal statement + pay | E2E dues cycle and matching; accountant keyboard flows |
| **W3** | Operations (≈5 wks, M3) | Tickets list/board/detail, staff queue, documents explorer, bookings calendar, visitors + gate console, notifications center/preferences/templates, upload component | E2E ticket/booking/visitor; gate console a11y on tablet viewport |
| **W4** | Governance (≈4 wks, M4) | Meetings, attendance desk, live vote runner, decisions, minutes, portal voting | E2E meeting with a vote; live tally under polling |
| **W5** | Packs & integrations (≈4 wks, M6) | Packs settings with RJSF theme, consent texts, provider-driven login/pay/import surfaces, API keys, webhooks, "connect mobile" QR page | TR pack configurable end-to-end from the UI without TR-specific code |
| **W6** | 1.0 (≈3 wks, M7) | Privacy screens, platform admin, command palette, perf/a11y audit closure, full catalogs, docs screenshots | Lighthouse and axe gates; WCAG AA manual pass |

---

## 20. Risks and open questions

| Risk / question | Mitigation / decision needed |
|---|---|
| Client generator choice (`openapi-typescript` trio vs `orval`) | W0 spike on: typed search params, mutation ergonomics, MSW generation, bundle size. ADR |
| RJSF theme effort and RJSF's maintenance pace | Keep the schema subset small and documented; theme ~15 widgets; if RJSF stalls, `@autoform/react` or a hand-rolled renderer for the same subset |
| Two "apps" (management + portal) drifting in one codebase | Same routes, same components, permission-filtered; e2e per role prevents drift |
| Bank matching and gate console need keyboard-heavy, dense UIs that generic components don't serve well | Treat them as dedicated screens with their own components and shortcuts from W2/W3, not grid add-ons |
| Polling costs on large sites (live votes, notifications) | Visible-tab only, backoff when idle; SSE when the backend ships it |
| CSP nonce and Vite's dev mode | Dev disables nonce enforcement (report-only); production enforces; e2e runs against the production build served by Go |
| Large tables in older browsers used by management offices | Support evergreen browsers + last 2 versions; virtualization; no IE/legacy builds |
| Lingui extraction discipline | CI extraction + reviewers' checklist; `pseudo` locale in Storybook makes hard-coded strings obvious |
