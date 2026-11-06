# OpenBekci — Packs Plan (SDK, authoring, Turkish pack)

| | |
|---|---|
| Status | Draft v0.1 |
| Date | 2026-11-06 |
| Parent | [00-overview.md](00-overview.md) §6.7. Runtime and SDK surface in [01-backend.md](01-backend.md) §10; pack UI in [02-web.md](02-web.md) §12 and [03-mobile.md](03-mobile.md) §10 |

---

## 1. Goals and non-goals

**Goals**

- A pack SDK that lets a country (or a vendor) add law, integrations, documents and reference data **without touching the core**, and without the core knowing the pack exists.
- The Turkish pack (`packs/tr`) as the first complete pack and the proof that the extension points are the right ones.
- A pack authoring experience good enough that a local developer in another country can produce a usable pack from the docs and the `example` pack in a few weeks.
- Clear legal and governance rules: packs encode law; they must cite sources, name maintainers and carry a disclaimer.

**Non-goals (v1)**

- Runtime-loaded or untrusted plugins (WebAssembly sandbox is a post-1.0 ADR).
- A marketplace. Packs are code in repositories; the docs list them.
- Packs that replace core behaviour wholesale (no "override the ledger"). Packs add implementations behind interfaces the core calls.

---

## 2. What a pack is

A pack is a Go module that implements `sdk.Pack`, is compiled into the server binary, and is **enabled per organization**. It may contribute implementations for the extension points below and nothing else. A pack may also ship data for the clients (settings schemas, consent texts, labels) but no client code in v1.

| Extension point | Interface (in `pkg/sdk`) | Core default | Called by |
|---|---|---|---|
| Identity provider | `IdentityProvider` | password, magic link, passkeys, generic OIDC | identity module (login, linking) |
| Payment provider | `PaymentProvider` | none (manual payments only) | payments module (checkout, webhooks, status) |
| Notification channel | `NotificationChannel` | in-app, email, push | notifications dispatcher |
| Statement importer | `StatementImporter` | generic CSV (configurable columns) | payments import |
| Legal rules | `LegalRules` | generic quorum/weights/thresholds; simple % late fee | meetings, billing |
| Allocation policy | `AllocationPolicy` | equal, by share, by area, by occupants, fixed table | billing |
| Late fee policy | `LateFeePolicy` | simple monthly % after grace | billing job |
| Document template | `DocumentTemplate` | generic statement, receipt, minutes, decision list | documents render job |
| Chart of accounts | `ChartOfAccounts` | generic chart | site creation |
| Address format | `AddressFormat` | generic (lines, locality, region, postal code, country) | tenancy, clients |
| Validators | `Validator` (national id, tax id, IBAN variants, plate formats) | IBAN, E.164 phone, email | people, vendors, forms |
| Reference data | `ReferenceData` (admin areas, banks, holidays) | ISO countries/currencies | address forms, scheduling, importers |
| Consent texts | `ConsentTexts` | generic privacy notice | privacy module, clients |
| Settings | `SettingsSchema` (JSON Schema + `x-ui`) | — | settings module, schema-driven forms |
| Migrations | goose `fs.FS`, tables `pack_<id>_*` | — | `migrate` |
| Routes | `RouteGroup` under `/v1/orgs/{org}/packs/<id>/` | — | HTTP layer |
| Jobs | `JobDefinition` (River) | — | worker |
| Event subscribers | `Subscribe(type, handler)` | — | outbox dispatcher |
| Invoice issuer | `InvoiceIssuer` (post-1.0; interface reserved) | none | billing (management fee invoices, vendor e-invoice intake) |

`Validator`, `ReferenceData` and the reserved `InvoiceIssuer` are additions this plan makes to the backend's §10.1 list; the backend plan is updated to match.

---

## 3. SDK design

### 3.1 Principles

- **Narrow, typed, versioned.** Every interface is small, documented with the exact call sites in the core, and covered by a conformance test the pack can run.
- **Namespaced everything.** Routes, settings keys, job kinds, tables, template ids, event handler names, i18n keys — all prefixed with the pack id. The registry rejects collisions at startup, so a bad pack cannot boot the server.
- **Tenant-safe by construction.** `Services.DB()` only hands out tenant transactions (RLS applies to `pack_*` tables too — the migration helper adds the policy automatically). There is no raw pool.
- **No reach into core tables.** Packs read through `UnitReader`, `PeopleReader`, … and write through `LedgerPoster`, `PaymentRecorder`, `Notifier`, `FileStore`. The core's invariants apply to pack writes exactly as to its own.
- **Fail closed.** A pack that panics in a handler is recovered and the request fails with `pack_error`; a pack that fails `Register` prevents startup with a named error. Jobs and event handlers from a disabled pack are skipped, never errored.
- **Everything localizable.** Pack-provided labels, template strings and consent texts are catalogs keyed per locale with the core's fallback chain.

### 3.2 Lifecycle

| Phase | What happens |
|---|---|
| Compile | `cmd/openbekci/packs_all.go` imports official packs; `init()` registers a `sdk.Pack` constructor in a global list. A `-tags nopacks` build has none |
| Startup | Registry calls `Manifest()`, checks `MinSDK`, calls `Register(ctx, registry)`, validates namespacing, freezes |
| `migrate` | Core migrations, then each pack's migrations (own goose table `pack_<id>_goose_version`), in pack id order. Pack migrations run even if no org has the pack enabled — schema is global, data is per org |
| Enable (per org) | Admin enables from settings; settings validated against the schema; `OnEnable(ctx, org)` hook (e.g. seed a localized chart of accounts if the site has none, register default templates) |
| Running | Core calls the pack's implementations only for orgs where it's enabled; pack routes 404 otherwise |
| Disable | `OnDisable(ctx, org)`; data retained; implementations stop being selected (e.g. late-fee policy reverts to the core default — the admin is warned in the UI) |
| Purge | `openbekci admin packs purge --org … --pack …` deletes the pack's rows for that org after confirmation; no automatic purge |
| Remove from build | Only by rebuilding without the pack; tables stay until an operator drops them (documented) |

### 3.3 Settings and secrets

- `SettingsSchema` = JSON Schema 2020-12 + `x-ui` (order, groups, widgets, help keys) + `x-secret: true` on fields stored encrypted and never returned (write-only; the UI shows "set"/"not set").
- Scopes: `org` (default) and `site` (e.g. a bank account per site). The pack reads with `Settings().Get(ctx, scope, key)`.
- Validation on write by the core (ajv-equivalent in Go, `santhosh-tekuri/jsonschema`) plus an optional `ValidateSettings(ctx, values) error` hook for cross-field or live checks (e.g. test provider credentials).

### 3.4 Client-side contributions (data only)

| Contribution | Delivered how |
|---|---|
| Sign-in button for an identity provider | Discovery document lists `auth_methods` with `label` i18n key and icon id |
| Settings form | Schema endpoint; RJSF (web) renders; mobile doesn't show admin settings |
| Consent screens | `GET …/privacy/consents` returns Markdown per locale with version |
| Labels for pack-specific enums (e.g. a provider's payment statuses) | Pack catalogs merged into the API's `codes` catalog endpoint; clients map unknown codes to the pack's label or fall back |
| Address fields | `AddressFormat` → field list, labels, validation regexes, reference-data endpoints for cascading selects (il → ilçe → mahalle) |
| Document downloads | Files, via the documents module |

If a pack needs real client code (NFC, a native SDK), it ships separate web/mobile packages behind the enabled-packs flag — designed in the respective plans, not built in v1.

### 3.5 Versioning and compatibility

- `pkg/sdk` is semver'd independently; **v1.0 is frozen at project milestone M6**. Until then, minor versions may break with migration notes.
- `Manifest.MinSDK` is checked at startup; the server also exposes its SDK version in `/v1/instance`.
- Interface evolution rule: add methods only via new optional interfaces (`type PaymentRefunder interface{…}`, checked with type assertion), never by widening an existing one. Breaking changes batch into `sdk/v2`.
- Packs declare `Manifest.Countries` (ISO 3166-1) for the UI's "suggested packs" when an org picks a country.

### 3.6 Testing support

- `pkg/sdk/testkit`: boots Postgres (testcontainers), runs core + pack migrations, creates an org/site with the golden 24-unit fixture, enables the pack, and exposes typed helpers (`PostCharge`, `RecordPayment`, `RunJob`, `CallRoute`, `Emit(event)`).
- **Conformance suites** per interface: `testkit.ConformPaymentProvider(t, p)`, `ConformStatementImporter(t, imp, samples)`, `ConformLegalRules(t, rules, scenarios)`, `ConformDocumentTemplate(t, tpl)`. They assert idempotency, error mapping, locale handling and namespacing.
- Provider HTTP interactions are recorded with `go-vcr` fixtures; live tests run only with credentials in CI secrets, nightly.

---

## 4. Authoring guide (what `docs/packs/` will contain)

1. **Start from `packs/example`** — a complete, tiny pack (one validator, one template, one setting, one route, one job, one event subscriber, migrations) with tests; copied by `tools/newpack <id>`.
2. **Manifest and registration** — id, version, countries, MinSDK; what each `Register*` does and where the core calls it.
3. **Data** — naming (`pack_<id>_*`), RLS helper, migrations, reading core data through readers, writing through posters.
4. **Settings** — schema conventions, secrets, scopes, validation hook.
5. **Localization** — catalogs, fallback chain, pluralization, document templates per locale.
6. **Legal content** — the `LEGAL.md` requirement (sources with article numbers and dates, effective-from, maintainer, disclaimer), how `LegalRules` scenarios are expressed as table tests that double as documentation.
7. **Testing** — testkit, conformance suites, recorded HTTP.
8. **Building a server with your pack** — `tools/obkbuild` (an xcaddy-style helper: `obkbuild --with github.com/acme/openbekci-pack-de@v0.3.0` generates a `main` with the import and runs `go build`), or a one-file fork of `packs_all.go`.
9. **Publishing** — repo layout, tagging, listing in the docs' pack index, CODEOWNERS and review expectations for official packs.

---

## 5. Governance for packs

- **Official packs** live in `packs/` in the monorepo, are built into the released binary, follow the project's CI, and each has at least one named maintainer with domain knowledge in `CODEOWNERS`. Legal-rule changes require review by a maintainer of that pack plus one core maintainer.
- **Community packs** live in their own repositories, are listed in `docs/packs/index.md` after a light review (builds with `obkbuild`, passes conformance suites, has `LEGAL.md`), and are not shipped in official binaries.
- **Disclaimer**: every pack's `LEGAL.md` and the enable screen state that the rules are an implementation of the maintainers' reading of the law at a given date and are not legal advice; organizations remain responsible for compliance.
- **Licensing**: `pkg/sdk` is Apache-2.0 so private packs are possible; official packs are AGPL-3.0 like the core.

---

## 6. The Turkish pack (`packs/tr`)

### 6.1 Scope by area

| Area | Content | Extension points |
|---|---|---|
| **Legal rules** (Kat Mülkiyeti Kanunu No. 634 and the site's yönetim planı) | Quorum for the first call (majority by **both** share (arsa payı) and owner count) and the second call (no quorum, decisions by majority of attendees); decision thresholds by topic as the law prescribes (simple majority of attendees, majority of owners+shares, four-fifths, unanimity) encoded as a topic → threshold table with article citations; voting weight = one vote per unit with share-based ties as configured; proxy limits per the law; late-payment compensation (gecikme tazminatı) at the statutory monthly rate with grace handling; annual ordinary assembly timing reminders (default January unless the yönetim planı says otherwise); roles yönetici / denetçi mapped onto core roles | `LegalRules`, `LateFeePolicy`, reminders via jobs |
| **Site structure** | Toplu yapı (multi-block) provisions map onto Site → Building; block-level and site-level assemblies | core already supports; pack provides defaults and labels |
| **Documents** | Karar defteri page, toplantı tutanağı, hazirun cetveli (attendance list), vekaletname (proxy form), aidat makbuzu (receipt), hesap ekstresi (statement), ihtarname (payment warning letter), **işletme projesi** (annual operating budget per art. 37 — generated from charge plans + expense budget) | `DocumentTemplate`; işletme projesi needs a core *budget* entity (planned post-1.0) — until then it's produced from charge plans and a pack-side expense budget table |
| **Chart of accounts** | Simplified chart in Turkish, aligned with Tek Düzen naming where useful | `ChartOfAccounts` |
| **Address & reference data** | İl / ilçe / mahalle lists (static, versioned), optional UAVT address code field, postal codes; Turkish public holidays for scheduling and due-date rolling | `AddressFormat`, `ReferenceData` |
| **Validators** | TCKN checksum, vergi numarası, TR IBAN specifics, vehicle plate format | `Validator` |
| **Identity** | TCKN captured and stored encrypted in `pack_tr_person_identity` with consent; **e-Devlet sign-in** behind configuration — integration with e-Devlet Kapısı requires an agreement with the Digital Transformation Office, so the pack ships the OIDC/SAML-style flow and a "pending agreement" state; until then TCKN format validation and manual verification by the manager | `IdentityProvider` |
| **Payments** | iyzico first (hosted checkout + webhooks + refunds), PayTR second; bank transfer instructions with per-site IBAN and a reference-code scheme (`<unit>-<period>`) to ease matching | `PaymentProvider` |
| **Bank statements** | Importers for the CSV/XLSX exports of the major banks (Ziraat, İş, Garanti BBVA, Yapı Kredi, Akbank — start with two, add by contribution); matching heuristics on reference codes, names and TCKN fragments | `StatementImporter`, matching rules |
| **SMS** | Netgsm and İleti Merkezi channels; **İYS** (İleti Yönetim Sistemi) note: operational notices are exempt from commercial-message consent, marketing is out of scope; the channel tags every message as operational | `NotificationChannel` |
| **Privacy (KVKK)** | Aydınlatma metni and açık rıza texts per processing purpose (identity, contact, financial, camera/visitor logs), versioned; VERBİS registration guidance in docs; data-residency note recommending Turkey-hosted deployments for Turkish organizations | `ConsentTexts`, docs |
| **E-invoicing** (phase 2) | Outbound e-Fatura/e-Arşiv for management-company fees through a GİB-approved integrator adapter; inbound e-Fatura intake to create vendor bills | `InvoiceIssuer` (post-1.0) |
| **Localization** | `tr` catalogs for everything the pack adds; Turkish number/date conventions come from the core's locale support | catalogs |

### 6.2 Settings (schema sketch)

```
tr.legal.late_fee_rate_pct_per_month   number, default statutory rate, min 0, max statutory rate
tr.legal.grace_days                    integer, default 0
tr.legal.first_meeting_month           integer 1–12, default 1
tr.identity.edevelet                   object { enabled, client_id, client_secret (x-secret), redirect_url }
tr.payments.iyzico                     object { enabled, api_key (x-secret), secret_key (x-secret), sandbox }
tr.sms.provider                        enum [netgsm, iletimerkezi], credentials (x-secret), sender_id
tr.bank.<site>.iban, account_holder    site scope
tr.kvkk.data_controller                object { legal_name, address, contact, verbis_no }
```

### 6.3 Sequencing

| Phase | Content | Depends on |
|---|---|---|
| **TR-1** Legal + documents + data | LegalRules with scenario tests, late fee, templates (statement, receipt, minutes, attendance, proxy, decision book, warning letter), chart, address/reference data, validators, consent texts, catalogs | SDK v0 (B5), meetings (B4) |
| **TR-2** Money integrations | iyzico provider, two bank importers, matching heuristics, reference-code scheme | payments (B2), SDK conformance suites |
| **TR-3** Communications | Netgsm + İleti Merkezi channels, phone OTP enabled through them (closes the backend's phone-first question), SMS invitations | notifications (B3) |
| **TR-4** Identity | TCKN storage + consent, e-Devlet flow (dark until agreement), manual verification | identity (B1), agreement with the Digital Transformation Office |
| **TR-5** (post-1.0) | e-Fatura/e-Arşiv, inbound e-invoice bills, işletme projesi on the core budget entity, open-banking statement feeds | `InvoiceIssuer`, core budget |

### 6.4 Legal accuracy process

- `packs/tr/LEGAL.md`: every rule cites the article, the date of the law text consulted, and known ambiguities; the yönetim planı can override defaults where the law allows, and the settings say which.
- Scenario tests read like case law: "24 units, 18 owners present holding 55 % of shares, first call → quorum met"; they are the documentation.
- A reviewer with property-management or legal background is named as pack maintainer before TR-1 ships.

---

## 7. Generality check: two "paper packs"

Before the SDK freezes (M6), map two other jurisdictions onto the extension points **on paper** (no code) to catch gaps cheaply. Suggested: Germany (WEG — Eigentümerversammlung quorum changes since the 2020 reform, Hausgeld/Wirtschaftsplan, Jahresabrechnung, SEPA mandates, DATEV-style exports) and the Netherlands (VvE — reservefonds obligations, stemverhoudingen per splitsingsakte, iDEAL). Each produces a one-page table "requirement → extension point or gap". Gaps found this way are fixed in the SDK before v1.0 rather than after.

---

## 8. Milestones

| # | Name | Deliverables | Exit criteria |
|---|---|---|---|
| **P0** | SDK v0 (with B5) | `pkg/sdk` interfaces incl. `Validator`, `ReferenceData`, reserved `InvoiceIssuer`; registry with namespacing and per-org enablement; migrations/RLS helper; testkit; `packs/example`; `tools/newpack` | `packs/example` passes testkit and conformance suites in CI |
| **P1** | TR-1 (M6) | Legal rules, late fee, document templates, chart, address/reference data, validators, consent texts, `LEGAL.md`, catalogs | Scenario tests reviewed by the TR legal maintainer; documents render in the golden site |
| **P2** | TR-2 + TR-3 (M6) | iyzico, two bank importers, SMS channels + OTP, SMS invitations | Conformance suites + recorded provider tests green; end-to-end pay and match in web and mobile |
| **P3** | TR-4 + authoring (M6→M7) | TCKN storage/consent, e-Devlet flow (dark), `tools/obkbuild`, authoring guide, pack index page, paper packs DE/NL, SDK v1.0 freeze | A contributor builds a server with an external sample pack following only the docs |
| **P4** | Post-1.0 | TR-5 (e-invoicing, budget/işletme projesi, open banking), second real country pack with a community maintainer, WebAssembly sandbox ADR | — |

---

## 9. Risks and open questions

| Risk / question | Mitigation / decision needed |
|---|---|
| Legal rules encoded wrongly | `LEGAL.md` with citations, scenario tests as documentation, named domain maintainer, disclaimer, settings to override where the law allows |
| e-Devlet access requires an institutional agreement | Ship the flow dark; TCKN validation + manual verification meanwhile. Owner decision whether to pursue the agreement for the hosted service |
| Bank export formats change without notice | Importers are table-driven with sample fixtures; failures surface as "unrecognized format" with a one-click "send sample to maintainers" (manual, privacy-aware) |
| SDK freeze too early (gaps discovered by a second country) | Paper packs DE/NL before freeze; optional-interface evolution rule keeps additions non-breaking |
| Phone OTP depends on the TR SMS channel | TR-3 is sequenced early in M6; phone-first onboarding via invitation links covers the interim (backend §21, mobile §18) |
| `işletme projesi` needs a budget entity the core lacks | Pack-side expense budget table for v1; promote to core post-1.0 (several countries need annual budgets — it is generic) |
| KVKK data residency for the hosted service | Operate the Turkish hosted region in Turkey or document the transfer basis; self-hosting sidesteps it |
| Packs that need client code | Not v1; feature-flag mechanism reserved in both client plans |
