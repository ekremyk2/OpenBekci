# OpenBekci — Plan Review (v0.1 of the plan set)

| | |
|---|---|
| Status | **Applied.** Review of plans 00–05 as committed in `bbb87e8`; plans revised to v0.2 on 2026-11-06/07 against this review and `docs/decisions/`. Kept for the reasoning; the findings tables are no longer a to-do list |
| Date | 2026-11-06 |
| Method | Three independent reviews (internal consistency; domain and Turkish practice; architecture and delivery) plus the author's own pass. Each reviewer read all six documents. Findings below carry the author's judgment — where a reviewer's claim could not be verified it is marked **(verify)**. |

---

## 1. Verdict

The architecture is sound in shape — Go monolith, Postgres-only, contract-first, a real ledger — and the component plans are consistent with each other at the level of principles. Below that level the plan set has three kinds of problems:

1. **The money and governance model is too simple for the first market.** Receivables don't distinguish owner from tenant; there is one share per unit and one allocation rule per plan; budget / işletme projesi, fiscal-year close, opening balances and collections are missing or deferred. These are not polish; a Turkish management company would reject a demo without them.
2. **Several mechanisms are specified in a way that doesn't work as written** — the RLS policy and tenant-context code, erasure against append-only tables, late-fee generation, catch-up of periodic jobs, the relay enrollment check — and several are heavier than needed (JWT, outbox, balance cache, presigned uploads, monthly period state machine).
3. **The roadmap is 2× under-estimated and sequenced with real dependency bugs** (endpoints scheduled in B5 that the web app needs at M0; bank import at M2 vs B3), and the licensing split rests on a claim that is probably false for compiled-in packs and risky for the App Store.

None of this changes the stack choices. It changes the domain model, a dozen mechanisms, the first-release scope, and the license text — all cheaper to fix now than after B1.

---

## 2. Decisions the owner must make (blocking)

> **Decided 2026-11-06** — all five as recommended: D1 AGPL + pack exception / MPL-2.0 mobile / Apache SDK-spec-clients; D2 organization = access boundary, site owns the data and is portable; D3 0.1 web-only money-complete; D4 self-host first, SaaS-capable; D5 minimal SMS gateway in core with one provider. Recorded in [00-overview.md §2.2](00-overview.md#22-decided-on-2026-11-06-after-the-plan-review). The table below is kept for the reasoning.

| # | Decision | Why it blocks | Recommendation |
|---|---|---|---|
| D1 | **License** (currently "assumed AGPL core / Apache SDK") | A Go pack compiled into the AGPL binary is a combined work; AGPL §13 then applies to anyone running the pack-added server over a network. The plan's promise "private packs without AGPL obligations" is at best contested. Separately, AGPL and Apple's App Store terms are widely treated as incompatible; the usual fix (a copyright-holder exception) is impossible once DCO-only outside contributions exist. | Get a licensing opinion before the first external PR. Likely shape: AGPL-3.0 server + web **with an explicit pack-linking exception** (or accept and state that deployed packs are AGPL); **MPL-2.0 or Apache-2.0 for `mobile/`**; Apache-2.0 for `sdk/`, `api/`, clients. Decide CLA vs DCO with that in mind. |
| D2 | **Who is the tenant: the management company or the site?** | In Turkey the books belong to the site (kat malikleri kurulu); management companies are contractors replaced every 1–3 years. With org = company, a site that changes company has no path for its history, residents and board to move, and the company can remove the site's auditor. | Keep org as the access/billing boundary **but make site the data-owning unit**: site export/import between orgs preserving ids, board/auditor memberships site-owned, "offboard site" workflow — designed in B1, not bolted on. (Alternative, site = tenant + "portfolio" grouping, costs cross-org aggregation under RLS.) |
| D3 | **First-release scope** | The plan's 1.0 is ≈ 117 engineer-weeks at face value, realistically 220–260 (4–5 engineer-years). With 1–2 engineers that is 2–4 years with nothing shippable in between. | Adopt the **0.1 "a Turkish site runs on it"** cut in §9 (≈ 45–50 engineer-weeks, web-only), then iterate. |
| D4 | **Deployment model** (still assumed "self-host first, SaaS-capable") | D2 and the relay depend on it. | Confirm. |
| D5 | **Phone-first onboarding** | Core has no SMS channel until the TR pack (M6); older Turkish residents don't use email. | Ship a minimal SMS gateway interface with one provider (Netgsm) in core at M1, or pull the TR SMS channel forward as the first thin pack. |

---

## 3. Money model

The highest-impact theme. All three reviews and the author's pass converge here.

| Sev | Finding | Judgment | Action (plan change) |
|---|---|---|---|
| HIGH | **No debtor dimension on receivables.** Charges are owed by a *unit*; in practice the tenant pays regular aidat and the owner pays demirbaş/special assessments, the owner remains debtor of record, icra needs a *person* debtor, and tenants must not see the owner's capital debt. (01 §4.3/§4.5/§9.8) | Agree. Every Turkish competitor tags each charge by debtor. | Add `debtor_kind` (owner / tenant / unit) to `charge_plan` and `charge`; snapshot `debtor_person_id` from the occupancy in force on the due date; sub-ledger key `(unit_id, debtor_kind)`; statements per unit *and* per person; portal visibility derived from it. |
| HIGH | **One share per unit, one policy per plan, no cost groups, no block level.** KMK art. 20 splits caretaker/heating-staff costs equally and the rest by arsa payı; toplu yapı allocates block costs to the block's units and site costs to all; shops are often exempt from lift costs. (01 §4.1/§4.5/§9.6) | Agree. `unit_filter jsonb` is not a real hook. | Replace single share with **allocation keys**: named per-site key sets (`arsa_payi`, `block_share`, `heated_area`, `headcount`, custom) with a value per unit; a plan/budget line names its key and scope (site / building / cost group); unit tags for cost groups. `by_share` becomes "key = arsa_payi". |
| HIGH | **No consumption-based allocation** (central heating 70/30 consumption/area under the ısı paylaşım regulation, water submeters) **(verify current thresholds)**. | Agree; common in Turkish buildings. | v1: per-unit amount upload (`fixed_table`) from the metering company's CSV. Later: `meter` + `meter_reading` and a composite policy. |
| HIGH | **Budget / işletme projesi and fiscal-year close deferred post-1.0.** KMK art. 37 makes the işletme projesi mandatory, notified with a 7-day objection window, and once final an İİK 68/1 enforcement document — it is the *source* of dues and the basis of icra. DE Wirtschaftsplan/Jahresabrechnung and NL begroting/jaarrekening are the same object. The plan generates it *from* charge plans; reality is the reverse. (01 §9.10, 04 §6.1/§9) | Agree; three reviews independently. | Core `budget` (fiscal year, lines by expense account × allocation key × fund, status draft/notified/final/revised, `decision_id`); "create charge plans from budget"; budget-vs-actual; **fiscal year close**: surplus/deficit, carry-over or per-unit settlement charges/credits, closing entries, "accounts approved (ibra)" link. B2, not post-1.0. |
| HIGH | **No opening balances.** Every adopting site has per-unit debts (principal and late fee), bank/cash balances and open bills on day one. (01 §8, 02 §9.1) | Agree. | `opening_balance` posting type against an opening-equity system account; CSV import with dry-run; locked after first lock date. B2. |
| HIGH | **Late-fee semantics undefined where money is lost.** A daily job creating fee charges floods the ledger, can't handle backdated payments (transfer received the 28th, recorded the 3rd), partial payments, cancelled charges, waivers, or "what do I owe if I pay today". (01 §9.7/§4.5/§11) | Agree; two reviews converge on the same fix. | Late fee = **accrual computed on demand** (statement, payoff, payment time) and **crystallized** monthly or at settlement into one fee charge per unit-period; the job is an idempotent *reconciler* (expected-to-date minus posted = delta, may post reversals); bulk `waive` with reason; per-unit "in legal collection — freeze" flag; fee on principal only; `LateFeePolicy` receives the allocation order. |
| HIGH | **Periodic jobs don't catch up.** River periodic jobs don't backfill missed runs; "02:00 site-local" isn't one cron; a box off over the 1st never generates that month. (01 §11) | Agree. | Hourly job generating *all missing* charges up to today, idempotent on `(plan, unit, period)`. Same for fees and reminders. |
| HIGH | **Collections workflow missing** (reminder stages → ihtarname → icra), `collection_case` (lawyer, file no., costs charged back), freezing reminders/fees for units in icra, icra debt breakdown export. | Agree; weekly pain for managers. | Core generic dunning stages + `collection_case`; TR pack supplies templates and breakdown format. |
| HIGH | **Reversal dating and partially-paid cancellation unspecified.** Corrections to closed periods, late vendor bills, cancelling a charge that has allocations. (01 §9.3/§9.4) | Agree. | Separate `document_date` from `entry_date`; reversals dated today; cancellation un-allocates and re-homes the payment. |
| HIGH | **Cash-basis reporting missing.** Assemblies and denetçi read gelir-gider on a cash basis; the accrual P&L is not what is presented. Also missing: debtor list, collector reconciliation (tahsilat by day/method/collector), kasa/işletme defteri print (date-ordered, KMK art. 36), bank reconciliation, block-level P&L, fund balances, settlement sheet. (01 §9.10) | Agree. | Add to reports; `journal_line.building_id` (or cost centre) for block P&L. |
| MED-HIGH | **No fund dimension** (operating vs reserve/demirbaş; mandatory reservefonds in NL; US operating/reserve). | Agree. | `fund_id` on `journal_line` (operating / reserve / custom); plans and bills carry the fund; fund-balance report. |
| MED-HIGH | **Payment-provider settlement not modelled.** iyzico/PayTR deduct commission and settle net, often to the *company's* merchant account; refunds; webhook idempotency by `(org, provider, provider_txn_id)`. Also the merchant-of-record question (a site rarely has the identity a PSP requires). (01 §4.6, 04 §6.1) | Agree. | `PaymentProvider` defined around gross/net/fee, a provider clearing account, refunds and idempotent webhooks *before* iyzico. Decide merchant model; note Turkish bank "kurum tahsilat" services as the common channel. |
| MED | **Single amount per plan.** Aidat changes once or twice a year by decision. | Agree. | Effective-dated `charge_plan_rate` linked to the `decision`. |
| MED | **Missing charge kinds**: special assessment, reserve contribution, utility re-bill, legal cost, deposit (liability), settlement. | Agree. | Extend enum; deposits post to a liability. |
| MED | **Missing simple money moves**: asset→asset transfer (cash deposit), outgoing refund, expense without a vendor bill (fiş), payment split across an owner's several units, unallocated credit's home. | Agree. | `transfer`, `refund`, light `expense`; receivable lines per unit from allocation; person-level credit account. |
| MED | **Allocation basis not snapshotted.** Disputes months later need "why 3.2 %". | Agree. | `allocation_basis jsonb` per run; shown in statement line detail. |
| MED | **Gap-free receipt numbering** (aidat makbuzu) absent; `decision.sequence_no` exists, receipts don't. | Agree. | Per-site per-year sequence table with row locking; immutable `payment.receipt_no`. |
| MED | **Payment allocation order hard-coded** (oldest-first). TBK art. 100–101 orders costs/interest before principal when in delay **(verify)**; other countries differ; managers need "pay this charge". | Agree. | `PaymentAllocationOrder` policy (core defaults; pack override), manual override always. |
| MED | **Chart of accounts lacks**: unit prepayment as liability (not negative asset), deposits held, reserve fund, severance provision (kıdem tazminatı), opening-balance equity, prior-year surplus, other income (common-area rent, interest), withholding receivable, bank fees, legal costs, multiple cash boxes. | Agree. | Extend default chart; packs localize names. |
| MED | **Two sub-ledgers can drift** (`charge_payment` allocations vs `journal_line` by unit). | Agree. | Named invariant tests in the golden year; `rebuild-charge-status` next to `rebuild-balances`. |
| MED | **Monthly period open/closed state machine** is accountant workflow managers won't do. | Agree. | Single site-level **lock date**; periods stay a reporting grouping; add `fiscal_year` with approval status. |
| LOW-MED | `account_balance` cache = hot-row contention; a 500-unit site's SUM over an indexed `journal_line` is sub-millisecond. | Agree (author flagged too). | Cut for v1; keep the `rebuild-balances` idea as a report. |
| LOW | Bills lack tax breakdown (needed for commercial-unit VAT abroad and inbound e-Fatura later); payables lack credit notes, approval status, recurring bills, batch payments. | Agree, later. | `net/tax/gross` on bills; approval status with D-approvals (§4). |

The golden fixture (01 §9.11) must grow to validate this model: a two-block toplu yapı with a shop, a co-owned unit, a tenant change and an owner sale mid-year, central-heating re-billing, an icra case, and a year-end settlement.

---

## 4. Tenancy, roles and governance

| Sev | Finding | Judgment | Action |
|---|---|---|---|
| HIGH | Org = tenant makes a site non-portable; board/auditor memberships are company-editable. | See D2. | Site export/import + site-owned governance memberships in B1. |
| HIGH | **Two sources of rights**: `owner`/`resident` are both membership roles and occupancy kinds; nothing says which wins or what happens at `valid_to`. Will produce access bugs and KVKK leaks. (01 §4.3/§7) | Agree (author flagged too). | Portal rights derive **only** from occupancy (implicit unit scope); `membership` is for staff/board/auditor/accountant roles. Former occupants keep read access to their own period's statements (setting). |
| HIGH | **Unit transfer / move-out is only `valid_to`.** Needs a handover: as-of date, settlement statement, pro-rata choice, debt stays with person (TR, DE) vs follows unit (NL), "borcu yoktur" certificate. | Agree. | `transfer`/`move_out` use case with a `LegalRules` default + manual, audited override; certificate template in core. |
| MED | **No approval workflow** (board approves budget, bills above threshold, legal action, waivers; dual control on outgoing payments). Web §2 promises "approvals"; the domain has none. | Agree. | `approval` entity (target, policy by amount/kind, approvers, decision link); permissions `bills.approve`, `payments.approve`, `legal.approve`. |
| MED | **Meetings lack the legal paperwork**: notice dispatch record with proof (KMK art. 29: ≥ 15 days, registered letter or signature, agenda, second date), second call 7–15 days after the first, dissent notes (art. 32), notification of absentees, eligibility snapshot (owner of record + share at meeting date), co-owner representative, decision status (annulled). | Agree; the current `meeting` is a scheduling object. | Add `meeting_notice`, `decision.dissents[]`, `decision.notified_at`, `meeting.eligibility_snapshot`, `unit_vote_representative`; `LegalRules.ValidateSchedule`. |
| MED | **Vote weighting as written is wrong for KMK**: "one vote per unit with share-based ties" — KMK art. 31 is one vote per unit, max one-third per person, co-owners vote through one representative; shares enter only through quorum and the "sayı ve arsa payı" thresholds. Proxy caps: ≤ 5 % of votes, ≤ 2 proxies in buildings ≤ 40 units **(verify article mapping)**. (04 §6.1) | Agree; rewrite. | Weight = 1 per unit, 1/3 cap, proxy caps, thresholds table by topic with citations. |
| MED | **Binding online ballots** have no clear basis in KMK **(verify)**. | Keep as non-binding polls unless the pack marks a vote binding. | Mobile/web "vote" features become "poll" unless `LegalRules` says binding. |
| MED | Board positions are elected mandates with terms; should link to the electing decision; expiry reminders. | Agree. | `membership.decision_id`; reminder job. |
| MED | **Owners' right to inspect books** vs owner sees only own statements; debtor list must never be public (KVKK Board decisions) **(verify)**. | Agree. | Site setting "financial transparency to owners" (aggregate gelir-gider + budget always; debtor list board-only by default). |
| MED | Auditor needs write rights for reports; guard endpoints must be data-minimized (name, unit, plate, expected visitors — never contacts/balances/TCKN). | Agree. | `audit.report` permission; explicit guard endpoints. |
| MED | Missing practical roles: collections officer, support agent, external technician as assignee; portfolio (cross-site) dashboard for companies. | Agree. | Add `collections`, `support` built-ins; `staff` linkable to a `vendor`; org-level read-only aggregation. |
| LOW-MED | Units: appurtenances (parking/storage as `parent_unit_id`), `legal_ref` (tapu bağımsız bölüm no), `common` type; `vacant` status should be derived from occupancies; temsilciler kurulu (delegate assembly) needs a `representative` role with unit-count weight. | Agree. | Schema additions; derive vacancy. |

---

## 5. Privacy, legal and security

| Sev | Finding | Judgment | Action |
|---|---|---|---|
| HIGH | **Erasure is impossible as designed.** `audit_event` stores before/after JSONB of every mutation, `notification.payload`, `bank_transaction.raw`, `payment.provider_payload` and `journal_line.person_id` carry PII, and `audit_event`/`journal_*`/`ballot` are append-only with no UPDATE grant. Anonymizing `person` leaves the data in a dozen tables. (01 §16.5/§4.14/§9.3) | Agree; the single most important correctness gap outside money. | Pick now: (a) per-person envelope encryption + crypto-shredding, or (b) a `SECURITY DEFINER` `privacy_erase()` owned by the migrator as the only path that rewrites PII columns in append-only tables, plus audit diffs storing field names + hashes for PII fields. Redact `x-secret` settings from audit diffs. Retention for raw provider/bank payloads, sessions, login attempts. |
| HIGH | **Secrets are state.** "Nothing else is state" (05 §6) is wrong: losing `ENCRYPTION_KEY` makes TOTP secrets, device tokens and TCKNs unrecoverable. | Agree. | Backup set includes `.env`/secret files; restore test restores them; "dump first, then files" ordering; server refuses placeholder/short keys at startup; compose README generates keys. |
| MED | **Formal notice proof.** Assembly calls, işletme projesi and decisions to absentees are legally notified; email/SMS/push aren't valid tebligat unless the yönetim planı allows **(verify)**. "Force channels" isn't enough. | Agree. | `formal_notice` record (method, date, proof file) + printable signature list. |
| MED | **KVKK 2024 transfer regime** (Law 7499: adequacy, standard contracts notified within 5 working days) **(verify details)**; hosted service needs a concrete basis; recommend TR-hosted region. | Note in docs. | Pack `LEGAL.md` + hosted-service policy. |
| MED | **Relay enrollment is an SSRF primitive** (relay fetches any posted `base_url`); the challenge is a permanent public value; no replay protection in the HMAC header; the relay is not stateless (enrollments, quotas). Also LAN/VPN instances can't enroll at all. | Agree on all; author had flagged reachability. | DNS-resolve and reject private ranges, HTTPS only, no redirects, size/time caps; ephemeral enrollment session; timestamp window + nonce in HMAC; manual enrollment token path; call it "no device registry", not stateless. |
| MED | **Relay is a shared point of failure/ban**; iOS has no UnifiedPush escape. | Accept and document. | `RELAY_ENROLLMENT=invite` default, per-server kill switch day one, app degrades to periodic fetch, relay log schema in the repo (provably content-free), relay sees tokens + `org_id` — say so. |
| MED | Provider webhooks must verify provider signatures, not just be rate-limited; magic-link/invitation sending needs abuse limits (SMTP reputation); reset enumeration. | Agree. | Add to backend §16. |
| LOW-MED | `BASE_URL` as the single origin binds passkey RP ID, cookie origin checks and OIDC callbacks; changing it invalidates passkeys; `TRUSTED_PROXIES` needs a compose default (Caddy's IP isn't stable). | Agree. | Document; `EXTRA_ORIGINS` for the Origin check; compose default trusts the compose network CIDR. |
| LOW | Kimlik Bildirme Kanunu resident-change reporting, kapıcı employer obligations (SGK, muhtasar), interest withholding, common-area rent income — the TR pack docs must say what the software does *not* do **(verify scope)**. | Agree. | "Tax and employer obligations" page in `packs/tr/LEGAL.md`. |

---

## 6. Platform mechanics (what doesn't work as written, and what is heavier than needed)

| Sev | Finding | Judgment | Action |
|---|---|---|---|
| HIGH | **RLS as written has three bugs.** (1) once a custom GUC has been SET LOCAL and the tx ends, `current_setting('app.org_id', true)` returns `''` on that session, and `''::uuid` throws on the next query from a reused pooled connection; (2) `SET LOCAL app.org_id = $1` doesn't take bind parameters; (3) the platform-admin "sentinel" can never satisfy `org_id = current_setting(...)`, so platform reads return nothing. Also `FORCE ROW LEVEL SECURITY` filters the owner, so backfill migrations run as `openbekci_migrator` are RLS-filtered. | Verified: all four are real Postgres behaviours. | `NULLIF(current_setting('app.org_id', true), '')::uuid` wrapped in `(SELECT …)` (InitPlan, evaluated once per query, not per row); `SELECT set_config('app.org_id', $1, true)`; a second DB role with `BYPASSRLS` and its own small pool for `platformadmin` and the cross-org `GET /v1/me`; grant `BYPASSRLS` to the migrator. |
| MED | **Pooler guidance is inverted** (author's error in 05 §3.4 / 01 §17). `SET LOCAL` inside an explicit transaction is *safe* under PgBouncer transaction mode (the tx is pinned). What breaks is River's `LISTEN/NOTIFY` (use `PollOnly`) and pgx's prepared-statement cache (PgBouncer ≥ 1.21 with `max_prepared_statements`, or simple/describe exec mode). | Agree; the current "startup check" tests the wrong thing. | Fix text; startup check for LISTEN support and exec mode instead. |
| MED | **Cross-tenant FK integrity isn't DB-enforced** (FK checks bypass RLS). RLS gives *org* isolation only; site-level scoping is application-only — the plan implies more. | Agree. | Composite FKs `(org_id, building_id) REFERENCES building(org_id, id)` on tenant-to-tenant FKs; state the boundary honestly. |
| MED | **JWT access tokens buy nothing** (single verifier, same DB) and break "revocation works uniformly" (15-min window); `SECRET_KEY` rotation logs everyone out only because of them. | Agree (author flagged too). | Opaque bearer access tokens (hash in `session`), rotating refresh tokens; one auth path. Update overview §6.3. |
| MED | **Outbox + `event_delivery` are redundant with River** — `InsertTx` in the business tx *is* a transactional outbox; one fan-out job means one failing subscriber retries all. | Agree (author flagged too). | One River job per `(event, subscriber)` with unique-job dedupe; drop `outbox`/`event_delivery`; audit is the history. |
| MED | **Pack `Routes` contradict "nobody hand-writes a route"** and provider webhooks must be *unauthenticated* yet mount under the tenant-guarded prefix. | Agree. | `RouteGroup` is webhook/callback-only with explicit `Public()` registration (tenant from path, no session); anything clients call goes through a core endpoint. |
| MED | **Event payloads in `pkg/sdk/events` freeze core internals into the public SDK**; `LegalRules` as one fat interface conflicts with the "optional interfaces only" evolution rule. | Agree. | Payloads = ids + type + version; handlers fetch via readers. Split `LegalRules` per concern (quorum, weights, thresholds, schedule validation, transfer rule). |
| MED | **`updated_since` sync has no tombstones**; ETags on cursor-paginated RLS lists are costly. | Agree. | v1: drop `updated_since`/ETag, refetch first pages (lists are small). Later: `deleted_at` tombstones. |
| MED | **Offline write queue has an ID problem** (offline ticket + offline comment; attachment sequencing). | Agree. | Cut offline writes from mobile 1.0 (retry-on-failure only). If ever added: client-supplied UUIDv7 ids accepted API-wide, as a deliberate contract. |
| MED | **iOS Notification Service Extension is an unscoped Swift component** (shared keychain group, token refresh, fetch within time budget, localization). | Agree (author flagged). | Ship 1.0 with generic titles, no NSE; scope NSE (~2 weeks) for 1.1. Add `instance_id` to the relay `data` allowlist for multi-account routing. |
| MED | **OpenAPI 3.1 across three generators** (`kin-openapi`/`oapi-codegen` and `openapi-generator` Dart have partial 3.1 support; `type: [..,"null"]`, `const`, `examples` can generate silently wrong code). | Agree. | Author in the 3.0.3-compatible subset; B0 spike verifies nullable/oneOf/enum in all three; open enums (`enumUnknownDefaultCase`) so additive values don't crash old store builds. |
| MED | **Two upload paths in three clients** (presigned S3 needs bucket CORS, public MinIO hostname, CSP exception). | Agree. | v1: server-proxied uploads only (streams to S3 when configured). |
| LOW-MED | **Pure-Go PDF with Turkish glyphs**: `fpdf` core fonts are cp1252 and print `ğ ş İ ı` as garbage; Gotenberg breaks "one binary + Postgres". | Agree; decide now, not at B2. | Pure Go with an embedded TTF (e.g. Noto Sans) — rule out the sidecar. |
| LOW | Idempotency store needs principal in the key, an "in progress" row (insert-first, unique violation → 409) to stop racing retries, and all `POST` creates should honour the header (mobile queue relies on it beyond the money subset). | Agree. | Backend §8. |
| LOW | `init()` registration vs an explicit `var Official = []sdk.Pack{…}` slice; Postgres-backed rate-limit fallback; monthly Flutter tracking promise; React 19 + RJSF compatibility unverified; Vite CSP (`html.cspNonce`, chunk-load errors across upgrades → force reload). | Agree, all minor. | Explicit slice; in-memory limits only; drop the tracking promise; verify RJSF at W0; note Vite details. |

---

## 7. Sequencing and consistency

The consistency reviewer found 6 high, 21 medium and 27 low discrepancies. The ones that would mislead an engineer on day one:

| Sev | Finding | Action |
|---|---|---|
| HIGH | **Client endpoints scheduled in B5 are consumed at M0–M2**: discovery document, `/v1/instance`, `/v1/me` with effective permissions (web W0 builds its sidebar from it), dashboard aggregate (mobile MB1), image variants (web W3). | Discovery + `/v1/me` (with permissions) → B0/B1; dashboard, variants → B2/B3; only devices, web-login link and checkout stay in B5. |
| HIGH | **Bank statement import is an M2/W2 feature but a B3 backend deliverable**; the core generic CSV importer is named in the packs plan only; overview §3 says "importers via packs". | Import framework + core CSV importer + manual matching → B2; matching *rules* → B3. Fix overview §3. |
| HIGH | **Push relay identity defined three ways** (`PUSH_RELAY_URL/TOKEN` vs `server_id/secret` vs device `relay_id`); no mapping `relay_id → url/secret/app_id`; no way to serve two apps/relays from one server. | Backend §12/§17 adopt a relay config list keyed by `relay_id`; state whether the official relay is default-on (recommend: off until enrolled). |
| HIGH | **Discovery document schema sketched in three places with different fields** (`auth_methods` strings vs objects; `relay_enrollment_challenge` and `sdk_version` missing from the mobile example). | Backend §8 becomes the single normative schema; others reference it. |
| HIGH | **File endpoints break the path convention** (`/files` without `/v1/orgs/{org}`) and ship in B3 while M1 announcements and M2 bills have attachments; `announcement_attachment` doesn't exist. | `/v1/orgs/{org}/files…`; `file` table + local storage → B1; add `announcement_attachment`. |
| MED | `pkg/sdk` placement contradicts itself (inside the AGPL `server` module vs a separate module with its own semver); `testkit` "boots an in-memory core" so it can't live in the Apache module. | Top-level `sdk/` module with its own `go.mod` and `LICENSE`; `testkit` as a separate AGPL module (or drives a server binary). |
| MED | **e-Devlet** is "demoable" at M6 in overview/backend/mobile but "dark until agreement" in packs — and the domain review says the agreement is unobtainable in practice for a private project. | Cut from all milestones; TCKN checksum + manual verification only. |
| MED | Reminders/"send to resident" at M2 need B3 notifications; consents and export requests at mobile M5 need backend B7 privacy; web's M5 items have no W row; mobile deep-links to `<server>/app/…` which doesn't exist. | Pull email channel into B2 or move reminders to W3; consents + export *request* → B5, erasure → B7; add a W row at M5; fix the mobile path. |
| MED | Secret-field marker `x-secret` vs `format: secret`; `x-ui` vs "uiSchema"; `x-stability` undefined; pack "codes catalog endpoint" doesn't exist in backend; `OnEnable`/`OnDisable`/`ValidateSettings`/"platform hooks" absent from the SDK surface; late fee owned by both `LegalRules` and `LateFeePolicy`; TR integration counts differ (one vs two banks, one vs two SMS providers, four vs seven templates); module names `tenancy` vs `property`, `platformadmin` and `integrations` missing from the layout; CLI/env catalogue incomplete (`push-relay enroll`, `packs purge`, `export-org`, `INSECURE_DEV`, `CLAMAV_ADDR`, `AUTO_MIGRATE`); platform-admin API never specified; magic-link/invite URL formats defined by each client, never by the backend; notification/event/push `type` vocabularies drift; dev-compose and Taskfile paths disagree; mobile's "starts at B3" contradicts its own mitigation. | One pass over backend §3/§8/§10/§12/§17 as the normative owner of each; a "spec extensions" subsection listing `x-openbekci-roles`, `x-idempotent`, `x-ui`, `x-secret`, `x-stability`; packs §6.3 authoritative for TR counts; overview marks pack UI bundles post-1.0. |
| LOW | 27 smaller items (unit-type lists, locale fallback chain in mobile, `device` columns, `is_platform_admin` naming, `deliveries` optional vs committed, tag names `v*` vs `mobile-v*`, SDK freeze timing B6 vs P3, stale "backend updated" notes, a `tr.identity.edevelet` typo, …). | Fix during the same pass; tracked in the annex. |

---

## 8. Scope, estimates and delivery

| Finding | Judgment |
|---|---|
| Face-value total ≈ 117 engineer-weeks (backend 35, web 28, mobile 26, packs/infra ≈ 28). Realistic ≈ 220–260: B1 is 7–9 weeks not 4 (passkeys + TOTP + magic link + sessions + invitations + import + authz harness + audit + settings + announcements); B2 is 10–12 not 6; B3 lists eight subsystems in 5 weeks; web mirrors 60+ screens plus a design system, RTL, a11y, Storybook, visual tests, RJSF; mobile 1.0 includes multi-account, offline queue, a new relay on two platforms, NSE, F-Droid. | Agree. The milestones are "complete feature areas", not vertical slices, and the per-component parallelism assumes three engineers. |
| The definition of done (spec PR, authz row, RLS test, audit event, catalogs, RTL check, user docs, ADR) is a 30–50 % tax per feature. | Keep spec/authz/RLS/audit (cheap once the harness exists); drop mandatory docs and ADR per feature until there are contributors to read them. The authz matrix harness itself is 1–2 weeks and unestimated. |
| SDK freeze at M6 one milestone after its first consumer (TR-1) appears. | Build TR-1 *as* the SDK is built; freeze at 1.0 or later. For a minimal release the TR late-fee rate needs no pack: core "configurable % per month, default 5" covers it. |
| App Store submission needs a reviewer-accessible demo instance; Google OIDC on the hosted instance triggers Apple's Sign-in-with-Apple requirement. | Add to the dependency graph if/when mobile ships. |

**Cut from v1 without regret** (good design, wrong decade): `account_balance` cache, outbox tables, presigned uploads, statement caching; JWT; passkeys, TOTP, generic OIDC, API keys, webhooks, SSE; mobile offline writes, multi-account, UnifiedPush/F-Droid, NSE, white-label tooling, `updated_since`/ETag; Helm, zero-downtime migration CI, observability compose stack, Postgres rate-limit fallback; Storybook interaction tests, visual snapshot matrix, Lighthouse CI, per-milestone WCAG manual passes; `tsvector` search, digests, per-org template overrides, matching *rules* (keep manual matching); `obkbuild`, conformance suites, `packs/example`, separate pack versioning, paper packs (keep as a one-day exercise before the SDK freezes); e-Devlet; org export/import, `rotate-keys`, demo instance, docs-site generator choice. Document versions, read receipts, parcel log, live tally polling: later.

**Dangerously thin — design before code**: everything in §3 marked HIGH, erasure (§5), payment-provider settlement, receipt numbering, budget entity, secrets in backups.

---

## 9. Proposed first public release — "0.1: a Turkish site runs on it"

Target ≈ 45–50 engineer-weeks (two people for ~6 months, or one for ~9–10). Web only. Tag `0.x`; `/v1` is not frozen.

| Area | In |
|---|---|
| Server | Org / site / building / unit with **allocation keys**; persons, occupancies (with debtor kind), staff memberships; built-in roles; email + password, magic link, invitations, **SMS OTP via one provider (D5)**; cookie + opaque bearer sessions; RLS with the §6 fixes and composite FKs; audit; CSV/XLSX import of units/people **with opening balances** and dry-run |
| Money | Default chart with funds; **budget → charge plans** (equal, by key, fixed table); charge generation with catch-up; manual payments (cash/transfer) with policy-driven allocation and manual override; **late fee as accrual + monthly crystallization** (configurable %, default 5, waive, freeze); simple expenses and vendor bills; transfers and refunds; unit and person statements (HTML/CSV; PDF with embedded TTF); cash-basis gelir-gider, trial balance, receivables aging, debtor list, collector reconciliation; lock date; gap-free receipt numbers; generic CSV bank import with manual matching; `admin anonymize-person` that actually works |
| Ops | Announcements (Markdown, audience), tickets with photos (server-proxied uploads, local storage), documents (folders, visibility), email + in-app notifications, collection stages (reminder 1/2, ihtarname export) |
| Web | Management + portal for the above; en/tr catalogs; logical CSS from day one, RTL *testing* deferred; a handful of Playwright flows |
| Infra | Compose (Caddy + Postgres + nightly `pg_dump` **+ secrets**), GoReleaser binaries/images, install + backup/restore docs |

**Explicitly out**: mobile and relay, pack SDK (TR rules are core settings), meetings/voting, bookings, visitors/gate, passkeys/TOTP/OIDC, webhooks/API keys, Helm, observability stack, e-Devlet, iyzico, export/erasure UI, RJSF.

**First additions after 0.1**, in order: meetings and decisions with the KMK paperwork (annual pain), bank auto-matching rules (weekly pain), approvals, mobile.

---

## 10. Action plan

**A. Decide (owner)** — D1–D5 in §2.

**B. Rewrite the domain model** (backend §4/§9; then overview §3 and the client feature lists):
1. `debtor_kind` on plans and charges; per-person statements; occupancy-derived portal rights; membership = staff/governance only.
2. Allocation keys per site; cost groups; building scope on lines; fund dimension.
3. `budget` + `fiscal_year` + settlement + lock date; opening balances; effective-dated plan rates; extended charge kinds and chart; transfers/refunds/light expenses; allocation-basis snapshot; receipt sequences.
4. Late fee as accrual/crystallization with a reconciling, catch-up job; `PaymentAllocationOrder`; collections (`collection_case`, stages, freeze).
5. Unit transfer/move-out use case; approvals; meeting paperwork entities; corrected KMK voting rules; site-owned governance memberships and site export/import.
6. Erasure strategy; PII retention; formal notices.

**C. Fix mechanisms** (backend §5/§6/§8/§11/§13, infra §4): RLS code, BYPASSRLS role, composite FKs, pooler text; opaque tokens; River as outbox; drop balance cache, presigned uploads, period state machine; pack routes = webhooks only; event payloads = ids; 3.0.3 subset + open enums; server-proxied uploads; embedded-TTF PDF; relay SSRF/replay/enrollment; secrets in backups; placeholder-key refusal.

**D. Re-sequence and reconcile** (all docs): §7 table; move B5 client endpoints earlier; bank import to B2; normative owners for discovery schema, relay config, spec extensions, CLI/env, URL formats, type vocabularies; `sdk/` as a top-level module; e-Devlet out; packs §6.3 authoritative for TR counts.

**E. Re-baseline the roadmap** to §9, then 0.2 … with the current M1–M7 content redistributed and estimates doubled.

**F. Verify with a Turkish legal/practice maintainer before encoding** (claims the domain review marked uncertain): daily pro-rating and current rate of gecikme tazminatı; article mapping for 4/5 and unanimity thresholds and any conflict-of-interest exclusion; toplu yapı assembly periodicity and temsilciler kurulu rules; whether the denetim raporu needs a separate notarised book and where expenses are transcribed; validity of email/SMS as tebligat under a yönetim planı; legality of binding electronic votes; current central-heating consumption-split thresholds; KVKK 2024 transfer regime; Kimlik Bildirme Kanunu duties for managers; kapıcı wage tax exemption scope; TBK partial-payment allocation order.

---

## Annex — low-severity consistency items (for the reconciliation pass)

Stale "backend updated" notes in packs §2 and infra §13 · overview §11 says core is email-only (backend has phone first-class) · `DELETE /v1/me/sessions/current`, `mfa_required`, `invalid_grant`, `pack_error`, `X-OpenBekci-App` undefined in backend · "tenant" (occupancy kind) vs `resident` (role); `representative` occupancy unexplained; board "approve bookings" has no permission · unit type lists differ (overview vs backend) · overview §6.3 omits API keys · mobile locale fallback skips org default · mobile asserts Weblate while overview leaves it open · `device` lacks `push_kind`, `app_version`, `locale`, UnifiedPush endpoint · push message shape differs (backend `collapse_key` in `data` vs infra message-level, plus `priority`, `ttl_seconds`, `apns.generic_title_key`) · visitor/meeting QR codes, `app_lock_required` policy, live quorum endpoint have no backend definition · block-level assemblies vs `meeting.site_id` only · no SDK point for role labels · `tr.identity.edevelet` typo · `ReferenceData` registered but never exposed over HTTP · Vite proxy lists differ (§3 vs §17) · B0 fixes client generators before the W0/MB0 spikes; `clients/ts` never consumed by web · `foss` APK attached to `v*` but mobile tags are `mobile-v*` · B1 "CSV" vs "CSV/XLSX"; W1 vendors vs B2 vendors · I1 load-test exit vs k6 "B6 onwards" · SDK freeze B6 vs P3 · thumbnails B3 vs `?variant` B5 · overview §6.7 "generic forms in the clients" should say web · overview M5 omits B5 · `membership` hangs off `person`, not `user` (backend §5 wording) · `is_platform_admin` vs `platform_admin` · `deliveries` optional (backend) vs committed (mobile gate mode).
