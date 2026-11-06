# Decisions — Money

| | |
|---|---|
| Date | 2026-11-06 |
| Decided by | project owner, in a question-and-answer walkthrough |
| Status | Decided. The backend, web and review plans are to be revised to match; until then this file wins over conflicting text in `docs/plans/`. |

Plain-language decisions about how money works in OpenBekci. Each one says what was decided, what it means for the product, and what it changes in the plans.

## M1. Who pays what

**Decided:** when a unit has a tenant, the **tenant owes regular dues (aidat)** and the **owner owes special assessments** (demirbaş, big repairs, capital works). Each charge type has a default payer; the manager can override it per unit.

**Means:** every charge carries "owed by owner" or "owed by tenant" and the person it was assigned to when it was created. A unit has two running balances (owner side, tenant side). The owner sees the whole unit; the tenant sees only the tenant side. Reminders and legal follow-up go to the right person.

**Plan changes:** backend §4.5 `charge.debtor_kind` + `debtor_person_id`; `charge_plan.default_debtor`; statements per unit side and per person; portal visibility rules; collections target a person.

## M2. How costs are split

**Decided:** all splitting rules are available and **each site chooses which rule applies to which dues plan**. Law 634 defaults are pre-selected (staff costs equal per unit, everything else by arsa payı) so a new site works without configuration.

**Rules in the first release:** by arsa payı (land share), equal per unit, fixed amount per unit from an uploaded table (e.g. the heating-meter company's monthly file, or shops at a different rate), by area (m²). Sites with several blocks can split a cost among one block's units only.

**Plan changes:** backend §4.1/§4.5 single `share` → per-site allocation keys with a value per unit; plans and expense allocations name a key and a scope (site / block); defaults by Law 634 in the generic core, Turkish names in the pack.

## M3. Where dues come from

**Decided:** in the first release the manager **sets a monthly aidat amount directly** (per unit, per share, or per plan). The yearly budget (işletme projesi) as the *source* of dues is a later feature.

**Also decided (see M8):** a light, optional **expense budget by category per year** exists from the first release so "budget vs actual" can be reported. It does not drive dues.

**Plan changes:** backend §4.5 `charge_plan` with an effective-dated amount (aidat changes mid-year by decision); a small `budget` table (site, year, expense category, amount); the full işletme projesi workflow (notification, 7-day objection, finality, dues derived from it) moves to a later release.

## M4. Late fees

**Decided:** late fees (gecikme tazminatı, statutory 5 % per month; the site may set lower, never higher) are **computed automatically, shown continuously, booked once a month, and waivable**.

**Means:** a statement or "what do I owe today" always includes the fee accrued so far; at month end the accrued amount becomes one fee line per unit side; managers can waive fees per unit with a reason (audited) and freeze fee accrual for a unit in legal collection. Fees apply to principal only. Backdated payments reduce or reverse the accrued fee automatically.

**Plan changes:** backend §9.7 and §11 — late fee as an on-demand accrual plus a monthly crystallization job that reconciles (expected minus booked = delta); `waive` and `freeze` operations; `late_fee_run` replaced accordingly.

## M5. How residents pay

**Decided:** three channels.

| Channel | First release? | How the system learns about it |
|---|---|---|
| **Cash** with a sequentially numbered receipt (aidat makbuzu), recorded by the manager or whoever collected it | Yes | Entered on the spot; receipt printed or sent by SMS/email; collector tracked for reconciliation |
| **Bank transfer**, matched from the bank's statement file | Yes | Manager uploads the bank's CSV/Excel export; the system suggests the unit for each line (reference text, payer name, amount); manager confirms. Which banks' formats come first is still open (see "open") |
| **Online card payment from the app** | Wanted; arrives with the first payment provider (Turkish pack) | Provider checkout; the payment model is designed for it from the start: gross amount, provider fee, net settlement, refunds, idempotent webhooks, and a clear merchant-of-record setting (site or management company) |

Not chosen for now: bank dues-collection services (kurum tahsilat). Can be added as another import/feed later.

**Plan changes:** receipts need a per-site gap-free sequence; cash by collector with a per-collector cash box; `payment` model gets gross/fee/net and provider identifiers before any provider is built; bank import moves into the money milestone (B2), with *manual* matching first and matching *rules* later.

## M6. Expenses

**Decided:** **simple entry, no approval workflow in the software.** An expense is: what, who was paid, how much, when, from which account (bank or cash), optional receipt photo. Recurring expenses (electricity, kapıcı salary, elevator maintenance) can repeat monthly. Approval of spending is something boards decide in meetings, outside the system; the system records what was spent and shows it in reports.

**Plan changes:** backend §4.6 — a light `expense` entry is the primary path; full vendor bills with due dates and partial payments stay available for sites that want them but are not the default; the `approval` entity proposed in the review is **not** built in the first release. Vendor payment batching, credit notes and tax breakdown remain later items.

## M7. Migration of an existing site

**Decided:** on day one a site imports **per-unit debts from an Excel sheet**, its **bank and cash balances** at the go-live date, and — when sample files exist — an **export from another program (e.g. Apsiyon)**. Unpaid bills at go-live are entered by hand.

**Means:** an "opening balances" step in site setup: units and people from a spreadsheet with a column mapping the manager can adjust, each unit's opening debt (principal, and already-accrued late fee if known, per owner/tenant side), bank and cash opening balances. A dry-run preview shows errors before anything is saved. Opening entries are locked once the first month is closed.

**Plan changes:** backend §8 bulk import gains opening balances with dry-run; an `opening_balance` posting type against an opening-equity account; an importer framework where "Apsiyon export" is one format, built when a sample export is available.

## M8. Reports for the assembly and the auditor

**Decided:** the first release produces:

1. **Cash-basis income and expense (gelir-gider)** for any period, by category — the sheet read at the assembly.
2. **Debtor list (borçlu listesi)** — units with outstanding balance: principal, late fee, total, months overdue. Visible to the manager and board; never public.
3. **Per-unit statement (hesap ekstresi) as PDF** — charges, payments, fees, running balance for a date range; printable and sendable.
4. **Budget vs actual** — against the light category budget from M3.

**Plan changes:** backend §9.10 adds the cash-basis report, debtor list and statement PDF to the money milestone; budget vs actual depends on the `budget` table in M3. Trial balance and receivables aging remain as accountant views.

## Defaults set without asking (change them if wrong)

These have an obvious default; they are listed so nothing is hidden.

| Topic | Default |
|---|---|
| Fiscal year | Calendar year; a site can set another start month |
| Reserve fund (demirbaş / yedek akçe) | Tracked separately from operating money as its own fund with its own balance; special assessments (M1, owner-paid) go to it by default |
| Several cash boxes / collectors | Supported: each person who collects cash has a cash box; "hand over cash to the manager / deposit to bank" is a transfer between accounts |
| Receipts by SMS/email | Yes, since SMS exists in the core from the first release (decision D5) |
| Payment allocation order | Oldest debt first, principal before fee; the manager can always pick a specific charge to pay |
| Currency | One currency per site (TRY for Turkish sites); no multi-currency |
| Corrections | Nothing is edited after the fact; a mistake is reversed with a visible correction and a reason |

## Still open in Money

- Which banks' statement formats to support first (needs real sample files, anonymized).
- Whether to build the Apsiyon importer in the first release (needs a sample export).
- Merchant of record for online card payments: the site or the management company? Affects accounting (client money) and which provider accounts are needed.
- Year-end: carry surplus/deficit forward silently (default) or produce a per-unit settlement. Default: carry forward; settlement comes with the full budget feature.
