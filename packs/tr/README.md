# OpenBekci Turkish pack (`packs/tr`)

The first official pack (milestones P1 "TR-1" and P2 "TR-2"): the Turkish rules and papers on top of the core, card payments, bank statements and SMS. Licence AGPL-3.0.
Legal status of every value: see [LEGAL.md](LEGAL.md). Plan: `docs/plans/04-packs.md` section 6.

Enable it per organization (`POST /v1/orgs/{org}/packs/tr/enable`); it is in the default build and absent from
`-tags nopacks`. A site without the pack stays fully usable with the core's generic defaults and can set every value by
hand; with the pack, the values below are **pack defaults** the site may edit.

## What it contributes

| Extension point | Content |
|---|---|
| `QuorumPreset`, `ThresholdPreset`, `VoteWeightPreset`, `SchedulePreset`, `TransferRule`, `LateFeePreset` | Law 634 defaults, written into the site settings `quorum`, `thresholds`, `vote_weight`, `schedule`, `transfer_rule`, `late_fee_policy`. `presets_test.go` has the scenario tests ("24 units, 18 owners present holding 55 % of shares, first call: quorum met") |
| `DocumentTemplate` x11 | `tr.call_letter`, `tr.attendance_list` (hazirun cetveli), `tr.minutes`, `tr.delivery_checklist`, `tr.proxy_form` (vekâletname), `tr.decision_book_draft`, `tr.receipt` (aidat makbuzu), `tr.statement`, `tr.closing_statement`, `tr.debt_certificate`, `tr.demand_letter` (ihtarname) |
| `ChartOfAccounts` | Turkish account names (Tek Düzen wording where useful); names only |
| `AddressFormat`, `ReferenceData` | il / ilçe (81 / 973, no mahalle: see LEGAL.md), public holidays 2025 to 2027, served at `GET /v1/orgs/{org}/reference/{kind}` (`province`, `district`, `holiday`, `address_format`) |
| `Validator` | vergi numarası (and TCKN as tax id), TR IBAN, plate |
| `ConsentTexts` | KVKK aydınlatma metni and açık rıza per purpose (identity, contact, financial, camera), tr and en, at `GET /v1/orgs/{org}/privacy/consents` |
| `Catalog` | tr and en labels for all of the above, in the `pack_labels` section of `GET /v1/orgs/{org}/codes` |
| `SettingsSchema` | `tr_kvkk_data_controller` (site), `tr_payments_iyzico_*`, `tr_sms_iletimerkezi_*` (org and site), `tr_bank_formats` (org) |
| `PaymentProvider` | `tr.iyzico`: hosted checkout form, callback and signed webhook, status, full refunds, daily settlements. Flow, assumptions and recording: [PAYMENTS.md](PAYMENTS.md) |
| `StatementImporter` x2 | `tr.ziraat`, `tr.isbank`: CSV (UTF-8 or windows-1254) and XLSX, table-driven format files in `bankformats/`, reference code `<unit>-<period>` lifted into the line reference. Synthetic fixtures: [BANKS.md](BANKS.md) |
| `SMSProvider` | `tr.iletimerkezi`: İleti Merkezi send-sms JSON API, every message sent as operational (`iys` 0); an organization selects it with the setting `sms_provider` = `tr.iletimerkezi` |


## SMS: İleti Merkezi and İYS

`tr.iletimerkezi` sends through `POST https://api.iletimerkezi.com/v1/send-sms/json` with the credentials in the request body
(`tr_sms_iletimerkezi_username` is the API key and `tr_sms_iletimerkezi_password` the API hash that the İleti Merkezi panel
generates, `tr_sms_iletimerkezi_sender_id` the approved sender). The pack reads them at organization scope and sets
`iys` to `"0"` on **every** message: the software sends operational notices only (sign-in codes, dues and meeting notices,
receipts); marketing is out of scope and the İYS consent flow is not implemented (LEGAL.md). Status codes mapped: 401 to
`ErrCredentials`, 451 (duplicate), 452 and 466 (recipient), 400, 402 and 450 to `ErrRejected`, transport and 5xx failures to
`ErrUnavailable`. API access must be switched on in the panel (Ayarlar, Güvenlik, Erişim İzinleri).

`ValidateSettings` checks the shape of the iyzico and İleti Merkezi settings. It tests the İleti Merkezi credentials with a
`get-balance` request **only when the pack was built with `tr.WithSMSTestEndpoint(url)`** (tests and staging): a production
build never sends anything to the provider when an administrator saves settings.

## Existing Turkish organizations

Organizations created before the pack existed have `country_code` `TR` (the default) and no enablement row, and would
lose the Turkish governance defaults that moved from the core into this pack. At every start the server runs
`packs.Host.AutoEnableForCountry`: it enables each pack that lists countries for the organizations of those countries
**that have no enablement row for it**, as a system action (audited as `pack.enable`, presets applied to the sites that
have not changed the settings). An organization that has a row, enabled or disabled, is never touched, so an administrator
who disabled the pack keeps that decision. Note that an organization created later in country `TR` is enabled at the next
start too, unless it disabled the pack first.

## Presets

| Preset | Value |
|---|---|
| Quorum | first call: more than half by number of owners **and** by arsa payı; second call: none |
| Thresholds | default, budget, election: more than half of all by number and share (art. 32); routine: more than half of those present; plan amendment: four fifths (art. 28); common area change: four fifths (art. 19); main structure: all (art. 18) |
| Vote weight | one vote per unit; one third cap per person; one representative per co-owned unit; proxies: two others in buildings of 40 units or fewer, else 5 % of the votes |
| Schedule | call at least 15 days ahead; second meeting 7 to 15 days after; ordinary assembly in January |
| Transfer rule | debt stays with the leaving person |
| Late fee | 5 % per month, default and ceiling (the site may go lower, never higher), per day, no grace |

## Document data

A template receives `sdk.RenderRequest.Data`, JSON-shaped, and returns `text/markdown` (the subset in `sdk.Document`);
the core typesets it. Every key is optional. Money and dates are formatted strings.

| Kind | Keys |
|---|---|
| `call_letter` | `site_name`, `kind` (`ordinary`/`extraordinary`), `title`, `scheduled_at`, `second_call`, `location`, `agenda[{position,title,description,will_be_voted}]`, `recipient`, `qr_token`, `issued_by`, `issued_at` |
| `attendance_list` | `site_name`, `scheduled_at`, `location`, `call` (`first`/`second`), `rows[{no,name,units,proxy_for,votes,share}]`, `eligible_votes`, `eligible_share`, `present_votes`, `present_share`, `quorum_reached`, `no_quorum_required` |
| `minutes` | `site_name`, `kind`, `scheduled_at`, `second_call`, `location`, `call`, `attended`, the votes and shares as above, `quorum_reached`, `items[{position,title,decision{year,seq,text,outcome,has_votes,yes,no,abstain,dissents[{name,text}]}}]` |
| `delivery_checklist` | `site_name`, `scheduled_at`, `second_call`, `location`, `min_notice_days`, `owners[{no,name,units,method,sent_at,proof}]` |
| `proxy_form` | `site_name`, `kind`, `scheduled_at`, `location`, `owner_name`, `units` |
| `decision_book_draft` | `site_name`, `scheduled_at`, `location`, `decisions[{year,seq,text,outcome,has_votes,yes,no,abstain,dissents}]` |
| `receipt` | `site_name`, `receipt_no`, `date`, `payer`, `unit`, `method`, `lines[{desc,amount,advance}]`, `total`, `balance` |
| `statement`, `closing_statement` | `site_name`, `debtor_name`/`person`, `unit`, `period_from`, `as_of`, `move_out_date`, `lines[{date,kind,part,ref,debit,credit,balance}]`, `principal`, `late_fee`, `balance` |
| `debt_certificate` | `site_name`, `person`, `unit`, `as_of`, `has_debt`, `balance`, `principal`, `late_fee` |
| `demand_letter` | `site_name`, `date`, `debtor_name`, `door`, `side_label`, `as_of`, `total`, `principal`, `late_fee`, `days`, `breakdown[{period,due,principal,late_fee}]` |

The core falls back to its generic document when the organization's locale is not `tr` or a template fails.

## Develop

```sh
cd packs/tr && go vet ./... && go test ./...        # needs only the SDK
cd server && go test ./internal/sdktest -run 'TR|Pack'   # the lifecycle test boots the core on PostgreSQL
```

`data/admin_areas.json` is generated data (provinces by plate code, districts by name); update `version` when it
changes. Add a template by implementing it in `templates.go` and listing it in `Templates()`; label it in `catalog.go`.
