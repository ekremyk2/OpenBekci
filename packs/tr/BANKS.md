# Bank statement importers (`tr.ziraat`, `tr.isbank`) and the reference code

Milestone TR-2. Two importers for the CSV and XLSX exports of **Ziraat Bankası** and **Türkiye İş Bankası**, and the
reference-code scheme that makes bank transfers match. Decision (`docs/decisions/money.md`, bank-format open point): the
two banks are built from public knowledge of the export shape with **synthetic fixtures**; real, anonymized exports come
later.

**The fixtures are synthetic** (`trtest/fixtures/banks/README.md`): invented accounts, names and amounts in the shape the
exports are believed to have. The importers have never read a real bank file. The first real export of either bank
will probably need an adjustment of its format file (a header spelled differently, another date layout, a different
amount column); that is a YAML edit and a fixture, not code.

## Formats are data

One YAML file per bank in `bankformats/` (embedded). Each defines:

| Key | Meaning |
|---|---|
| `id`, `name`, `formats` | importer id (`tr.<bank>`), display name, `csv` and/or `xlsx` |
| `delimiter` | CSV delimiter (`;`) |
| `fingerprint` | the headers that must all be in the header row; the header row is searched in the first 40 rows, so a preamble (account number, holder, period) before it is fine. Matching is case, diacritic and punctuation insensitive (`İşlem Tarihi` = `islem tarihi`) |
| `filename_hints` | for an xlsx: a workbook is compressed, its header cannot be read from the first KiB, so detection takes a file name containing one of these (`ziraat_...xlsx`). A CSV is detected by its header; an xlsx whose name has no hint is not detected and the manager picks the importer |
| `date_formats` | Go layouts tried in order; workbook date cells (day serials) are understood too |
| `columns` | header names (aliases) for `date`, `description`, `reference`, `counterparty`, `balance`, and either one signed `amount` or `debit` plus `credit` |
| `skip_row_prefixes` | footer or carry-over rows to ignore (`Toplam`, `Devreden`) |
| `counterparty_patterns` | regexps that lift the payer or payee name out of the description when the file has no column for it |

What the importer does with a file:

- **Encoding**: UTF-8 (byte order mark dropped) when the bytes are valid UTF-8, else windows-1254. Decimal comma, dot
  thousands (`1.250,00`), `TL`/`₺` suffixes, `(30,00)` and trailing minus; workbook numbers (`1250.5`) work too.
- **Amounts**: money in is positive. Debit and credit columns become one signed amount. A zero amount, an unreadable date or an
  unreadable amount is a **row error** (`Statement.Errors`), the rest of the file imports and the core marks the import `partial`.
- **Balances**: when every line has a running balance and they chain, in file order or in reverse (newest first), the
  opening and closing balance are derived and checked by the conformance suite (`opening + total = closing`).
- **Raw rows** hold the source cells keyed by header; the core deletes them after 90 days and everything else is in the
  typed fields.
- **Enabled formats**: the organization setting `tr_bank_formats` (a list of importer ids) restricts which formats may be
  parsed; empty means all.

## Adding a bank: a fixture, not code

1. Write `bankformats/<bank>.yaml` (copy `ziraat.yaml`; the id is `tr.<bank>`).
2. Add an anonymized export to `trtest/fixtures/banks/` and a row to `bankManifest` in `trtest/banks.go` with the number of lines, the total in kuruş and the number of row errors you expect.
3. `go test ./packs/tr/...` and `go test ./server/internal/sdktest -run 'TR'`; `TestEveryFormatHasASample` fails for a format without a fixture.
4. Add the label `tr.<bank>` to `catalog.go` and, in `server/internal/sdktest/tr_p2_test.go`, one conformance test line (`ConformStatementImporter`).

Never commit a real statement. Anonymize names, account numbers, IBANs and descriptions first.

## The reference-code scheme

`<unit>-<period>`, for example **`A-12-2026-03`**: door number `A-12`, period March 2026. The resident writes it in the
description of the EFT, FAST or havale; the importers lift it into `StatementLine.Reference` (whether it was in a
reference column or inside the description); the core's matching (`payments/bankimport.Rank`) scores a reference that
names the unit's door (`0.55`, plus `0.15` when the period is an open charge's) above the payer's name (`0.30`) and the amount
(`0.12` to `0.20`), and offers up to three suggestions the manager confirms.

Rules, because banks mangle descriptions: the period is always last (`YYYY-MM`); the door number has no spaces (a space
becomes a hyphen, Turkish letters are folded to ASCII, upper case); the code is one token. `tr.RefCode(unit, period)`
builds it and `tr.ParseRefCode(text)` splits it; the core builds the same text (`bankimport.BuildReference`).
A bank's own dekont number in the reference column is kept as the reference when the description holds no code.

**Where residents see it:** the pack implements the optional `sdk.ReferenceHinter`, and the core's bank details response
(`GET /v1/orgs/{org}/sites/{site}/bank-details?unit_id=...`) returns `suggested_reference` (`<door>-<current month at
the site>`, for example `A-12-2026-03`) for organizations that have the pack enabled, null otherwise. The seam is
`tenancy.BankReferenceHint`, implemented by the packs host.

