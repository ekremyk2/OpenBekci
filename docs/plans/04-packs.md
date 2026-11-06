# OpenBekci — Packs Plan (SDK, authoring, Turkish pack)

| | |
|---|---|
| Status | **Draft v0.2** — revised on 2026-11-06 against the [plan review](06-review.md) and [`docs/decisions/`](../decisions/) |
| Parent | [00-overview.md](00-overview.md) §6.7 binds this document. SDK runtime corrections in [01-backend.md](01-backend.md) §10; schema-driven pack settings forms in [02-web.md](02-web.md) §12; packs in the mobile app in [03-mobile.md](03-mobile.md) §10; licensing per [LICENSING.md](../../LICENSING.md) |

**Where packs sit in the release plan.** The pack SDK and the first real pack ship in core release **0.4**. Until then there is no pack mechanism, and everything Turkish is in the core: the `tr` locale, Law 634 presets as default site settings when `country_code = TR`, the TCKN checksum validator, Turkish document templates in the core template set, and Netgsm SMS through the core gateway (decision D5). Releases 0.1–0.3 wait for nothing in this document. In 0.4 the pack-shaped parts of that Turkish content move out of the core into `packs/tr`, and the integrations that only make sense behind a provider interface (iyzico, İleti Merkezi, bank statement formats) are built as pack contributions.

---

## 1. Goals and non-goals

**Goals**

- A pack SDK that lets a country (or a vendor) add integrations, documents, legal presets and reference data **without touching the core**, and without the core knowing the pack exists — the core never contains `if country == "TR"` (overview design goal 2).
- The Turkish pack (`packs/tr`) as the first complete pack and the proof that the extension points are the right ones.
- A site without any pack stays fully usable: packs **fill settings and add providers**; they never hold a rule hostage (overview design goal 1).
- Packs that encode law cite their sources, name a maintainer and carry a disclaimer.

**Non-goals (through 1.0)**

- Runtime-loaded or untrusted plugins (a WebAssembly sandbox is a post-1.0 ADR).
- Pack client code: no web or mobile bundles from packs before 1.0 (overview §6.7). Packs contribute data the clients already know how to render.
- Packs that replace core behaviour (no "override the ledger"); identity providers as an extension point (sign-in methods are core; OIDC is a later core feature, and e-Devlet is out — overview §2.2).
- A marketplace. Packs are code in repositories; the docs list them.

---

## 2. What a pack is

A pack is a Go module that implements `sdk.Pack`, is compiled into the server binary through an explicit list, and is **enabled per organization**. It may contribute implementations for the extension points below and nothing else.

| Extension point | Interface (in `sdk`) | Core default (no pack) | Called by |
|---|---|---|---|
| Payment provider | `PaymentProvider` (§3.8) | none — cash and bank transfer only | payments: checkout, status, webhooks, refunds, settlement |
| SMS provider | `SMSProvider` | Netgsm driver in the core gateway (D5) | core `sms.Gateway`: the site picks a provider from core + enabled packs |
| Statement importer | `StatementImporter` | generic CSV with configurable columns, manual matching | payments import |
| Legal presets | `QuorumPreset`, `ThresholdPreset`, `VoteWeightPreset`, `SchedulePreset`, `TransferRule`, `LateFeePreset` (§3.7) | generic defaults in site settings; Law 634 values when `country_code = TR` until 0.4 | settings seeding on site creation / pack enable; governance and billing read **site settings**, never the pack |
| Allocation policy | `AllocationPolicy` | share (arsa payı), equal, area, table (decision M2) | billing |
| Payment allocation order | `PaymentAllocationOrder` | oldest first, principal before fee; manual override always | payments |
| Document template | `DocumentTemplate` | generic statement, receipt, closing statement, debt certificate, call letter, attendance list, minutes, decision list | documents render job |
| Chart of accounts names | `ChartOfAccounts` | generic chart (backend §4.4 subtypes) | site creation; packs localize names, never structure |
| Address format | `AddressFormat` | lines, locality, region, postal code, country | tenancy, clients (field list as data) |
| Validators | `Validator` (tax id, IBAN variants, plate formats) | IBAN, E.164 phone, email, **TCKN checksum (core, S6)** | people, vendors, imports |
| Reference data | `ReferenceData` (admin areas, banks, holidays) | ISO countries and currencies | served by a core reference-data endpoint (added to backend §8 with 0.4); cascading address selects, due-date rolling |
| Consent texts | `ConsentTexts` | generic privacy notice | privacy module; clients show Markdown per locale with a version |
| Settings | `SettingsSchema` (JSON Schema + `x-ui`, `x-secret`) | — | settings module; web schema-driven forms |
| Migrations | goose `fs.FS`; tables `pack_<id>_*` with RLS | — | `migrate` |
| Routes | `Routes` — **webhook/callback only** (§3.5) | — | HTTP layer under `/v1/orgs/{org}/packs/{id}/…` |
| Jobs | `JobDefinition` (River) | — | worker |
| Event subscribers | `Subscribe(type, handler)`; payload `{type, version, ids}` (§3.6) | — | River fan-out |
| Invoice intake | `InvoiceIssuer` — **reserved, post-1.0**; planned use is inbound e-Fatura intake into vendor bills | none | expenses |

**Optional interfaces** (type-asserted; a pack implements only what it needs): `OnEnable(ctx, org)`, `OnDisable(ctx, org)`, `ValidateSettings(ctx, scope, values) error` (cross-field and live checks such as testing provider credentials), and two platform hooks — `StartupCheck(ctx) error` (configuration sanity at boot; an error aborts startup with a named message) and `Ready(ctx) error` (contributes to `/readyz`, warn-only). That is the complete list; nothing else is called on a pack by reflection or name.

---

## 3. SDK design

### 3.1 Modules and licences

| Path | Module | Licence | Holds |
|---|---|---|---|
| `sdk/` | top-level Go module, its own `go.mod` and `LICENSE` | **Apache-2.0** | interfaces, manifest types, registry contract, readers/posters types, event types |
| `server/internal/sdktest` | part of the server module | **AGPL-3.0** | the test harness that boots the core and drives packs through the registry — it cannot live in the Apache module because it imports the core |
| `packs/tr` | own module | **AGPL-3.0** | the official Turkish pack |
| `server/cmd/openbekci/packs.go` | server | AGPL | `var Official = []sdk.Pack{tr.New()}`; `-tags nopacks` leaves it empty |

`go.work` ties `server`, `sdk` and `packs/*` together in development. The server **imports** `sdk`; `sdk` imports nothing from the server. The SDK is Apache-2.0 so that anyone may build against it; whether a *private* pack escapes the server's AGPL obligations is governed by the **pack-linking exception** in `LICENSING.md` (draft, pending legal review), not by the SDK's licence — see §5.

### 3.2 Principles

- **Narrow, typed, versioned.** Every interface is small and documented with the exact call sites in the core.
- **Namespaced everything.** Routes, settings keys, job kinds, tables, template ids, event handler names and catalog keys are prefixed with the pack id. The registry rejects collisions at startup, so a bad pack cannot boot the server.
- **Tenant-safe by construction.** `Services.DB()` only hands out tenant transactions (RLS applies to `pack_*` tables; the migration helper adds the policy and the composite tenant FK convention). There is no raw pool.
- **No reach into core tables.** Packs read through `UnitReader`, `PeopleReader`, `LedgerReader`, … and write through `LedgerPoster`, `PaymentRecorder`, `Notifier`, `FileStore`. The core's invariants (balanced entries, lock date, idempotency, audit) apply to pack writes exactly as to its own. Field encryption uses the core helper (backend §16).
- **Fail closed.** A pack that panics in a handler is recovered and the request fails with `pack_error`; a pack whose `Register` fails prevents startup with a named error. Jobs and handlers of a disabled pack are skipped, never errored.
- **Everything localizable.** Pack labels, template strings and consent texts are catalogs per locale on the core fallback chain (overview §6.6).

### 3.3 Registration and lifecycle

| Phase | What happens |
|---|---|
| Compile | `cmd/openbekci/packs.go` lists official packs in `var Official = []sdk.Pack{…}`. **No `init()` registration.** A fork adds its pack to the slice |
| Startup | The registry reads `Manifest()` (id, version, `MinSDK`, `Countries`), checks `MinSDK`, calls `Register(ctx, registry)`, runs `StartupCheck` where implemented, validates namespacing, then freezes |
| `migrate` | Core migrations, then each pack's migrations in pack-id order with its own goose table `pack_<id>_goose_version`. Pack migrations run even when no organization has the pack enabled: schema is global, data is per organization |
| Enable (per organization) | Org admin enables from settings; settings validated against the schema and `ValidateSettings`; `OnEnable` seeds presets into site settings for the organization's sites that have not overridden them, localizes chart-of-accounts names, registers templates |
| Running | The core selects pack implementations only for organizations where the pack is enabled; pack routes answer 404 otherwise |
| Disable | `OnDisable`; data retained; implementations stop being selected. Settings already filled by presets **stay as they are** (they are the site's settings now); providers and importers become unavailable and the UI says so |
| Purge | `openbekci admin packs purge --org … --pack …` deletes the pack's rows for that organization after confirmation; never automatic |
| Remove from build | Only by rebuilding without the pack; tables stay until an operator drops them (documented) |

The discovery document lists enabled pack ids (`packs: ["tr"]`) and the server's `sdk_version`; `GET /v1/instance` carries pack details (backend §8).

### 3.4 Settings and secrets

- `SettingsSchema` = JSON Schema 2020-12 plus the two spec extensions defined in backend §8: `x-ui` (order, groups, widgets, help keys) and `x-secret: true` on write-only fields that are stored encrypted, never returned (the UI shows "set / not set") and redacted from audit diffs.
- Scopes: `org` (default) and `site` (e.g. a provider account per site). The pack reads with `Settings().Get(ctx, scope, key)`.
- The core validates on write (`santhosh-tekuri/jsonschema`); `ValidateSettings` is the optional hook for cross-field and live checks.
- Legal presets do **not** live in pack settings; they fill core site settings (§3.7).

### 3.5 Routes: webhooks and callbacks only

Pack routes exist for provider webhooks and return-URL callbacks, mounted at `/v1/orgs/{org}/packs/{id}/…`. A route is registered with `Public()`: the tenant context is set from the path, **no session is required**, and the pack verifies the provider's signature before doing anything (backend §16). Routes never appear in the OpenAPI spec as client operations. **Anything a client calls goes through a core endpoint** — checkout creation (`checkout_url` / `return_url` + status poll, backend §8), settings, consent texts, labels, reference data — which calls the pack behind the interface. This keeps "nobody hand-writes a client route" true.

### 3.6 Events

Packs subscribe to core events by type. The payload is `{type, version, ids}` — never a record — and the handler fetches what it needs through readers, so core internals are not frozen into the public SDK. Delivery is one River job per `(event, subscriber)` with unique-job dedupe (backend §11); handlers must be idempotent on the ids.

### 3.7 Presets fill site settings

`LegalRules` as one interface is gone. Each preset is a small interface that returns **default values for existing site settings** so a site without a pack can configure the same rules by hand and a site with a pack can still deviate where its yönetim planı allows:

| Preset | Fills | Core default without a pack |
|---|---|---|
| `QuorumPreset` | first-call and second-call quorum bases | majority of units present on the first call; none on the second |
| `ThresholdPreset` | per-topic decision threshold table | simple majority of those present for every topic |
| `VoteWeightPreset` | weight per unit, per-person cap, co-owner representation, proxy caps | one vote per unit, no caps |
| `SchedulePreset` | minimum call notice, second-meeting window, ordinary assembly month | 15 days, 7–15 days, January |
| `TransferRule` | what happens to debt on move-out/sale | stays with the person (S7) |
| `LateFeePreset` | `late_fee_policy`: monthly rate ceiling and default, grace days, pro-rating | configurable % per month, default 5, no ceiling |

Presets are applied when a site is created (by `country_code`) and when a pack is enabled, only to settings the site has not changed; the settings UI shows "pack default" next to a value and lets the site edit it. Governance (0.2) and billing (0.1) read site settings exclusively, which is why they work before the SDK exists.

### 3.8 Payment providers

`PaymentProvider` is defined around how providers actually settle (review §3, decision M5):

- **Gross / fee / net**: the resident pays gross, the provider keeps a fee, the net is settled to a bank account; the core posts gross to the resident's receivable, the fee to an expense account and the net through a per-provider **clearing account** until the settlement arrives.
- **Refunds** through the provider, posted as reversals on the same accounts.
- **Idempotent webhooks** keyed by `(org, provider, provider_txn_id)`; replays are acknowledged and ignored; signatures verified by the pack.
- **Merchant of record** is a core payments setting (site or management company, decision M5 — still open) that the provider reads; it decides whose provider account is used and whether the net is client money.
- Checkout is created by a core endpoint; the provider returns `checkout_url`; the client polls status; the webhook confirms (backend §8).

The core `payment` row already carries `gross`, `fee`, `net`, `method = card:<provider>` and `provider_txn_id` from 0.1 so that the first provider changes no schema.

### 3.9 What reaches the clients (data only)

| Contribution | Delivered how |
|---|---|
| Settings form | Schema endpoint; the web app renders it with schema-driven forms (web §12); mobile shows no admin settings |
| Consent screens | `GET …/privacy/consents` returns Markdown per locale with a version |
| Labels for pack-provided codes (provider statuses, importer ids, template names) | Merged into the core `GET /v1/orgs/{org}/codes?locale=` endpoint (exists since 0.1); clients map unknown codes through it or fall back to the raw code |
| Address fields | `AddressFormat` → field list, labels, regexes; `ReferenceData` behind a core endpoint for il → ilçe → mahalle |
| Documents | Files through the documents module, filed automatically (O8) |
| Checkout | `checkout_url` opened in a browser/web view; status poll |

A pack that would need real client code (NFC, a native SDK) is out of scope before 1.0; the client plans reserve an enabled-packs flag and nothing more.

### 3.10 Versioning and compatibility

- `sdk/` is semver'd independently of the server; **v1.0 is frozen at project release 1.0**. Until then minor versions may break with migration notes.
- `Manifest.MinSDK` is checked at startup; `/v1/instance` exposes the server's SDK version.
- Evolution rule: add behaviour only via new optional interfaces checked by type assertion; never widen an existing interface. Breaking changes batch into `sdk/v2`.
- `Manifest.Countries` (ISO 3166-1) drives the "suggested packs" hint when an organization picks a country.

### 3.11 Testing support

- `server/internal/sdktest` boots Postgres (testcontainers), runs core and pack migrations, creates an organization and a site from the golden fixture (backend §18), enables the pack, and exposes typed helpers (`PostCharge`, `RecordPayment`, `RunJob`, `CallRoute`, `Emit`). Official packs in the monorepo use it directly; external packs run it by adding themselves to a fork's `Official` slice. A mode that drives a built server binary is a 1.0 item, not a 0.4 one.
- **Conformance suites** are kept to the two interfaces with external side effects in 0.4: `ConformPaymentProvider` (idempotent webhooks, refund posting, error mapping) and `ConformStatementImporter(samples)` (format detection, totals, raw-payload stripping). Others follow when a second pack needs them.
- Provider HTTP interactions are recorded with `go-vcr`; live tests run only with credentials in CI secrets, nightly.

---

## 4. Authoring guide (outline of `docs/packs/`, written in 0.4)

1. **Start from `packs/example`** — a deliberately tiny pack: one validator, one template, one setting, one webhook route, one job, one subscriber, one migration, with tests. `tools/newpack <id>` copies it.
2. **Manifest and registration** — id, version, countries, `MinSDK`; what each `Register*` does and where the core calls it; the optional interfaces.
3. **Data** — `pack_<id>_*` naming, RLS and composite-FK helper, migrations, readers and posters, encryption helper.
4. **Settings** — schema conventions, `x-ui`, `x-secret`, scopes, `ValidateSettings`.
5. **Presets** — which site settings each preset fills and how a site overrides them.
6. **Localization** — catalogs, fallback chain, pluralization, templates per locale, labels through `/codes`.
7. **Legal content** — the `LEGAL.md` requirement (articles with dates, effective-from, maintainer, disclaimer); scenario tests that double as documentation.
8. **Testing** — `sdktest`, the two conformance suites, recorded HTTP.
9. **Building a server with your pack** — `tools/obkbuild` (an xcaddy-style helper: `obkbuild --with github.com/acme/openbekci-pack-de@v0.3.0` generates a `main` with the import and runs `go build`), or a one-file edit of `packs.go` in a fork. Kept minimal: generate, build, done.
10. **Publishing** — repository layout, tagging, listing in the docs' pack index.

---

## 5. Governance and licensing

Only what matters before a second contributor exists:

- **Legal maintainer.** The Turkish pack has a named maintainer with property-management or legal background **before its presets are encoded** — the same person who checks the `(verify)` list (overview §11), needed before 0.2 encodes the governance presets in the core and again when they move into the pack in 0.4.
- **`LEGAL.md`** in every pack that encodes law: each rule with the article, the date of the law text consulted, known ambiguities, and which settings the yönetim planı may override. It opens with the disclaimer, repeated on the enable screen: the rules implement the maintainers' reading of the law at a given date, are not legal advice, and organizations remain responsible for compliance.
- **DCO** sign-off on every contribution (no CLA). Because of that, the pack-linking exception below cannot be widened later without every contributor's consent — it exists from the first commit.
- **Licensing** (LICENSING.md): the server is AGPL-3.0 **with a pack-linking exception** (draft, pending legal review before the first outside contribution). Under the exception a pack that talks to the server only through `sdk/` may stay private; without it a compiled-in pack would be part of a combined AGPL work. The Apache-2.0 licence of `sdk/` makes building against the interfaces unencumbered, but it is the exception, not the SDK licence, that makes private packs possible. Official packs are AGPL-3.0 like the core.
- Later (when there is a second regular contributor): `CODEOWNERS` entries per pack, a review rule "legal-rule changes need the pack maintainer plus one core maintainer", and a community pack index with a light listing bar (builds with `obkbuild`, passes the conformance suites, has `LEGAL.md`).

---

## 6. The Turkish pack (`packs/tr`)

### 6.1 What is core already, what moves in 0.4

| Already core (0.1–0.3) | In 0.4 |
|---|---|
| `tr` locale and catalogs | stay core |
| Law 634 governance and late-fee defaults as site settings when `country_code = TR` | become `tr` **presets**; the core keeps generic defaults for sites without a pack |
| TCKN checksum, encrypted `person.national_id`, masking, manual verification (S6) | stay core |
| Turkish document templates in the core template set | Turkish-wording templates move to the pack as `DocumentTemplate`s; the core keeps generic equivalents |
| Netgsm SMS in the core gateway (D5); OTP, invitations, receipts, reminders | stays core; the pack adds **İleti Merkezi** (others by contribution) as `SMSProvider`s |
| Generic CSV bank import with manual matching | the pack adds two bank formats and the reference-code scheme |
| Generic consent texts | KVKK texts move to the pack |
| Cash and bank-transfer payments with gross/fee/net on `payment` | the pack adds **iyzico** |

### 6.2 Scope by area

| Area | Content | Extension points |
|---|---|---|
| **Governance presets** (Kat Mülkiyeti Kanunu No. 634, the site's yönetim planı) | Quorum: first call a majority by **both** owner count and arsa payı, second call no quorum, decisions by majority of those present **(verify)**. Vote weight: one vote per unit regardless of arsa payı, co-owners through one representative, a person holds at most one-third of all votes **(verify, art. 31)**. Proxies: at most 5 % of total votes per person; in buildings of 40 units or fewer at most two others **(verify)**. Thresholds: a per-topic table — simple majority of those present; majority by number *and* share; four-fifths; unanimity — with article citations **(verify article mapping)**. Schedule: call ≥ 15 days before, second meeting 7–15 days after the first **(verify)**; ordinary assembly yearly in January unless the yönetim planı says otherwise **(verify)**; yearly reminder for the notary closing of the karar defteri **(verify)**. Every value editable per site (governance.md defaults and open point) | `QuorumPreset`, `ThresholdPreset`, `VoteWeightPreset`, `SchedulePreset` |
| **Late fee** | Core accrual / monthly crystallization model (decision M4). The pack supplies the statutory monthly rate preset (**5 %**, the site may set lower, never higher) and daily pro-rating of the accrual **(verify)**; fees on principal only; freeze while in legal collection is core | `LateFeePreset` |
| **Move-out** | Remaining debt stays with the leaving person (S7) — the Turkish rule equals the core default; the pack only confirms it | `TransferRule` |
| **Documents** | Call letter (çağrı) with agenda and second date; per-owner delivery checklist; hazirun cetveli; vekaletname; toplantı tutanağı; karar defteri page draft (numbered, "draft for transcription"); aidat makbuzu; hesap ekstresi; ihtarname; borç durum belgesi; closing statement. **İşletme projesi is later**: budget-driven dues are post-1.0 (decision M3); the light category budget is core | `DocumentTemplate` |
| **Chart of accounts** | Turkish names for the core default chart, aligned with Tek Düzen naming where useful | `ChartOfAccounts` |
| **Address and reference data** | İl / ilçe / mahalle lists (static, versioned), optional UAVT address code, postal codes; Turkish public holidays for due-date rolling and meeting scheduling | `AddressFormat`, `ReferenceData` |
| **Validators** | Vergi numarası, TR IBAN specifics, vehicle plate format. TCKN checksum stays core (S6) | `Validator` |
| **Identity** | **Nothing.** e-Devlet is removed entirely — no dark flow, no "pending agreement" state. Identity = TCKN checksum + manual verification by the manager, both core | — |
| **Payments** | **iyzico** first (hosted checkout, webhooks, refunds) on the §3.8 settlement model; PayTR by contribution. Bank-transfer instructions with the site's IBAN and a reference-code scheme (`<unit>-<period>`) to ease matching. Merchant of record open (§9) | `PaymentProvider` |
| **Bank statements** | Importers for the CSV/XLSX exports of **two banks first** (candidates: Ziraat, İş, Garanti BBVA, Yapı Kredi, Akbank — chosen when anonymized sample files exist, §9); table-driven format definitions; matching suggestions on reference code, payer name and amount (rules are a later core feature) | `StatementImporter` |
| **SMS** | İleti Merkezi provider (Netgsm is core); **İYS** note: operational notices are exempt from commercial-message consent **(verify)**, marketing is out of scope; every message is tagged operational | `SMSProvider` |
| **Privacy (KVKK)** | Aydınlatma metni and açık rıza texts per processing purpose (identity, contact, financial, camera/visitor logs), versioned; note on the 2024 transfer regime (Law 7499: adequacy, standard contracts notified within 5 working days) **(verify)**; VERBİS registration guidance; data-residency recommendation (Turkey-hosted deployments); the **debtor list is never public** (core rule; KVKK Board practice **(verify)**) | `ConsentTexts`, docs |
| **E-invoicing** (post-1.0) | **Inbound e-Fatura intake only**: vendor invoices received through a GİB-approved integrator become vendor bills with net/tax/gross. Outbound e-Fatura/e-Arşiv for the management company's own fees is a non-goal (overview §1) | `InvoiceIssuer` (reserved) |
| **Docs: tax and employer obligations** | A page in `packs/tr/LEGAL.md` stating what the software does **not** do: Kimlik Bildirme Kanunu resident-change reporting, kapıcı employer duties (SGK, muhtasar), interest withholding, common-area rent income tax **(verify scope)** | docs |
| **Localization** | `tr` catalogs only for what the pack adds (provider statuses, importer names, template strings) via `/codes`; the `tr` UI locale is core | catalogs |

### 6.3 Settings (schema sketch)

Pack-owned settings (namespaced `tr.`):

```
tr.payments.iyzico          object { enabled, api_key (x-secret), secret_key (x-secret), sandbox }     org or site
tr.sms.iletimerkezi         object { enabled, username, password (x-secret), sender_id }               org or site
tr.bank.formats             list of enabled importer ids                                                org
tr.kvkk.data_controller     object { legal_name, address, contact, verbis_no }                         site
tr.efatura.inbound          reserved (post-1.0)
```

Core settings the pack fills through presets (names per backend §4.1 / §4.14): `site.settings.late_fee_policy` (rate default and ceiling, grace, pro-rating), `site.settings.governance.{quorum, thresholds, vote_weight, schedule}`, `site.settings.transfer_rule`. The merchant-of-record setting is core (`payments.merchant_of_record`, site or organization) and is read by every provider.

### 6.4 Legal accuracy process

- `packs/tr/LEGAL.md`: every preset value cites the article, the date of the law text consulted, known ambiguities, and which settings the yönetim planı may override — plus the disclaimer and the tax-and-employer page.
- Scenario tests read like case law ("24 units, 18 owners present holding 55 % of shares, first call → quorum met") and are the documentation of the presets.
- The `(verify)` list in §6.2 is one list with overview §11 and governance.md: the legal maintainer checks it **before 0.2 encodes the core defaults** and signs off again when the values move into the pack in 0.4.

### 6.5 Sequencing

| Step | Content | Release |
|---|---|---|
| **TR-0** | Turkish rules as core settings and templates; Netgsm in the core gateway; TCKN in core | 0.1–0.3 (not pack work) |
| **TR-1** | Move pack-shaped content out of the core: presets with scenario tests, templates, chart names, address/reference data, validators, KVKK texts, `LEGAL.md`, catalogs | 0.4 |
| **TR-2** | iyzico on the settlement model; two bank importers; reference-code scheme; İleti Merkezi | 0.4 |
| **TR-3** | Inbound e-Fatura intake; open-banking statement feeds; işletme projesi on budget-driven dues; PayTR and more banks by contribution | post-1.0 |

---

## 7. Generality check: two paper packs

Before `sdk/` v1.0 freezes at project release 1.0, map two other jurisdictions onto the extension points **on paper**, as a one-day exercise: Germany (WEG — Eigentümerversammlung quorum since the 2020 reform, Hausgeld / Wirtschaftsplan, Jahresabrechnung, SEPA mandates) and the Netherlands (VvE — reservefonds obligation, stemverhoudingen per splitsingsakte, iDEAL). Each yields a one-page table "requirement → extension point or gap". Gaps are fixed in the SDK before the freeze rather than after.

---

## 8. Milestones

Pack milestones are re-based on core releases. SDK runtime work (module, registry, enablement, `sdktest`) is counted in the backend's 0.4 estimate; the ≈ **8 engineer-weeks** below are the pack work of overview §8.

| # | Release | Deliverables | Exit criteria | Weeks |
|---|---|---|---|---|
| **P0** | 0.4 | `sdk/` module with the §2 interfaces and optional hooks; registry with namespacing and per-org enablement; migrations/RLS helper; `sdktest`; `packs/example`; `tools/newpack`; the two conformance suites | `packs/example` passes `sdktest` and both suites in CI; a `-tags nopacks` build boots | (backend) + 0.5 |
| **P1** | 0.4 | TR-1: presets with scenario tests, templates, chart names, reference data, validators, KVKK texts, `LEGAL.md` with the tax-and-employer page, catalogs; the core's TR-specific defaults removed behind `country_code` | Golden site renders every template; scenario tests signed off by the TR legal maintainer; a site without the pack still configures the same rules by hand | 3.5 |
| **P2** | 0.4 | TR-2: iyzico (checkout, webhooks, refunds, settlement accounting with clearing account), two bank importers from anonymized samples, reference-code scheme, İleti Merkezi | Conformance suites and recorded provider tests green; a resident pays by card from web and mobile and the net settles; two real statement files import and match | 4 |
| **P3** | 1.0 | Authoring guide, `tools/obkbuild`, pack index page, paper packs DE/NL, SDK v1.0 freeze, lawyer's review of the pack-linking exception | A contributor builds a server with an external sample pack following only the docs | (1.0 budget) |
| **P4** | post-1.0 | TR-3 (inbound e-Fatura, open banking, budget-driven dues / işletme projesi), second country pack with a community maintainer, WebAssembly sandbox ADR | — | — |

---

## 9. Risks and open questions

| Risk / question | Mitigation / decision needed |
|---|---|
| Legal presets encoded from unverified claims | `(verify)` list in §6.2; presets are site-editable; `LEGAL.md` with citations and disclaimer; legal maintainer signs off before 0.2 (core) and 0.4 (pack) |
| Which two banks first | Needs anonymized sample statement files from two banks (money.md open point); importers are table-driven so a third bank is a fixture, not code |
| Bank export formats change without notice | Fixture-driven detection; failures surface as "unrecognized format" with a manual, privacy-aware "send sample to maintainers" path |
| Merchant of record for card payments: site or management company | Owner decision before P2 (money.md open point); affects client-money accounting and which iyzico accounts exist. Turkish bank "kurum tahsilat" services remain a possible later feed |
| Pack-linking exception has not been reviewed by a lawyer | Review before the first outside contribution (overview §10); until then private packs are an intent, not a promise |
| SDK freeze too early (gaps found by a second country) | Paper packs DE/NL before the 1.0 freeze; optional-interface evolution rule keeps additions non-breaking |
| `sdktest` lives inside `server/internal`, unreachable from external modules | Acceptable for 0.4 (official packs only); a binary-driving mode is a 1.0 item |
| KVKK data residency for a hosted service | Operate a Turkish hosted region in Turkey or document the transfer basis **(verify)**; self-hosting sidesteps it |
| Packs that need client code | Not before 1.0; the client plans reserve an enabled-packs flag only |
