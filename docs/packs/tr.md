# Turkish pack

`packs/tr` is the first official pack (AGPL-3.0). It is in the default server build and absent from `-tags nopacks`. An organization enables it with `POST /v1/orgs/{org}/packs/tr/enable`. Organizations whose country is `TR` and that have no enablement record are enabled automatically at server start; an organization that disabled it keeps that decision.

A site without the pack stays fully usable with generic defaults and can set every value by hand. With the pack, the values below are **pack defaults** that the site may edit.

## What it contributes

| Area | Content |
|---|---|
| Legal presets | Law 634 defaults: quorum, decision thresholds, vote weight and proxies, meeting schedule, debt on transfer, late fee. |
| Documents | Eleven templates: call letter, attendance list, minutes, delivery checklist, proxy form, decision book draft, receipt, statement, closing statement, debt certificate, demand letter. |
| Accounts and data | Turkish account names; provinces (81) and districts; public holidays 2025 to 2027; address format. |
| Validators | Tax number (and TCKN as a tax id), Turkish IBAN, plate. |
| Privacy | KVKK notices and consent texts per purpose, in Turkish and English. |
| Card payments | `tr.iyzico`: hosted checkout, signed webhook, status, full refunds, daily settlements. |
| Bank statements | `tr.ziraat` and `tr.isbank`: CSV and XLSX, table-driven format files, a `<unit>-<period>` reference code lifted from the transfer text. |
| SMS | `tr.iletimerkezi`: every message is sent as operational, never marketing. |

## Defaults in plain words

- **Quorum:** on the first call, more than half of the owners by number **and** by land share; on the second call none.
- **Decisions:** most topics need more than half of all owners by number and share; routine matters more than half of those present; a plan amendment or a common-area change four fifths; changes to the main structure everyone.
- **Votes:** one per unit, a cap of one third per person, one representative per co-owned unit; proxies limited to two others in buildings of 40 units or fewer, otherwise 5 % of the votes.
- **Meetings:** the call at least 15 days ahead; a second meeting 7 to 15 days later; the ordinary assembly in January.
- **Late fee:** 5 % per month, the default and the ceiling.

## Read this before relying on it

The rules implement the maintainers' reading of the law at a given date. They are **not legal advice**, and organizations remain responsible for their own compliance. Every rule with its article, the date of the law text consulted, known ambiguities and which settings the management plan may override is in `packs/tr/LEGAL.md`. Values the maintainers could not confirm stay marked **(verify)** until the pack's legal maintainer signs them off. The pack does not handle tax or employer obligations, e-Fatura, open banking, or İYS marketing consent.

## More

The pack's own documents in the repository: `packs/tr/README.md` (settings, document data), `LEGAL.md`, `PAYMENTS.md` (the iyzico flow), `BANKS.md` (statement formats and the synthetic fixtures). To set up card payments, enter the iyzico credentials in the pack settings at the site or organization scope, depending on who is the merchant of record. For SMS, set the organization setting `sms_provider` to `tr.iletimerkezi` and enter the İleti Merkezi credentials (API access must be switched on in their panel).
