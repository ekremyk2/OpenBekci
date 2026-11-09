# Paper packs: Germany (WEG) and the Netherlands (VvE)

Status: the generality check of [plan 04-packs.md](../plans/04-packs.md) section 7, done before the `sdk/` v1.0 freeze. No code
was written for these packs; each row says how a real pack would meet one requirement with the extension points of
[the authoring guide](README.md). Legal statements are from the maintainer's reading of the statutes and carry **(verify)**
until a lawyer of the country has checked them, as the Turkish pack does in `packs/tr/LEGAL.md`.

Result: 51 requirements; most map onto existing extension points, some need a convention, and seven were gaps. Two are closed
in SDK 1.0.0 with optional interfaces; five need host work and stay post-1.0 additions (section 4), none of them needs an
existing interface to change. That is the point of the exercise: a shape that would have needed widening is fixed before the
freeze, not after.

Legend. **Covered**: an extension point does it as is. **Convention**: covered, with a rule a pack author must follow.
**Gap (fixed)**: new optional interface in SDK 1.0.0. **Gap (post-1.0)**: additive later, workaround given.

## 1. Germany: Wohnungseigentumsgesetz (WEG), reform in force since 1 December 2020

Site = Wohnungseigentümergemeinschaft (WEG), organization = Verwaltung (property manager). Currency EUR, locale `de`.

| # | Requirement | Extension point or gap |
|---|---|---|
| D1 | The owners' meeting is quorate whatever the attendance; no second meeting (WEG section 25, 2020) (verify) | Covered: `QuorumPreset` with basis `none` for both calls; `SchedulePreset` with the second-meeting days at zero |
| D2 | Notice in text form, as a rule at least three weeks ahead (section 24 IV) (verify) | Covered: `SchedulePreset.MinNoticeDays` 21; it is a "should", and the site may deviate with a reason (core) |
| D3 | A meeting at least once a year (section 24 I) (verify) | Covered: `SchedulePreset.OrdinaryAssemblyMonth` is a month; with no fixed month the pack picks the usual one (core reminders) |
| D4 | Simple majority of the votes cast; abstentions do not count (section 25 I) (verify) | Convention: `ThresholdDefaults` `of: "present"`; whether abstentions count is core governance behaviour, not a pack matter |
| D5 | Vote per owner (Kopfprinzip) by default; the Gemeinschaftsordnung may use shares or value | Covered: `VoteWeightPreset` `per_unit` (default), `by_share` for deviating sites; per-site, editable |
| D6 | Co-owners of one unit vote with a single voice | Covered: `CoOwnerRepresentation: single_representative` |
| D7 | Proxies (Vollmacht), limits only by the Gemeinschaftsordnung | Covered: `ProxyCapPerPerson` zero (no cap), site-editable |
| D8 | Majority for structural changes and cost keys (sections 16 II, 20), unanimity or a deed otherwise (verify) | Covered: `ThresholdDefaults.ByTopic`; "needs all owners" is `of: "all"` with 1/1 |
| D9 | Circular resolutions in writing (section 23 III) and online participation (section 23 I) | Not an SDK matter: core governance modes; the pack can only set the threshold of the topic |
| D10 | Wirtschaftsplan (budget) decided by the owners; Hausgeld is the monthly advance (section 28 I, II) | Covered: core budget, dues plan and charges; pack contributes the document template `de.wirtschaftsplan` (`DocumentTemplate`, pack-only kind) |
| D11 | Hausgeld split by co-ownership share (MEA) unless the Gemeinschaftsordnung says otherwise | Covered: core allocation key `share`; a different statutory key would be an `AllocationPolicy` (`de.*`) |
| D12 | Erhaltungsrucklage (maintenance reserve) of reasonable size (section 19 II no. 4) | Covered: the core reserve fund. Limitation: fund display names come from the core locales (`server/locales`), a pack cannot rename a fund; `ChartOfAccounts` renames accounts only. Fix in core: add `de` and `nl` locales |
| D13 | Jahresabrechnung (annual statement) per owner, owners decide on the settlement amounts (section 28 II) | Covered: core fiscal year close and per-unit statements; pack template `de.jahresabrechnung`; the decision uses the core "decision with effects" |
| D14 | Vermogensbericht (asset report, section 28 IV) | Covered: pack template `de.vermoegensbericht` fed by `LedgerReader` through the render data |
| D15 | Deadline for the annual statement and the budget (the owners may fix one) | Gap (fixed): `ComplianceChecker` reports "annual statement overdue" for the dashboard |
| D16 | Default interest: base rate plus 5 points a year, base rate changes twice a year (BGB 288; owners fix consequences of default, section 28 III) (verify) | Convention: `LateFeePreset` is read when a site is created, so a changing base rate is a site setting the manager edits, or a pack job that proposes it; the owners fix a rate by resolution in any case |
| D17 | Owner changes: the buyer owes Hausgeld due after the change of the land register, the seller what fell due before (verify) | Convention: `TransferRule` `stays_with_person` is the default; due-date-based liability is a future value of the open string `DebtOnMoveOut` (core), additive |
| D18 | SEPA Core direct debit: mandate per owner (mandate reference, signature date, creditor id), pre-notification, pain.008 collection files, returns | Mandates in `pack_de_*` tables (`RegisterMigrations`, `Services.DB`); collection by a periodic `RegisterJob` that writes pain.008 through `FileStore.Put` and tells the manager with `Notifier`; returns arrive as bank statement lines. **Gap (post-1.0)**: no authenticated pack routes for mandate screens; no mandate-aware `PaymentProvider` |
| D19 | Direct debit through a provider such as GoCardless | Covered for the online flow: `PaymentProvider` with webhooks; a return after payment is `EventKindRefunded` |
| D20 | Bank statements in camt.053 and MT940 (the manager's bank delivers one file with the account of every WEG) | `StatementImporter` covers single accounts. Gap (fixed): `MultiStatementImporter` returns one statement per IBAN |
| D21 | Owner reference on transfers | Covered: `ReferenceHinter` ("Hausgeld", unit number, period) and the importer lifts it into `StatementLine.Reference` |
| D22 | German IBAN, Steuer-ID, USt-IdNr, postcode checks | Covered: `Validator` with `Country() == "DE"` |
| D23 | Address format (street, house number, postcode, city) | Covered: `AddressFormat` |
| D24 | Fund of a WEG sits on an account in the WEG's own name | Covered: per-site bank details (core) |
| D25 | Data protection text (DSGVO) and the privacy officer's contact | Covered: `ConsentTexts` for `de` |
| D26 | German SMS gateway for sign-in codes | Covered: `SMSProvider` |
| D27 | Labels and documents in German | Covered: `Catalog`, template `Locales()` |
| D28 | Heating and utility cost statements (HeizkostenV) | Out of scope for the pack; consumption data is core "expense allocation by key" with a table key; a metering import would be a core feature |

## 2. Netherlands: Vereniging van Eigenaars (VvE), Burgerlijk Wetboek book 5

Site = VvE, organization = beheerder. Currency EUR, locale `nl`. The splitsingsakte (deed of division) and the reglement
(model reglement or own) are per building, so presets are defaults, and the per-building values live in site settings.

| # | Requirement | Extension point or gap |
|---|---|---|
| N1 | Votes follow the share of each apartment right (breukdeel), set in the splitsingsakte | Covered: `VoteWeightPreset` `by_share` with the unit share of the core allocation key; the deed's fractions are imported per unit |
| N2 | No owner may cast more than one third of all votes (art. 5:121 BW) (verify) | Covered: `PerPersonCapNumerator` 1, `PerPersonCapDenominator` 3 |
| N3 | The deed or the reglement may change the vote rule per building | Covered: presets are defaults; the site edits `vote_weight` (the "pack default" marker disappears) |
| N4 | Quorum and majority as the reglement says (absolute majority of votes cast in the model reglement) (verify) | Covered: `QuorumPreset` `units_and_share`, `ThresholdPreset`; second meeting days if the reglement has the rule |
| N5 | Notice period and the annual meeting within a period after the financial year (reglement) (verify) | Covered: `SchedulePreset` (notice 14 days is the common model value (verify)); the month is a default only |
| N6 | Reservefonds: yearly contribution of at least 0.5 percent of the rebuilding value, or the amount of a multi-year maintenance plan (MJOP); statutory since 2023 (verify) | Covered: the core reserve fund and `reserve_contribution` income. The duty check is a gap (fixed): `ComplianceChecker` warns when the reserve contribution is below the minimum |
| N7 | MJOP as the base for the contribution | Convention: the site records the yearly amount in a budget line; the pack never reads the MJOP document |
| N8 | Fund names "reservefonds" and "exploitatiefonds" | Same limitation as D12: core locale `nl` |
| N9 | Monthly bijdrage (contribution) per apartment right, split by the deed's key | Covered: core dues plan with allocation key; `AllocationPolicy` `nl.*` if the deed's key is not a share |
| N10 | iDEAL payments for one-off and overdue contributions | Covered: `PaymentProvider` (Mollie, Adyen, Buckaroo style hosted checkout), webhooks, EUR. Convention: the payment method is `card:<provider>` in the core, so the provider id (`nl.mollie`) carries the rail; a kind label is a post-1.0 addition |
| N11 | SEPA direct debit (incasso) is the normal way to pay bijdrage | Same as D18 |
| N12 | Late payment and interest (statutory interest, the VvE may charge costs by reglement) (verify) | Convention: `LateFeePreset` as D16 |
| N13 | The buyer is jointly liable for contributions of the current and previous year, the notary settles at transfer (art. 5:113 BW) (verify) | Convention: `TransferRule` as D17; a new value of `DebtOnMoveOut`, additive in the core |
| N14 | Bank statements: camt.053 (all large banks), MT940, bank CSV | `StatementImporter` per format; `MultiStatementImporter` for a beheerder's multi-account file (as D20) |
| N15 | Payment reference | `ReferenceHinter` |
| N16 | Dutch IBAN, BSN (eleven-test), KvK number, BTW number, postcode (`1234 AB`) | Covered: `Validator` with `Country() == "NL"` |
| N17 | Address with house number and addition | Covered: `AddressFormat` fields `house_number`, `house_number_addition` |
| N18 | Documents: convocation, minutes, begroting, jaarrekening, ALV attendance list | Covered: `DocumentTemplate` per kind and locale `nl` |
| N19 | Annual statement and budget approved by the meeting; reporting duty in the reglement | `ComplianceChecker` (as D15) for deadlines; the approvals are core decisions |
| N20 | VvE chamber of commerce registration and the "VvE-beheerder" licence | Out of scope: a manager's own compliance, not the site's |
| N21 | AVG privacy text | Covered: `ConsentTexts` for `nl` |
| N22 | Dutch SMS gateway | Covered: `SMSProvider` |
| N23 | Tenants are not members; only owners vote | Covered: core roles and the vote weight rules (owners only); no pack involvement |

## 3. What changed in the SDK

| Gap | Fix in SDK 1.0.0 | Host call site |
|---|---|---|
| A manager's bank file holds several accounts (D20, N14) | `MultiStatementImporter` and `AccountStatement` (optional interface of a `StatementImporter`) | Bank import module, 1.x minor |
| Statutory duties the core cannot know (D15, N6, N19) | `ComplianceChecker` and `ComplianceFinding` (optional interface of a `Pack`) | Site dashboard and the nightly check, 1.x minor |

## 4. Gaps that stay post-1.0 (additive, no SDK change needed first)

These do not block the freeze because each is an optional interface or a new constructor next to existing names, which the
evolution rule allows. A pack for either country works today with the workaround.

| Gap | Why it waits | Workaround today |
|---|---|---|
| Authenticated pack routes (mandate screens, a per-owner pack form): `Route` can only be `Public`, and the host rejects anything else | Needs a host design for permissions and the OpenAPI position of pack routes | Mandates are entered by the manager through pack settings or a CSV import job; the web app gets no pack screen |
| Mandate-aware `PaymentProvider` (create mandate, collect, revoke) | The shape must be validated against a real provider (GoCardless, Mollie), not guessed on paper | Provider calls from a periodic job, posting with `PaymentRecorder.RecordProviderPayment` |
| Payment method kind (`ideal`, `sepa_direct_debit`) instead of `card:<provider>` | The database check and the clients' open enum change together | The provider id names the rail |
| New values of `TransferDefaults.DebtOnMoveOut` (due-date owner, joint liability) | Core ledger behaviour first | Site chooses `stays_with_person` and edits statements |
| A pack cannot rename funds or override core labels | Core locale files are the right place | Add `de` and `nl` core locales |
